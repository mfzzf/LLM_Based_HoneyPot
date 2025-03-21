package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/mfzzf/LLM_Based_HoneyPot/config"
)

// Logger 是日志记录器接口
type Logger interface {
	LogRequest(req *http.Request) string
	LogResponse(reqID string, resp *http.Response, body []byte)
	Close() error
}

// ELKLogger 实现了使用ELK的日志记录
type ELKLogger struct {
	esClient *elasticsearch.Client
	index    string
	enabled  bool
}

// RequestLog 请求日志结构
type RequestLog struct {
	ID        string            `json:"id"`
	Timestamp string            `json:"@timestamp"`
	Method    string            `json:"method"`
	Path      string            `json:"path"`
	RemoteIP  string            `json:"remote_ip"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body,omitempty"`

	// Ollama 特定字段
	LLMRequest *LLMRequestInfo `json:"llm_request,omitempty"`
}

// LLMRequestInfo 存储大模型请求的特定信息
type LLMRequestInfo struct {
	Model       string        `json:"model,omitempty"`
	Prompt      string        `json:"prompt,omitempty"`
	Messages    []ChatMessage `json:"messages,omitempty"`
	System      string        `json:"system,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
}

// ChatMessage 表示聊天消息
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ResponseLog 响应日志结构
type ResponseLog struct {
	RequestID string            `json:"request_id"`
	Timestamp string            `json:"@timestamp"`
	Status    int               `json:"status"`
	Headers   map[string]string `json:"headers"`
	Body      string            `json:"body,omitempty"`

	// Ollama 特定字段
	LLMResponse *LLMResponseInfo `json:"llm_response,omitempty"`
}

// LLMResponseInfo 存储大模型响应的特定信息
type LLMResponseInfo struct {
	Model         string `json:"model,omitempty"`
	GeneratedText string `json:"generated_text,omitempty"`
	Response      string `json:"response,omitempty"` // chat API返回
	Finished      bool   `json:"finished,omitempty"`
	TotalDuration int64  `json:"total_duration,omitempty"`
}

// NewELKLogger 创建一个新的ELK日志记录器
func NewELKLogger(cfg config.ELKConfig) (Logger, error) {
	if !cfg.Enabled {
		log.Println("ELK日志已禁用")
		return &ELKLogger{enabled: false}, nil
	}

	// 配置Elasticsearch客户端
	esCfg := elasticsearch.Config{
		Addresses: []string{cfg.URL},
	}

	// 设置认证方式：优先使用API Key（如果提供），否则使用用户名/密码
	if cfg.APIKey != "" {
		esCfg.APIKey = cfg.APIKey
		log.Println("使用API Key认证Elasticsearch")
	} else if cfg.Username != "" {
		esCfg.Username = cfg.Username
		esCfg.Password = cfg.Password
		log.Println("使用用户名/密码认证Elasticsearch")
	}

	client, err := elasticsearch.NewClient(esCfg)
	if err != nil {
		return nil, fmt.Errorf("创建elasticsearch客户端失败: %w", err)
	}

	// 检查连接
	res, err := client.Info()
	if err != nil {
		return nil, fmt.Errorf("连接elasticsearch失败: %w", err)
	}
	defer res.Body.Close()

	if res.IsError() {
		return nil, fmt.Errorf("elasticsearch返回错误: %s", res.String())
	}

	log.Printf("成功连接到Elasticsearch: %s", cfg.URL)

	return &ELKLogger{
		esClient: client,
		index:    cfg.Index,
		enabled:  true,
	}, nil
}

// LogRequest 记录请求并返回请求ID
func (l *ELKLogger) LogRequest(req *http.Request) string {
	if !l.enabled {
		return ""
	}

	reqID := fmt.Sprintf("%d", time.Now().UnixNano())

	// 读取请求体（如果有）
	var bodyStr string
	var llmRequestInfo *LLMRequestInfo

	if req.Body != nil && req.Header.Get("Content-Type") == "application/json" {
		bodyBytes, err := io.ReadAll(req.Body)
		if err == nil {
			// 重新设置请求体，因为读取是破坏性的
			req.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
			bodyStr = string(bodyBytes)

			// 解析LLM请求信息
			llmRequestInfo = parseOllamaRequest(req.URL.Path, bodyBytes)
		}
	}

	// 提取请求头
	headers := make(map[string]string)
	for name, values := range req.Header {
		headers[name] = strings.Join(values, ", ")
	}

	// 记录请求
	reqLog := RequestLog{
		ID:         reqID,
		Timestamp:  time.Now().UTC().Format(time.RFC3339),
		Method:     req.Method,
		Path:       req.URL.Path,
		RemoteIP:   req.RemoteAddr,
		Headers:    headers,
		Body:       bodyStr,
		LLMRequest: llmRequestInfo,
	}

	// 发送到Elasticsearch
	jsonData, err := json.Marshal(reqLog)
	if err != nil {
		log.Printf("无法序列化请求日志: %v", err)
		return reqID
	}

	_, err = l.esClient.Index(
		l.index,
		strings.NewReader(string(jsonData)),
		l.esClient.Index.WithContext(context.Background()),
		l.esClient.Index.WithDocumentID(reqID),
	)

	if err != nil {
		log.Printf("无法发送请求日志到Elasticsearch: %v", err)
	}

	return reqID
}

// parseOllamaRequest 解析Ollama API请求
func parseOllamaRequest(path string, bodyBytes []byte) *LLMRequestInfo {
	if !strings.Contains(path, "/api/") {
		return nil
	}

	var requestData map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &requestData); err != nil {
		log.Printf("解析请求体失败: %v", err)
		return nil
	}

	info := &LLMRequestInfo{}

	// 提取通用字段
	if model, ok := requestData["model"].(string); ok {
		info.Model = model
	}

	if temp, ok := requestData["temperature"].(float64); ok {
		info.Temperature = temp
	}

	if stream, ok := requestData["stream"].(bool); ok {
		info.Stream = stream
	}

	// 根据API路径分别处理
	switch {
	case strings.Contains(path, "/api/generate"):
		// 处理generate请求
		if prompt, ok := requestData["prompt"].(string); ok {
			info.Prompt = prompt
		}
		if system, ok := requestData["system"].(string); ok {
			info.System = system
		}

	case strings.Contains(path, "/api/chat"):
		// 处理chat请求
		if system, ok := requestData["system"].(string); ok {
			info.System = system
		}

		// 提取消息
		if messagesRaw, ok := requestData["messages"].([]interface{}); ok {
			for _, msgRaw := range messagesRaw {
				if msg, ok := msgRaw.(map[string]interface{}); ok {
					role, _ := msg["role"].(string)
					content, _ := msg["content"].(string)
					info.Messages = append(info.Messages, ChatMessage{
						Role:    role,
						Content: content,
					})
				}
			}
		}
	}

	return info
}

// LogResponse 记录响应
func (l *ELKLogger) LogResponse(reqID string, resp *http.Response, body []byte) {
	if !l.enabled || reqID == "" {
		return
	}

	// 提取响应头
	headers := make(map[string]string)
	for name, values := range resp.Header {
		headers[name] = strings.Join(values, ", ")
	}

	// 解析Ollama响应
	var llmResponseInfo *LLMResponseInfo
	if resp.Header.Get("Content-Type") == "application/json" && len(body) > 0 {
		llmResponseInfo = parseOllamaResponse(resp.Request.URL.Path, body)
	}

	// 记录响应
	respLog := ResponseLog{
		RequestID:   reqID,
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		Status:      resp.StatusCode,
		Headers:     headers,
		Body:        string(body),
		LLMResponse: llmResponseInfo,
	}

	// 发送到Elasticsearch
	jsonData, err := json.Marshal(respLog)
	if err != nil {
		log.Printf("无法序列化响应日志: %v", err)
		return
	}

	_, err = l.esClient.Index(
		l.index,
		strings.NewReader(string(jsonData)),
		l.esClient.Index.WithContext(context.Background()),
	)

	if err != nil {
		log.Printf("无法发送响应日志到Elasticsearch: %v", err)
	}
}

// parseOllamaResponse 解析Ollama API响应
func parseOllamaResponse(path string, bodyBytes []byte) *LLMResponseInfo {
	if !strings.Contains(path, "/api/") {
		return nil
	}

	var responseData map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &responseData); err != nil {
		log.Printf("解析响应体失败: %v", err)
		return nil
	}

	info := &LLMResponseInfo{}

	// 提取通用字段
	if model, ok := responseData["model"].(string); ok {
		info.Model = model
	}

	if finished, ok := responseData["done"].(bool); ok {
		info.Finished = finished
	}

	if duration, ok := responseData["total_duration"].(float64); ok {
		info.TotalDuration = int64(duration)
	}

	// 根据API路径分别处理
	switch {
	case strings.Contains(path, "/api/generate"):
		// 处理generate响应
		if response, ok := responseData["response"].(string); ok {
			info.GeneratedText = response
		}

	case strings.Contains(path, "/api/chat"):
		// 处理chat响应
		if message, ok := responseData["message"].(map[string]interface{}); ok {
			if content, ok := message["content"].(string); ok {
				info.Response = content
			}
		}
	}

	return info
}

// Close 关闭日志记录器
func (l *ELKLogger) Close() error {
	// Elasticsearch客户端没有明确的关闭方法
	return nil
}
