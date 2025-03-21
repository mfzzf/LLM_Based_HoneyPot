package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/mfzzf/LLM_Based_HoneyPot/admission"
	"github.com/mfzzf/LLM_Based_HoneyPot/config"
	"github.com/mfzzf/LLM_Based_HoneyPot/logger"
)

// OllamaProxy 表示Ollama代理服务器
type OllamaProxy struct {
	listenAddr string
	targetURL  *url.URL
	proxy      *httputil.ReverseProxy
	logger     logger.Logger
	admChecker admission.Checker // 添加准入控制检查器
}

// NewOllamaProxy 创建一个新的Ollama代理实例
func NewOllamaProxy(listenAddr, targetAddr string, logger logger.Logger, admCfg config.AdmissionConfig) (*OllamaProxy, error) {
	targetURL, err := url.Parse(targetAddr)
	if err != nil {
		return nil, err
	}

	proxy := httputil.NewSingleHostReverseProxy(targetURL)

	// 自定义请求导向器
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		modifyRequest(req, targetURL)
	}

	// 添加响应修改器
	proxy.ModifyResponse = func(resp *http.Response) error {
		// 如果请求上下文中有请求ID，则记录响应
		if reqID, ok := resp.Request.Context().Value("requestID").(string); ok && logger != nil {
			// 读取响应体
			var bodyBytes []byte
			if resp.Body != nil {
				bodyBytes, _ = io.ReadAll(resp.Body)
				// 重置响应体
				resp.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			}
			// 记录响应
			logger.LogResponse(reqID, resp, bodyBytes)
		}
		return nil
	}

	// 创建准入控制检查器
	var admChecker admission.Checker
	if admCfg.Enabled {
		log.Printf("[初始化] 准入控制已启用: 模型=%s, URL=%s",
			admCfg.ModelName, admCfg.OllamaURL)
		admChecker = admission.NewOllamaChecker(admCfg)
	} else {
		log.Printf("[警告] 准入控制已禁用")
	}

	return &OllamaProxy{
		listenAddr: listenAddr,
		targetURL:  targetURL,
		proxy:      proxy,
		logger:     logger,
		admChecker: admChecker,
	}, nil
}

// 修改请求
func modifyRequest(req *http.Request, target *url.URL) {
	req.Host = target.Host
	req.Header.Set("X-Forwarded-Host", req.Host)
	req.Header.Set("X-Proxy-Agent", "Ollama-Proxy")
}

// 添加一个新的流式响应收集器
type streamCollector struct {
	reqID       string
	logger      logger.Logger
	accumulated []byte
	path        string
	model       string
}

func newStreamCollector(reqID string, path string, model string, logger logger.Logger) *streamCollector {
	return &streamCollector{
		reqID:  reqID,
		logger: logger,
		path:   path,
		model:  model,
	}
}

func (sc *streamCollector) Write(p []byte) (int, error) {
	// 累积流式响应片段
	sc.accumulated = append(sc.accumulated, p...)

	// 记录完整响应（当最后一个片段到达时）
	if bytes.Contains(p, []byte(`"done":true`)) || bytes.Contains(p, []byte(`"done": true`)) {
		// 创建完整响应记录
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Request: &http.Request{
				URL: &url.URL{Path: sc.path},
			},
		}

		// 为聊天响应创建完整的聊天记录
		if strings.Contains(sc.path, "/api/chat") {
			var combinedResponse struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				Model string `json:"model"`
				Done  bool   `json:"done"`
			}

			combinedResponse.Message.Content = sc.getAccumulatedContent()
			combinedResponse.Model = sc.model
			combinedResponse.Done = true

			fullResponseBytes, _ := json.Marshal(combinedResponse)
			sc.logger.LogResponse(sc.reqID, resp, fullResponseBytes)
		} else {
			// 对于generate请求，直接使用累积的响应
			sc.logger.LogResponse(sc.reqID, resp, sc.accumulated)
		}
	}

	return len(p), nil
}

// 从流式响应片段中提取内容
func (sc *streamCollector) getAccumulatedContent() string {
	var fullContent strings.Builder

	// 将所有片段解析为单独的JSON对象
	for _, chunk := range bytes.Split(sc.accumulated, []byte("\n")) {
		if len(chunk) > 0 {
			var response map[string]interface{}
			if err := json.Unmarshal(chunk, &response); err != nil {
				continue
			}

			if message, ok := response["message"].(map[string]interface{}); ok {
				if content, ok := message["content"].(string); ok {
					fullContent.WriteString(content)
				}
			}
		}
	}

	return fullContent.String()
}

// 修改代理请求处理函数，确保准入控制先执行
func (op *OllamaProxy) handleRequest(w http.ResponseWriter, r *http.Request) {
	log.Printf("[代理] %s %s", r.Method, r.URL.Path)

	// 记录初始检查条件
	log.Printf("[调试] 准入控制配置: admChecker为空=%v, 请求方法=%s, 路径=%s",
		op.admChecker == nil, r.Method, r.URL.Path)

	// 记录请求
	var reqID string
	if op.logger != nil {
		reqID = op.logger.LogRequest(r)
	}

	// 准入控制检查 - 所有POST请求都需要检查
	if op.admChecker != nil && r.Method == "POST" {
		log.Printf("[调试] 开始准入控制检查: 请求路径=%s", r.URL.Path)

		// 保存请求体
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("[错误] 读取请求体失败: %v", err)
			http.Error(w, fmt.Sprintf("读取请求体失败: %v", err), http.StatusBadRequest)
			return
		}

		// 重置请求体
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// 执行准入控制检查
		allowed, reason, err := op.enforceAdmissionCheck(r)

		log.Printf("[调试] 准入检查结果: 允许=%v, 原因=%s, 错误=%v", allowed, reason, err)

		// 记录准入控制结果
		if op.logger != nil && reqID != "" {
			op.logger.LogAdmission(reqID, allowed, reason)
		}

		// 再次重置请求体
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		if err != nil {
			log.Printf("[警告] 准入控制检查失败: %v", err)
		} else if !allowed {
			log.Printf("[拒绝] 请求被准入控制拒绝: %s", reason)

			// 返回拒绝响应
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)

			response := admission.CreateDeniedResponse(reason, r.URL.Path)
			w.Write(response)
			return
		}
	}

	// 检测是否为流式请求
	isStreamRequest := false

	// 如果是POST请求且内容类型为JSON，检查是否流式请求
	if r.Method == "POST" && r.Header.Get("Content-Type") == "application/json" {
		bodyBytes, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		var requestData map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &requestData); err == nil {
			// 检查是否流式请求
			if stream, ok := requestData["stream"].(bool); ok && stream {
				isStreamRequest = true
			}
		}
	}

	// 处理流式响应 - 只有通过准入控制的请求才会到达这里
	if isStreamRequest && reqID != "" {
		// 创建自定义ResponseWriter来收集流式响应
		modelName := "unknown"
		if r.URL.Path == "/api/chat" || r.URL.Path == "/api/generate" {
			bodyBytes, _ := io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

			var requestData map[string]interface{}
			if err := json.Unmarshal(bodyBytes, &requestData); err == nil {
				if model, ok := requestData["model"].(string); ok {
					modelName = model
				}
			}
		}

		// 使用流式收集器
		collector := newStreamCollector(reqID, r.URL.Path, modelName, op.logger)
		teeWriter := io.MultiWriter(w, collector)

		// 创建代理ResponseWriter
		proxyWriter := &streamResponseWriter{
			ResponseWriter: w,
			teeWriter:      teeWriter,
		}

		// 设置上下文
		ctx := context.WithValue(r.Context(), "requestID", reqID)
		r = r.WithContext(ctx)

		// 转发请求
		op.proxy.ServeHTTP(proxyWriter, r)
	} else {
		// 非流式请求，使用标准代理逻辑
		if reqID != "" {
			ctx := context.WithValue(r.Context(), "requestID", reqID)
			r = r.WithContext(ctx)
		}
		op.proxy.ServeHTTP(w, r)
	}
}

// 自定义ResponseWriter用于处理流式响应
type streamResponseWriter struct {
	http.ResponseWriter
	teeWriter io.Writer
}

func (w *streamResponseWriter) Write(p []byte) (int, error) {
	return w.teeWriter.Write(p)
}

// 添加到OllamaProxy结构体中的方法
func (op *OllamaProxy) enforceAdmissionCheck(r *http.Request) (bool, string, error) {
	log.Printf("[强制] 执行强制准入检查")

	if op.admChecker == nil {
		log.Printf("[错误] 准入控制检查器未初始化")
		return true, "", nil
	}

	// 读取请求体
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("[错误] 读取请求体失败: %v", err)
		return true, "", err
	}
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	// 将整个请求体当作内容进行检查
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	contentToCheck := string(bodyBytes)
	log.Printf("[强制] 准入检查内容: %s", contentToCheck)

	allowed, reason, err := op.admChecker.CheckContent(ctx, contentToCheck)
	log.Printf("[强制] 准入检查结果: 允许=%v, 原因=%s, 错误=%v", allowed, reason, err)

	return allowed, reason, err
}

// 更新Start方法使用新的处理逻辑
func (op *OllamaProxy) Start() error {
	http.HandleFunc("/", op.handleRequest)

	log.Printf("Ollama代理启动于%s，转发至%s", op.listenAddr, op.targetURL)
	return http.ListenAndServe(op.listenAddr, nil)
}
