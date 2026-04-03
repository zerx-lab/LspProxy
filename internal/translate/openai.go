// Package translate 实现翻译引擎接口及三级缓存机制。
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

// ─────────────────────────────────────────────
// 提供商枚举与检测逻辑
// ─────────────────────────────────────────────

// provider 枚举，用于区分不同 API 提供商，以注入提供商特有的请求参数。
type provider int

const (
	providerUnknown  provider = iota // 未知提供商（兼容模式，不注入额外参数）
	providerQwen                     // 阿里云 DashScope（Qwen 系列）
	providerDeepSeek                 // DeepSeek
	providerDoubao                   // 字节跳动豆包（Volcengine）
	providerOpenAI                   // OpenAI 官方
)

// detectProvider 根据 API BaseURL 推断提供商类型。
func detectProvider(baseURL string) provider {
	u := strings.ToLower(baseURL)
	switch {
	case strings.Contains(u, "dashscope"):
		return providerQwen
	case strings.Contains(u, "deepseek"):
		return providerDeepSeek
	case strings.Contains(u, "volces.com") || strings.Contains(u, "volcengine"):
		return providerDoubao
	case strings.Contains(u, "openai.com"):
		return providerOpenAI
	default:
		return providerUnknown
	}
}

// isThinkingByDefault 判断指定提供商的某模型是否默认开启思考模式。
func isThinkingByDefault(prov provider, model string) bool {
	m := strings.ToLower(model)
	switch prov {
	case providerQwen:
		// qwen3-* 和 qwen3.* （如 qwen3.6-plus）默认开启思考
		return strings.HasPrefix(m, "qwen3")
	case providerDeepSeek:
		// deepseek-reasoner 和 deepseek-r1-* 系列默认开启思考
		return strings.Contains(m, "reasoner") || strings.Contains(m, "-r1")
	case providerDoubao:
		// doubao-*-thinking-* 系列默认开启思考
		return strings.Contains(m, "thinking")
	case providerOpenAI:
		// o1-*、o3-*、o4-* 系列默认开启思考
		return strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4")
	}
	return false
}

// buildThinkingParams 根据提供商、模型名称和思考模式配置，
// 返回需要注入请求体的额外字段。若无需注入则返回 nil。
//
// thinkingMode 取值：
//   - "auto"     自动：对默认开启思考的模型自动关闭（翻译任务不需要推理）
//   - "enabled"  强制开启（不注入关闭参数，让模型使用默认推理行为）
//   - "disabled" 强制关闭
func buildThinkingParams(prov provider, model, thinkingMode string) map[string]any {
	var disable bool
	switch thinkingMode {
	case "disabled":
		disable = true
	case "enabled":
		disable = false
	default: // "auto" 或空字符串
		disable = isThinkingByDefault(prov, model)
	}

	if !disable {
		return nil
	}

	// 各提供商关闭思考的参数格式不同
	switch prov {
	case providerQwen:
		// 阿里云 DashScope：enable_thinking=false
		return map[string]any{"enable_thinking": false}
	case providerDeepSeek:
		// DeepSeek：thinking_budget_tokens=0 关闭推理
		return map[string]any{"thinking_budget_tokens": 0}
	case providerDoubao:
		// 豆包：thinking.type="disabled"
		return map[string]any{"thinking": map[string]any{"type": "disabled"}}
	case providerOpenAI:
		// OpenAI o 系列无法完全关闭思考，降为最低推理强度
		return map[string]any{"reasoning_effort": "low"}
	}
	return nil
}

// ─────────────────────────────────────────────
// OpenAIEngine 结构体与构造函数
// ─────────────────────────────────────────────

// OpenAIEngine 使用 OpenAI 兼容 API 实现翻译引擎。
// 支持 DeepSeek、Qwen、豆包、Ollama 等兼容 OpenAI Chat Completions 接口的服务。
type OpenAIEngine struct {
	// BaseURL 是 API 基础地址，例如 https://api.openai.com/v1
	BaseURL string
	// APIKey 是认证密钥，Ollama 等本地服务可传空字符串
	APIKey string
	// Model 是模型名称，例如 gpt-4o-mini、deepseek-chat、qwen-plus
	Model string
	// ThinkingMode 思考模式："auto" | "enabled" | "disabled"
	ThinkingMode string
	// loader 提示词模板加载器，支持热重载
	loader *PromptLoader
	// prov 缓存检测到的提供商类型，避免每次翻译重复计算
	prov   provider
	client *http.Client
}

// NewOpenAIEngine 创建一个新的 OpenAIEngine 实例。
//   - baseURL:      API 基础地址（不含末尾斜杠），例如 "https://api.openai.com/v1"
//   - apiKey:       认证密钥，本地服务可传空字符串
//   - model:        模型名称
//   - thinkingMode: 思考模式，"auto" | "enabled" | "disabled"；空字符串等同于 "auto"
//   - loader:       提示词模板加载器，传 nil 时回退到内置 systemPrompt 函数
func NewOpenAIEngine(baseURL, apiKey, model, thinkingMode string, loader *PromptLoader) *OpenAIEngine {
	if thinkingMode == "" {
		thinkingMode = "auto"
	}
	return &OpenAIEngine{
		BaseURL:      strings.TrimRight(baseURL, "/"),
		APIKey:       apiKey,
		Model:        model,
		ThinkingMode: thinkingMode,
		loader:       loader,
		prov:         detectProvider(baseURL),
		client: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// Close 关闭提示词文件监听器，释放资源。
func (o *OpenAIEngine) Close() error {
	if o.loader != nil {
		return o.loader.Close()
	}
	return nil
}

// Name 返回引擎名称。
func (o *OpenAIEngine) Name() string {
	return "OpenAI(" + o.Model + ")"
}

// ─────────────────────────────────────────────
// 内部请求/响应结构体
// ─────────────────────────────────────────────

// openAIMessage 表示 Chat Completions API 中的单条消息。
type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// openAIResponse 是 Chat Completions API 的响应体（仅解析所需字段）。
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

// ─────────────────────────────────────────────
// 提示词与翻译方法
// ─────────────────────────────────────────────

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
// 针对 Keep-Alive 连接被服务端回收导致的 EOF / connection reset 错误，
// 最多自动重试一次（翻译请求是幂等的，重试安全）。
func (o *OpenAIEngine) Translate(ctx context.Context, text, targetLang string) (string, error) {
	// 获取系统提示词：优先使用 loader 渲染，loader 为 nil 时回退到内置函数
	var sysPrompt string
	if o.loader != nil {
		sysPrompt = o.loader.Render(targetLang)
	} else {
		sysPrompt = systemPrompt(targetLang)
	}

	// 使用 map 动态构建请求体，方便注入提供商特有参数
	reqMap := map[string]any{
		"model": o.Model,
		"messages": []openAIMessage{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: text},
		},
		// 翻译任务使用较低温度，保证输出稳定
		"temperature": 0.2,
	}

	// 注入思考模式控制参数（各提供商格式不同）
	for k, v := range buildThinkingParams(o.prov, o.Model, o.ThinkingMode) {
		reqMap[k] = v
	}

	bodyBytes, err := json.Marshal(reqMap)
	if err != nil {
		return "", fmt.Errorf("openai translate: 序列化请求体失败: %w", err)
	}

	// doOnce 执行一次 HTTP 请求并解析响应。
	// bodyBytes 已序列化完毕，每次重试直接复用，无副作用。
	doOnce := func() (string, error) {
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

	result, err := doOnce()
	if err != nil && isRetryableNetErr(err) && ctx.Err() == nil {
		// Keep-Alive 连接被服务端回收（EOF / connection reset）：
		// 标准库对 POST 不自动重试，此处补充一次幂等重试。
		result, err = doOnce()
	}
	return result, err
}

// isRetryableNetErr 判断错误是否属于可安全重试的网络层瞬时故障。
// 仅匹配 EOF 和连接重置，不重试超时或业务错误，避免放大 API 调用量。
func isRetryableNetErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "broken pipe")
}
