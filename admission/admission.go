package admission

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/mfzzf/LLM_Based_HoneyPot/config"
)

// Checker 定义了准入控制检查器的接口
type Checker interface {
	// CheckContent 检查内容是否合法，如果不合法则返回拒绝原因
	CheckContent(ctx context.Context, content string) (allowed bool, reason string, err error)

	// CheckPrompt 检查提示词是否合法
	CheckPrompt(ctx context.Context, prompt string) (allowed bool, reason string, err error)

	// CheckChatMessages 检查聊天消息是否合法
	CheckChatMessages(ctx context.Context, messages []Message) (allowed bool, reason string, err error)
}

// Message 表示聊天消息
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// OllamaChecker 使用Ollama模型进行准入控制检查
type OllamaChecker struct {
	config config.AdmissionConfig
	client *http.Client
}

// NewOllamaChecker 创建一个新的Ollama准入控制检查器
func NewOllamaChecker(cfg config.AdmissionConfig) Checker {
	// 增加超时时间，默认至少30秒
	timeout := time.Duration(cfg.Timeout) * time.Second
	if timeout < 30*time.Second {
		timeout = 30 * time.Second
		log.Printf("[准入] 警告: 配置的超时时间过短，已自动调整为30秒")
	}

	client := &http.Client{
		Timeout: timeout,
	}

	log.Printf("[准入] 初始化Ollama准入控制检查器: URL=%s, 模型=%s, 超时=%v",
		cfg.OllamaURL, cfg.ModelName, timeout)

	return &OllamaChecker{
		config: cfg,
		client: client,
	}
}

// CheckContent 检查内容是否合法
func (oc *OllamaChecker) CheckContent(ctx context.Context, content string) (bool, string, error) {
	log.Printf("[准入] 开始检查内容, 内容长度=%d", len(content))

	if !oc.config.Enabled {
		log.Printf("[准入] 准入控制已禁用")
		return true, "", nil
	}

	// 记录使用的模型名称
	log.Printf("[准入] 使用模型 %s 进行准入控制检查, URL=%s",
		oc.config.ModelName, oc.config.OllamaURL)

	// 使用系统提示词指导模型进行内容审核
	systemPrompt := `你是一个安全审核员。你的任务是检查content是否包含不允许的内容。你只需要输出 ALLOW和 DISALLOW`

	// 聊天请求
	requestBody := map[string]interface{}{
		"model": oc.config.ModelName,
		"messages": []map[string]string{
			{
				"role":    "system",
				"content": systemPrompt,
			},
			{
				"role":    "user",
				"content": content,
			},
		},
		"stream": false,
	}

	// 执行请求前记录
	log.Printf("[准入] 发送审核请求到模型")

	// 执行请求
	var result string
	var err error

	// 重试机制
	for retry := 0; retry <= oc.config.MaxRetries; retry++ {
		result, err = oc.doRequest(ctx, requestBody)
		if err == nil {
			break
		}

		log.Printf("[准入] 请求失败(重试 %d/%d): %v", retry, oc.config.MaxRetries, err)
		if retry < oc.config.MaxRetries {
			// 指数退避策略
			backoffTime := time.Duration(500*(1<<retry)) * time.Millisecond
			if backoffTime > 5*time.Second {
				backoffTime = 5 * time.Second
			}
			log.Printf("[准入] 等待 %v 后重试", backoffTime)
			time.Sleep(backoffTime)
		}
	}

	if err != nil {
		log.Printf("[准入] 控制失败，允许请求通过: %v", err)
		// 出错时默认允许，避免阻止正常服务
		return true, "", err
	}

	// 记录响应
	log.Printf("[准入] 收到模型响应: %s", result)

	// 分析结果
	if strings.HasPrefix(result, "ALLOW") {
		return true, "", nil
	} else if strings.HasPrefix(result, "DISALLOW") {
		reason := strings.TrimPrefix(result, "DISALLOW:")
		reason = strings.TrimSpace(reason)
		if reason == "" {
			reason = "内容不合规"
		}
		return false, reason, nil
	}

	// 如果响应格式不符合预期，默认允许并记录
	log.Printf("准入控制结果格式异常: %s", result)
	return true, "", nil
}

// CheckPrompt 检查提示词是否合法
func (oc *OllamaChecker) CheckPrompt(ctx context.Context, prompt string) (bool, string, error) {
	return oc.CheckContent(ctx, prompt)
}

// CheckChatMessages 检查聊天消息是否合法
func (oc *OllamaChecker) CheckChatMessages(ctx context.Context, messages []Message) (bool, string, error) {
	// 组合所有用户消息进行检查
	var userContents []string
	for _, msg := range messages {
		if msg.Role == "user" {
			userContents = append(userContents, msg.Content)
		}
	}

	// 如果没有用户消息，则默认允许
	if len(userContents) == 0 {
		return true, "", nil
	}

	// 检查最后一条用户消息
	return oc.CheckContent(ctx, userContents[len(userContents)-1])
}

// doRequest 执行Ollama API请求
func (oc *OllamaChecker) doRequest(ctx context.Context, requestBody map[string]interface{}) (string, error) {
	// 序列化请求体
	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("序列化请求失败: %w", err)
	}

	// 创建HTTP请求
	apiURL := fmt.Sprintf("%s/api/chat", oc.config.OllamaURL)
	log.Printf("[准入] 发送请求到: %s, 数据: %s", apiURL, string(jsonData))

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("创建HTTP请求失败: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// 请求开始时间
	startTime := time.Now()

	// 执行请求
	resp, err := oc.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("执行HTTP请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 计算请求耗时
	duration := time.Since(startTime)
	log.Printf("[准入] HTTP请求耗时: %v, 状态码: %d", duration, resp.StatusCode)

	// 读取响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API返回错误状态码: %d, 响应: %s", resp.StatusCode, string(body))
	}

	// 解析响应
	var response struct {
		Model     string `json:"model"`
		CreatedAt string `json:"created_at"`
		Message   struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
		Done          bool  `json:"done"`
		TotalDuration int64 `json:"total_duration"`
	}

	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("解析响应失败: %w, 响应内容: %s", err, string(body))
	}

	return response.Message.Content, nil
}

// CreateDeniedResponse 创建拒绝请求的响应
func CreateDeniedResponse(reason string, requestPath string) []byte {
	// 构造与原始API格式一致的响应
	responseMessage := fmt.Sprintf("很抱歉，我无法处理您的请求。原因：%s", reason)

	// 使用有序的结构体确保JSON字段顺序与原始API一致
	type ResponseMessage struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}

	type Response struct {
		Model              string          `json:"model"`
		CreatedAt          string          `json:"created_at"`
		Message            ResponseMessage `json:"message"`
		DoneReason         string          `json:"done_reason"`
		Done               bool            `json:"done"`
		TotalDuration      int64           `json:"total_duration"`
		LoadDuration       int64           `json:"load_duration"`
		PromptEvalCount    int             `json:"prompt_eval_count"`
		PromptEvalDuration int64           `json:"prompt_eval_duration"`
		EvalCount          int             `json:"eval_count"`
		EvalDuration       int64           `json:"eval_duration"`
	}

	response := Response{
		Model:     "phi3:3.8b", // 使用固定的模型名称，或从配置中获取
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Message: ResponseMessage{
			Role:    "assistant",
			Content: responseMessage,
		},
		DoneReason:         "stop",
		Done:               true,
		TotalDuration:      8000000000, // 模拟真实的时间数值
		LoadDuration:       15000000,
		PromptEvalCount:    15,
		PromptEvalDuration: 9000000,
		EvalCount:          400,
		EvalDuration:       7900000000,
	}

	jsonResponse, _ := json.Marshal(response)
	return jsonResponse
}
