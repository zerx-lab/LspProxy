package translate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// OpenAIEngine 使用 OpenAI 兼容 API 实现翻译引擎。
// 支持 DeepSeek、Qwen、Ollama 等兼容 OpenAI Chat Completions 接口的服务。
type OpenAIEngine struct {
	// BaseURL 是 API 基础地址，例如 https://api.openai.com/v1
	// 或 DeepSeek 的 https://api.deepseek.com/v1
	BaseURL string
	// APIKey 是认证密钥，Ollama 等本地服务可传空字符串
	APIKey string
	// Model 是模型名称，例如 gpt-4o-mini、deepseek-chat、qwen-plus
	Model  string
	client *http.Client
}

// NewOpenAIEngine 创建一个新的 OpenAIEngine 实例。
//   - baseURL: API 基础地址（不含末尾斜杠），例如 "https://api.openai.com/v1"
//   - apiKey:  认证密钥，本地服务可传空字符串
//   - model:   模型名称
func NewOpenAIEngine(baseURL, apiKey, model string) *OpenAIEngine {
	return &OpenAIEngine{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		Model:   model,
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// Name 返回引擎名称
func (o *OpenAIEngine) Name() string {
	return "OpenAI(" + o.Model + ")"
}

// ---- 内部请求/响应结构体 ----

// openAIMessage 表示 Chat Completions API 中的单条消息
type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// openAIRequest 是 Chat Completions API 的请求体
type openAIRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Temperature float64         `json:"temperature"`
}

// openAIResponse 是 Chat Completions API 的响应体（仅解析所需字段）
type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// systemPrompt 根据目标语言生成系统提示词。
// 要求模型：翻译为目标语言、保留 Markdown 格式、保留占位符、只返回翻译结果不要解释。
func systemPrompt(targetLang string) string {
	return fmt.Sprintf(
		"You are a professional technical documentation translator. "+
			"Translate the user's input into %s. "+
			"Rules you MUST follow:\n"+
			"1. Preserve all Markdown formatting (headings, bold, italic, code blocks, inline code, lists, links, etc.) exactly as-is.\n"+
			"2. Placeholders in the format $CODE_N$ (where N is a number, e.g. $CODE_0$, $CODE_1$) represent code snippets. "+
			"Keep them EXACTLY as-is — do NOT translate, modify, move, or remove them.\n"+
			"3. Output ONLY the translated text. No explanations, no preamble, no commentary.",
		targetLang,
	)
}

// Translate 调用 OpenAI 兼容 API 将 text 翻译为 targetLang 所指定的语言。
func (o *OpenAIEngine) Translate(ctx context.Context, text, targetLang string) (string, error) {
	// 构造请求体
	reqBody := openAIRequest{
		Model: o.Model,
		Messages: []openAIMessage{
			{Role: "system", Content: systemPrompt(targetLang)},
			{Role: "user", Content: text},
		},
		// 翻译任务使用较低温度，保证输出稳定
		Temperature: 0.2,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("openai translate: 序列化请求体失败: %w", err)
	}

	// 构造 HTTP 请求
	endpoint := o.BaseURL + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("openai translate: 构建请求失败: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	// 仅在 APIKey 非空时设置 Authorization 头
	if o.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.APIKey)
	}

	// 发送请求
	resp, err := o.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("openai translate: 请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 解析响应
	var apiResp openAIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return "", fmt.Errorf("openai translate: 解析响应失败 (HTTP %d): %w", resp.StatusCode, err)
	}

	// 优先检查 API 级别的错误字段（部分服务在 200 中也会返回 error 对象）
	if apiResp.Error != nil {
		return "", fmt.Errorf("openai translate: API 错误 [%s]: %s", apiResp.Error.Type, apiResp.Error.Message)
	}

	// 检查 HTTP 状态码
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("openai translate: HTTP 状态码 %d", resp.StatusCode)
	}

	// 提取翻译结果
	if len(apiResp.Choices) == 0 {
		return "", fmt.Errorf("openai translate: 响应中没有 choices")
	}

	result := strings.TrimSpace(apiResp.Choices[0].Message.Content)
	if result == "" {
		return "", fmt.Errorf("openai translate: 模型返回了空内容")
	}

	return result, nil
}
