package markdown

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// SegmentKind 表示文本片段的类型
type SegmentKind int

const (
	KindText SegmentKind = iota // 普通文本，需要翻译
	KindCode                    // 代码块，保留原文
)

// Segment 表示一段文本片段
type Segment struct {
	Kind    SegmentKind
	Content string
}

// Split 将 Markdown 文本分割为文本和代码片段。
//
// 处理以下代码块类型：
//  1. 围栏代码块（``` 或 ~~~），允许出现在行首或行中（兼容 LSP 文档中的非标准用法）
//  2. 行内代码（`...` 或 “...“，不超过 2 个反引号）
//
// 注意：对于 LSP hover/completion 文档，``` 常常出现在行中而非行首，
// 例如 "示例：```rust code```"。本函数放宽了 CommonMark 对行首的要求，
// 以正确识别此类场景并保护代码内容不被翻译。
func Split(text string) []Segment {
	var segments []Segment
	var current strings.Builder

	runes := []rune(text)
	length := len(runes)
	i := 0

	// flushText 将已累积的普通文本写入结果
	flushText := func() {
		if current.Len() > 0 {
			segments = append(segments, Segment{Kind: KindText, Content: current.String()})
			current.Reset()
		}
	}

	for i < length {
		// -------------------------------------------------------
		// 1. 检测围栏代码块：``` 或 ~~~（3 个及以上）
		//    不强制要求行首，兼容 LSP 文档中行中出现 ``` 的情况。
		// -------------------------------------------------------
		fence, fenceLen := detectFence(runes, i)
		if fenceLen > 0 {
			flushText()

			var code strings.Builder
			// 写入开启围栏标记
			code.WriteString(string(runes[i : i+fenceLen]))
			i += fenceLen

			// 写入开启围栏行剩余内容（语言标识符等），直到换行或文本末尾
			for i < length && runes[i] != '\n' {
				code.WriteRune(runes[i])
				i++
			}

			// 如果后面还有内容（换行后），进入多行扫描模式
			if i < length && runes[i] == '\n' {
				code.WriteRune('\n')
				i++

				// 扫描直到遇到匹配的关闭围栏（行首，与开启围栏相同字符，长度 >= fenceLen）
				for i < length {
					// 只在行首检测关闭围栏（符合 CommonMark 规范）
					closeFence, closeFenceLen := detectFence(runes, i)
					if closeFence == fence && closeFenceLen >= fenceLen {
						code.WriteString(string(runes[i : i+closeFenceLen]))
						i += closeFenceLen
						// 吃掉关闭围栏后的空格/换行
						for i < length && runes[i] != '\n' {
							code.WriteRune(runes[i])
							i++
						}
						if i < length && runes[i] == '\n' {
							code.WriteRune('\n')
							i++
						}
						break
					}
					// 普通行：整行写入代码块
					for i < length && runes[i] != '\n' {
						code.WriteRune(runes[i])
						i++
					}
					if i < length && runes[i] == '\n' {
						code.WriteRune('\n')
						i++
					}
				}
			} else {
				// 单行围栏（开启和关闭在同一行，或开启后直接到文本末尾）
				// 例如：```rust tracing_subscriber::fmt().init();```
				// 继续扫描当前行，查找相同的关闭围栏
				for i < length && runes[i] != '\n' {
					// 尝试匹配关闭围栏
					closeFence, closeFenceLen := detectFence(runes, i)
					if closeFence == fence && closeFenceLen >= fenceLen {
						code.WriteString(string(runes[i : i+closeFenceLen]))
						i += closeFenceLen
						break
					}
					code.WriteRune(runes[i])
					i++
				}
				// 若行尾还有换行，写入并前进
				if i < length && runes[i] == '\n' {
					code.WriteRune('\n')
					i++
				}
			}

			segments = append(segments, Segment{Kind: KindCode, Content: code.String()})
			continue
		}

		// -------------------------------------------------------
		// 2. 检测行内代码：1~2 个反引号包裹的内容（不跨行）
		//    注意：3 个及以上反引号已在上方作为围栏代码块处理。
		// -------------------------------------------------------
		if runes[i] == '`' {
			tickCount := 0
			j := i
			for j < length && runes[j] == '`' {
				tickCount++
				j++
			}
			// tickCount >= 3 已被上方围栏检测捕获（detectFence 返回非零），
			// 能走到这里说明 tickCount < 3（detectFence 要求 >= 3）
			closeIdx := findInlineClose(runes, j, tickCount)
			if closeIdx >= 0 {
				flushText()
				endIdx := closeIdx + tickCount
				segments = append(segments, Segment{
					Kind:    KindCode,
					Content: string(runes[i:endIdx]),
				})
				i = endIdx
				continue
			}
			// 未找到匹配关闭标记，作普通字符处理
		}

		// -------------------------------------------------------
		// 3. 普通字符
		// -------------------------------------------------------
		current.WriteRune(runes[i])
		i++
	}

	flushText()
	return segments
}

// detectFence 检测从位置 pos 开始是否是围栏代码块的开启标记。
// 返回围栏字符（'`' 或 '~'）以及连续字符的数量（至少 3 个才算围栏）。
// 如果不是围栏，返回 0 和 0。
func detectFence(runes []rune, pos int) (rune, int) {
	if pos >= len(runes) {
		return 0, 0
	}
	ch := runes[pos]
	if ch != '`' && ch != '~' {
		return 0, 0
	}
	count := 0
	for pos+count < len(runes) && runes[pos+count] == ch {
		count++
	}
	if count < 3 {
		return 0, 0
	}
	return ch, count
}

// findInlineClose 从 start 位置开始，在 runes 中寻找连续 tickCount 个反引号的位置。
// 行内代码不能跨越换行符（根据 CommonMark 规范）。
// 返回关闭标记第一个反引号的索引；如果找不到则返回 -1。
func findInlineClose(runes []rune, start, tickCount int) int {
	length := len(runes)
	for i := start; i < length; {
		if runes[i] == '\n' {
			// 行内代码不允许跨行
			return -1
		}
		if runes[i] == '`' {
			// 统计此处连续反引号数量
			count := 0
			j := i
			for j < length && runes[j] == '`' {
				count++
				j++
			}
			if count == tickCount {
				return i
			}
			// 数量不匹配，跳过这些反引号
			i = j
			continue
		}
		i++
	}
	return -1
}

// Join 将片段重新拼接为完整字符串
func Join(segments []Segment) string {
	var sb strings.Builder
	for _, seg := range segments {
		sb.WriteString(seg.Content)
	}
	return sb.String()
}

// ─────────────────────────────────────────────────────────────────────────────
// 占位符替换法（Placeholder Substitution）
//
// 与 Split/Join 的"分段翻译"不同，占位符法将代码块替换为短标记后，
// 把整段文本作为一个整体发送给翻译引擎，翻译完成后再还原代码块。
//
// 优点：
//   - 翻译引擎看到完整语句，上下文完整，翻译质量更高
//   - 代码块和文字混排、行内代码等复杂情况不再导致错位
//   - 即使 markdown 结构不规范也能正常处理
//
// 占位符格式：$CODE_N$（看起来像变量插值，翻译引擎通常原样保留）
// 还原时使用正则匹配以应对翻译引擎可能加入的空格。
// ─────────────────────────────────────────────────────────────────────────────

// placeholderFmt 是写入占位符时使用的格式字符串
const placeholderFmt = "$CODE_%d$"

// placeholderRe 用于还原阶段匹配占位符，宽松处理翻译引擎可能加入的空格
var placeholderRe = regexp.MustCompile(`\$\s*CODE_\s*(\d+)\s*\$`)

// PlaceholderRe 返回占位符匹配正则表达式，供外部包判断文本是否包含占位符。
func PlaceholderRe() *regexp.Regexp {
	return placeholderRe
}

// ─────────────────────────────────────────────────────────────────────────────
// 编程术语保护（Technical Term Protection）
//
// LSP 文档中存在大量编程领域专用词汇（如 Panic、throws、deprecated 等），
// 这些词语在普通语言语境下有其他含义，但在编程文档中是固定术语，不应被翻译。
//
// 保护策略：在占位符替换阶段，把已知技术术语也替换为 $CODE_N$ 占位符，
// 使翻译引擎完全跳过它们，与代码块享有同等保护级别。
//
// 匹配规则：
//   - 仅匹配单词边界（\b），避免替换正常单词中出现的子串
//   - 大小写不敏感（panic / Panic / PANIC 均匹配）
//   - 词表集中在 techTermRe 中维护，便于增删
// ─────────────────────────────────────────────────────────────────────────────

// techTermRe 匹配编程领域不应被翻译的技术术语。
// 使用单词边界 \b 确保精确匹配，使用 (?i) 进行大小写不敏感匹配。
//
// 收录原则：
//   - 在自然语言中有截然不同的含义、翻译后会产生语义错误的词语
//   - 不包含在反引号代码块中的词语（已由 Split() 保护）
//   - 翻译后语义正确的词语（如 deprecated→"弃用"）不在此列
var techTermRe = regexp.MustCompile(
	`(?i)\b(` +
		// 异常 / 错误处理：这些词在编程上下文有固定含义，直译会产生语义错误
		// panic → "恐慌"（错误），throws → "投掷"（错误），raises → "提升"（错误）
		`panic|throws?|raises?` +
		`)\b`,
)

// protectTechTerms 将文本中的已知技术术语替换为 $CODE_N$ 占位符。
//
// 仅在占位符掩码文本（masked）上调用，此时代码块已被替换为 $CODE_N$，
// 因此不会对代码块内的内容产生重复替换。
func protectTechTerms(masked string, codes []string) (string, []string) {
	result := techTermRe.ReplaceAllStringFunc(masked, func(term string) string {
		idx := len(codes)
		codes = append(codes, term)
		return fmt.Sprintf(placeholderFmt, idx)
	})
	return result, codes
}

// Protect 将文本中的所有代码块（围栏代码块和行内代码）以及编程技术术语替换为编号占位符。
//
// 处理顺序：
//  1. 按 Markdown 结构提取代码块（围栏块、行内代码），替换为 $CODE_N$
//  2. 在掩码文本上扫描已知技术术语，同样替换为 $CODE_N$（序号延续）
//
// 返回：
//   - masked: 替换后的文本，可直接送入翻译引擎
//   - codes:  被提取的原文（代码块 + 技术术语），按编号顺序存储，用于 [Restore] 还原
//
// 若文本不含任何代码块或技术术语，codes 为空切片，masked 等于原文。
func Protect(text string) (masked string, codes []string) {
	// ── 第一步：保护 Markdown 代码块 ──
	segments := Split(text)
	var sb strings.Builder
	sb.Grow(len(text))
	for _, seg := range segments {
		if seg.Kind == KindCode {
			sb.WriteString(fmt.Sprintf(placeholderFmt, len(codes)))
			codes = append(codes, seg.Content)
		} else {
			sb.WriteString(seg.Content)
		}
	}
	masked = sb.String()

	// ── 第二步：保护编程技术术语 ──
	masked, codes = protectTechTerms(masked, codes)

	return masked, codes
}

// Restore 将翻译后文本中的占位符还原为原始代码块内容。
//
// 使用正则匹配（而非字符串精确替换）以应对翻译引擎在占位符内添加空格的情况，
// 例如 `$ CODE_0 $` 也能正确还原为对应的代码块。
//
// 若 codes 为空，直接返回 masked 原文。
func Restore(masked string, codes []string) string {
	if len(codes) == 0 {
		return masked
	}
	return placeholderRe.ReplaceAllStringFunc(masked, func(match string) string {
		sub := placeholderRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		idx, err := strconv.Atoi(sub[1])
		if err != nil || idx < 0 || idx >= len(codes) {
			return match // 索引越界时保留占位符，不丢失信息
		}
		return codes[idx]
	})
}
