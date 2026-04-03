package translate

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// ─────────────────────────────────────────────────────────────────────────────
// 诊断消息模板化翻译（Template-aware Translation）
//
// LSP 诊断消息中常包含动态标识符（变量名、类型名、文件路径等），
// 导致"语义相同但标识符不同"的消息无法共享缓存。
//
// 例如：
//
//	"variable 'foo' declared but not used"
//	"variable 'bar' declared but not used"
//
// 这两条消息语义完全相同，仅标识符不同。模板化翻译的做法是：
//  1. 提取动态标识符，替换为占位符 $ID_N$
//  2. 翻译模板文本（模板相同的消息共享缓存）
//  3. 将占位符还原为原始标识符
//
// 匹配规则（覆盖常见 LSP 诊断格式）：
//   - 反引号包裹：`identifier`
//   - 单引号包裹：'identifier'
//   - 双引号包裹（短内容）："identifier"
// ─────────────────────────────────────────────────────────────────────────────

// idPlaceholderFmt 是标识符占位符的格式字符串
const idPlaceholderFmt = "$ID_%d$"

// idPlaceholderRe 用于还原阶段匹配 $ID_N$ 占位符
var idPlaceholderRe = regexp.MustCompile(`\$\s*ID_\s*(\d+)\s*\$`)

// identifierRe 匹配诊断消息中被引号/反引号包裹的标识符。
// 匹配组：
//
//	组 0：完整匹配（含引号），如 `foo` 或 'bar' 或 "baz"
//	组 1：反引号内容
//	组 2：单引号内容
//	组 3：双引号内容（限制长度 ≤ 80 字符，避免匹配长字符串）
var identifierRe = regexp.MustCompile(
	"`([^`\n]+)`" +
		`|` +
		`'([^'\n]+)'` +
		`|` +
		`"([^"\n]{1,80})"`,
)

// TemplateResult 保存模板化的结果
type TemplateResult struct {
	Template    string   // 模板文本（标识符已替换为 $ID_N$）
	Identifiers []string // 提取的标识符列表，按编号顺序
	IsTemplated bool     // 是否进行了模板化（false 表示原文无标识符）
}

// Templatize 将诊断消息中的动态标识符替换为编号占位符。
//
// 返回 [TemplateResult]，其中 Template 字段可用于翻译（模板级缓存），
// Identifiers 用于翻译后的还原。
//
// 若文本不包含任何可识别的标识符，返回 IsTemplated=false，调用方应使用原始文本翻译。
func Templatize(text string) TemplateResult {
	var identifiers []string

	template := identifierRe.ReplaceAllStringFunc(text, func(match string) string {
		idx := len(identifiers)
		identifiers = append(identifiers, match)
		return fmt.Sprintf(idPlaceholderFmt, idx)
	})

	if len(identifiers) == 0 {
		return TemplateResult{
			Template:    text,
			IsTemplated: false,
		}
	}

	return TemplateResult{
		Template:    template,
		Identifiers: identifiers,
		IsTemplated: true,
	}
}

// RestoreIdentifiers 将翻译后文本中的 $ID_N$ 占位符还原为原始标识符。
//
// 使用正则匹配以应对翻译引擎在占位符内添加空格的情况。
func RestoreIdentifiers(translated string, identifiers []string) string {
	if len(identifiers) == 0 {
		return translated
	}
	return idPlaceholderRe.ReplaceAllStringFunc(translated, func(match string) string {
		sub := idPlaceholderRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		idx := 0
		for _, ch := range sub[1] {
			idx = idx*10 + int(ch-'0')
		}
		if idx < 0 || idx >= len(identifiers) {
			return match // 索引越界时保留占位符
		}
		return identifiers[idx]
	})
}

// TemplateEngine 是带模板化翻译能力的引擎包装器。
//
// 对于诊断消息等短文本，先尝试模板化提取标识符，翻译模板后还原。
// 模板化后的文本更容易命中缓存（"variable '$ID_0$' is unused" 只需翻译一次）。
//
// 注意：此引擎仅用于诊断消息等短文本，hover 文档仍走段落级翻译。
type TemplateEngine struct {
	base Engine
}

// NewTemplateEngine 创建模板化翻译引擎包装器。
func NewTemplateEngine(base Engine) *TemplateEngine {
	return &TemplateEngine{base: base}
}

// TranslateWithTemplate 使用模板化策略翻译文本。
//
// 流程：
//  1. 提取标识符，生成模板
//  2. 用模板作为 key 调用底层引擎翻译（模板级缓存命中率更高）
//  3. 将翻译结果中的占位符还原为原始标识符
//
// 若文本不含标识符，直接透传给底层引擎。
func (t *TemplateEngine) TranslateWithTemplate(ctx context.Context, text, targetLang string) (string, error) {
	tmpl := Templatize(text)

	if !tmpl.IsTemplated {
		// 无标识符，直接翻译原文
		return t.base.Translate(ctx, text, targetLang)
	}

	// 翻译模板
	translated, err := t.base.Translate(ctx, tmpl.Template, targetLang)
	if err != nil {
		return "", err
	}

	// 还原标识符
	result := RestoreIdentifiers(translated, tmpl.Identifiers)

	// 边界保护：若翻译引擎删除了所有占位符，可能导致还原后标识符丢失
	// 此时退回原文翻译（不使用模板化）
	if strings.Count(result, "$ID_") > 0 {
		// 还有未还原的占位符，说明翻译引擎产生了新的 $ID_ 文本，退回直接翻译
		return t.base.Translate(ctx, text, targetLang)
	}

	return result, nil
}

// Translate 实现 [Engine] 接口，直接调用底层引擎（不使用模板化）。
// 模板化翻译应通过 [TranslateWithTemplate] 显式调用。
func (t *TemplateEngine) Translate(ctx context.Context, text, targetLang string) (string, error) {
	return t.base.Translate(ctx, text, targetLang)
}

// Name 返回引擎名称
func (t *TemplateEngine) Name() string {
	return t.base.Name() + "(template)"
}
