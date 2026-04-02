package translate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GoogleEngine 使用 Google 免费翻译 API 实现翻译引擎。
// 无需 API 密钥，直接调用公开端点。
type GoogleEngine struct {
	client *http.Client
}

// NewGoogleEngine 创建一个新的 GoogleEngine 实例，使用默认超时的 HTTP 客户端。
func NewGoogleEngine() *GoogleEngine {
	return &GoogleEngine{
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// Translate 将 text 翻译为 targetLang 所指定的语言。
// 源语言设为 auto（自动检测），不需要 API 密钥。
//
// 优化点：
//  1. 空白文本直接原样返回，不发起网络请求
//  2. 文本较长时改用 POST 请求，避免 URL 长度限制
func (g *GoogleEngine) Translate(ctx context.Context, text, targetLang string) (string, error) {
	// 空白文本（纯空格/换行）无需翻译，直接返回原文
	if strings.TrimSpace(text) == "" {
		return text, nil
	}

	const apiBase = "https://translate.googleapis.com/translate_a/single"
	// GET 请求 URL 安全长度阈值（保守取 800 字节留给其他参数）
	const getThreshold = 800

	var (
		resp *http.Response
		err  error
	)

	if len(text) <= getThreshold {
		resp, err = g.doGET(ctx, apiBase, text, targetLang)
	} else {
		resp, err = g.doPOST(ctx, apiBase, text, targetLang)
	}
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("google translate: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// Google 免费 API 返回嵌套 JSON 数组，结构示例：
	// [
	//   [                          ← raw[0]：翻译结果数组
	//     ["翻译片段1", "原文片段1", ...],
	//     ["翻译片段2", "原文片段2", ...],
	//   ],
	//   null,
	//   "en",
	//   ...
	// ]
	var raw []any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return "", fmt.Errorf("google translate: 解析响应失败: %w", err)
	}

	translated, err := extractGoogleResult(raw)
	if err != nil {
		return "", fmt.Errorf("google translate: 提取翻译结果失败: %w", err)
	}

	return translated, nil
}

// Name 返回引擎名称
func (g *GoogleEngine) Name() string {
	return "Google"
}

// doGET 使用 GET 请求调用 Google 翻译 API（适合短文本）。
func (g *GoogleEngine) doGET(ctx context.Context, apiBase, text, targetLang string) (*http.Response, error) {
	reqURL := fmt.Sprintf(
		"%s?client=gtx&sl=auto&tl=%s&dt=t&q=%s",
		apiBase,
		url.QueryEscape(targetLang),
		url.QueryEscape(text),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("google translate: 构建 GET 请求失败: %w", err)
	}
	setGoogleHeaders(req)

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google translate: GET 请求失败: %w", err)
	}
	return resp, nil
}

// doPOST 使用 POST 请求调用 Google 翻译 API（适合长文本，避免 URL 超长）。
func (g *GoogleEngine) doPOST(ctx context.Context, apiBase, text, targetLang string) (*http.Response, error) {
	reqURL := fmt.Sprintf("%s?client=gtx&sl=auto&tl=%s&dt=t", apiBase, url.QueryEscape(targetLang))

	form := url.Values{}
	form.Set("q", text)
	body := strings.NewReader(form.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, body)
	if err != nil {
		return nil, fmt.Errorf("google translate: 构建 POST 请求失败: %w", err)
	}
	setGoogleHeaders(req)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := g.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("google translate: POST 请求失败: %w", err)
	}
	return resp, nil
}

// setGoogleHeaders 为请求设置必要的 HTTP 头，模拟正常浏览器请求。
func setGoogleHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
}

// extractGoogleResult 从 Google API 嵌套数组响应中提取翻译文本。
// 将所有翻译片段（每个子数组的第 0 个元素）拼接为最终结果。
//
// 健壮性改进：
//   - 允许部分片段为空字符串（如原文本身就是空行）
//   - 只在结果为 nil（无法解析任何片段）时才报错
func extractGoogleResult(raw []any) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("响应数组为空")
	}

	// raw[0] 是翻译结果数组
	outerArr, ok := raw[0].([]any)
	if !ok {
		return "", fmt.Errorf("响应格式错误：raw[0] 不是数组（实际类型 %T）", raw[0])
	}

	var (
		sb     strings.Builder
		gotAny bool // 是否至少成功解析了一个片段（哪怕是空字符串）
	)

	for _, item := range outerArr {
		inner, ok := item.([]any)
		if !ok || len(inner) == 0 {
			continue
		}
		// inner[0] 是翻译文本，可能是 string 或 nil
		switch v := inner[0].(type) {
		case string:
			sb.WriteString(v)
			gotAny = true
		case nil:
			// Google 对某些片段返回 null，跳过即可
			gotAny = true
		}
	}

	if !gotAny {
		return "", fmt.Errorf("未能从响应中提取到任何翻译片段")
	}

	return sb.String(), nil
}
