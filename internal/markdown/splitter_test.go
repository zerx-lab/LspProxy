package markdown

import (
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// Split 测试
// ─────────────────────────────────────────────────────────────────────────────

func TestSplit_PlainText(t *testing.T) {
	segs := Split("hello world")
	if len(segs) != 1 {
		t.Fatalf("期望 1 个片段，得到 %d", len(segs))
	}
	if segs[0].Kind != KindText {
		t.Errorf("期望 KindText，得到 %v", segs[0].Kind)
	}
	if segs[0].Content != "hello world" {
		t.Errorf("内容不符: %q", segs[0].Content)
	}
}

func TestSplit_InlineCode(t *testing.T) {
	segs := Split("Use `init` to start")
	// 期望: ["Use ", "`init`", " to start"]
	if len(segs) != 3 {
		t.Fatalf("期望 3 个片段，得到 %d: %v", len(segs), segs)
	}
	assertEqual(t, segs[0], KindText, "Use ")
	assertEqual(t, segs[1], KindCode, "`init`")
	assertEqual(t, segs[2], KindText, " to start")
}

func TestSplit_FencedCodeBlock(t *testing.T) {
	input := "示例：\n```rust\nlet x = 1;\n```\n结束"
	segs := Split(input)
	kinds := make([]SegmentKind, len(segs))
	for i, s := range segs {
		kinds[i] = s.Kind
	}
	// 必须包含至少一个 KindCode 片段
	hasCode := false
	for _, s := range segs {
		if s.Kind == KindCode && strings.Contains(s.Content, "let x = 1;") {
			hasCode = true
		}
	}
	if !hasCode {
		t.Errorf("未找到包含代码内容的 KindCode 片段，实际片段: %v", segs)
	}
}

func TestSplit_MultipleInlineCodes(t *testing.T) {
	input := "调用 `foo()` 或 `bar()`"
	segs := Split(input)
	codeCnt := 0
	for _, s := range segs {
		if s.Kind == KindCode {
			codeCnt++
		}
	}
	if codeCnt != 2 {
		t.Errorf("期望 2 个行内代码片段，得到 %d", codeCnt)
	}
}

func TestSplit_TildeBlock(t *testing.T) {
	input := "text\n~~~\ncode\n~~~\nmore"
	segs := Split(input)
	hasCode := false
	for _, s := range segs {
		if s.Kind == KindCode && strings.Contains(s.Content, "code") {
			hasCode = true
		}
	}
	if !hasCode {
		t.Errorf("未识别 ~~~ 围栏代码块: %v", segs)
	}
}

func TestSplit_Join_Roundtrip(t *testing.T) {
	cases := []string{
		"plain text",
		"Use `init` here",
		"text\n```go\nfoo()\n```\nafter",
		"a `b` c `d` e",
		"",
		"   ",
	}
	for _, input := range cases {
		got := Join(Split(input))
		if got != input {
			t.Errorf("Join(Split(%q)) = %q，期望与原文相同", input, got)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Protect / Restore 测试
// ─────────────────────────────────────────────────────────────────────────────

func TestProtect_NoCode(t *testing.T) {
	masked, codes := Protect("hello world")
	if len(codes) != 0 {
		t.Errorf("期望 0 个代码块，得到 %d", len(codes))
	}
	if masked != "hello world" {
		t.Errorf("无代码块时 masked 应等于原文，得到 %q", masked)
	}
}

func TestProtect_SingleInlineCode(t *testing.T) {
	masked, codes := Protect("Use `init` to start")
	if len(codes) != 1 {
		t.Fatalf("期望 1 个代码块，得到 %d", len(codes))
	}
	if codes[0] != "`init`" {
		t.Errorf("codes[0] = %q，期望 \"`init`\"", codes[0])
	}
	if !strings.Contains(masked, "$CODE_0$") {
		t.Errorf("masked 中未找到占位符 $CODE_0$：%q", masked)
	}
	if strings.Contains(masked, "`init`") {
		t.Errorf("masked 中不应包含原始代码块内容")
	}
}

func TestProtect_MultipleInlineCodes(t *testing.T) {
	masked, codes := Protect("调用 `foo()` 或 `bar()`")
	if len(codes) != 2 {
		t.Fatalf("期望 2 个代码块，得到 %d", len(codes))
	}
	if !strings.Contains(masked, "$CODE_0$") {
		t.Errorf("masked 缺少 $CODE_0$: %q", masked)
	}
	if !strings.Contains(masked, "$CODE_1$") {
		t.Errorf("masked 缺少 $CODE_1$: %q", masked)
	}
}

func TestProtect_FencedBlock(t *testing.T) {
	input := "示例：\n```rust\nlet x = 1;\n```\n结束"
	masked, codes := Protect(input)
	if len(codes) == 0 {
		t.Fatal("期望至少 1 个代码块")
	}
	found := false
	for _, c := range codes {
		if strings.Contains(c, "let x = 1;") {
			found = true
		}
	}
	if !found {
		t.Errorf("代码内容未被提取到 codes: %v", codes)
	}
	if strings.Contains(masked, "let x = 1;") {
		t.Errorf("masked 中不应包含代码块内容")
	}
}

func TestRestore_Basic(t *testing.T) {
	masked, codes := Protect("Use `init` to start")
	restored := Restore(masked, codes)
	if restored != "Use `init` to start" {
		t.Errorf("还原失败: %q", restored)
	}
}

func TestRestore_NoCodes(t *testing.T) {
	result := Restore("hello world", nil)
	if result != "hello world" {
		t.Errorf("无 codes 时应原样返回: %q", result)
	}
}

func TestRestore_WithSpacesInPlaceholder(t *testing.T) {
	// 模拟翻译引擎在占位符内加了空格的情况
	codes := []string{"`init`"}
	modified := "使用 $ CODE_0 $ 开始"
	restored := Restore(modified, codes)
	if !strings.Contains(restored, "`init`") {
		t.Errorf("宽松匹配还原失败: %q", restored)
	}
}

func TestRestore_MultipleCodes(t *testing.T) {
	input := "Call `foo()` or `bar()` to proceed"
	masked, codes := Protect(input)
	restored := Restore(masked, codes)
	if restored != input {
		t.Errorf("多代码块还原失败:\n  原文: %q\n  还原: %q", input, restored)
	}
}

func TestProtectRestore_Roundtrip(t *testing.T) {
	cases := []string{
		// 纯文本
		"plain text without code",
		// 单个行内代码
		"Use `init` to setup",
		// 多个行内代码
		"Call `foo()` and `bar()` here",
		// 围栏代码块
		"示例：\n```rust\nlet x = 1;\n```\n结束",
		// 问题截图中的真实场景：文字和围栏代码块在同一行（不规范 markdown）
		"使用[init] 设置默认订阅者: ```rust tracing_subscriber::fmt().init();```",
		// 行内代码和围栏代码混合
		"Use `Foo` struct:\n```go\ntype Foo struct{}\n```",
		// 代码块内含反引号
		"code: `x := \"hello\"`",
		// 空文本
		"",
		// 纯代码块
		"```\nonly code\n```",
		// 多个围栏代码块
		"block1:\n```\na\n```\nblock2:\n```\nb\n```",
	}

	for _, input := range cases {
		masked, codes := Protect(input)
		restored := Restore(masked, codes)
		if restored != input {
			t.Errorf("Protect/Restore 往返测试失败:\n  输入:   %q\n  masked: %q\n  还原:   %q", input, masked, restored)
		}
	}
}

// TestProtectRestore_ScreenshotCase 专门测试截图中出现的问题场景：
// "使用[init] 设置默认订阅者: ```rust tracing_subscriber::fmt().init();"
// 这种代码块和文字在同一行的混排情况。
func TestProtectRestore_ScreenshotCase(t *testing.T) {
	// 模拟 LSP hover 文档中真实出现的混排格式
	input := "使用[init] 设置默认订阅者: ```rust tracing_subscriber::fmt().init();\n\n配置输出格式: ```rust\ntracing_subscriber::fmt()\n```"

	masked, codes := Protect(input)

	t.Logf("原文:   %q", input)
	t.Logf("masked: %q", masked)
	t.Logf("codes:  %v", codes)

	// masked 中不应再含有代码块内容（已被占位符替换）
	if strings.Contains(masked, "tracing_subscriber") {
		t.Errorf("masked 中仍含有代码内容，占位符替换不完整: %q", masked)
	}

	// 还原后应与原文完全一致
	restored := Restore(masked, codes)
	if restored != input {
		t.Errorf("还原失败:\n  期望: %q\n  实际: %q", input, restored)
	}
}

// TestProtect_MaskedTextIsTranslatable 验证 masked 文本适合送入翻译引擎：
// 应只含普通文本和占位符，不含反引号代码块。
func TestProtect_MaskedTextIsTranslatable(t *testing.T) {
	inputs := []string{
		"Returns the `Vec` length",
		"Use `init` or `run` to start the `Server`",
		"```rust\nfn main() {}\n```\nSee above example",
	}
	for _, input := range inputs {
		masked, _ := Protect(input)
		if strings.Contains(masked, "```") {
			t.Errorf("masked 中含有围栏代码块，翻译引擎可能无法正确处理: %q → %q", input, masked)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 辅助函数
// ─────────────────────────────────────────────────────────────────────────────

func assertEqual(t *testing.T, seg Segment, kind SegmentKind, content string) {
	t.Helper()
	if seg.Kind != kind {
		t.Errorf("Kind: 期望 %v，得到 %v（内容: %q）", kind, seg.Kind, seg.Content)
	}
	if seg.Content != content {
		t.Errorf("Content: 期望 %q，得到 %q", content, seg.Content)
	}
}
