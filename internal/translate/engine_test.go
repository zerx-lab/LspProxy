package translate

import (
	"context"
	"sync/atomic"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// NormalizeKey 单元测试
// ─────────────────────────────────────────────────────────────────────────────

func TestNormalizeKey_Basic(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "空字符串",
			input: "",
			want:  "",
		},
		{
			name:  "单行无空白差异",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "折叠行内多余空白",
			input: "hello   world   foo",
			want:  "hello world foo",
		},
		{
			name:  "去除行首尾空白",
			input: "  hello world  ",
			want:  "hello world",
		},
		{
			name:  "统一 CRLF 换行",
			input: "line1\r\nline2\r\nline3",
			want:  "line1\nline2\nline3",
		},
		{
			name:  "统一 CR 换行",
			input: "line1\rline2\rline3",
			want:  "line1\nline2\nline3",
		},
		{
			name:  "折叠连续空行",
			input: "line1\n\n\n\n\nline2",
			want:  "line1\n\nline2",
		},
		{
			name:  "保留一个空行作为段落分隔",
			input: "para1\n\npara2",
			want:  "para1\n\npara2",
		},
		{
			name:  "混合场景",
			input: "  hello   world  \n\n\n  foo   bar  \n\n  baz  ",
			want:  "hello world\n\nfoo bar\n\nbaz",
		},
		{
			name:  "制表符折叠",
			input: "hello\t\tworld",
			want:  "hello world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeKey(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeKey(%q)\n  got:  %q\n  want: %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeKey_Idempotent(t *testing.T) {
	// 规范化应该是幂等的：NormalizeKey(NormalizeKey(x)) == NormalizeKey(x)
	inputs := []string{
		"hello   world\n\n\n\nfoo",
		"  line1  \r\n  line2  \r\n",
		"\t\thello\t\t",
		"a\n\n\n\nb\n\n\n\nc",
	}
	for _, input := range inputs {
		first := NormalizeKey(input)
		second := NormalizeKey(first)
		if first != second {
			t.Errorf("NormalizeKey 不是幂等的:\n  input:  %q\n  first:  %q\n  second: %q", input, first, second)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CachedEngine 缓存规范化复用测试
// ─────────────────────────────────────────────────────────────────────────────

// mockEngine 是一个模拟翻译引擎，记录调用次数
type mockEngine struct {
	callCount   atomic.Int64
	translateFn func(text, targetLang string) (string, error)
}

func (m *mockEngine) Translate(ctx context.Context, text, targetLang string) (string, error) {
	m.callCount.Add(1)
	if m.translateFn != nil {
		return m.translateFn(text, targetLang)
	}
	return "[translated] " + text, nil
}

func (m *mockEngine) Name() string {
	return "mock"
}

func TestCachedEngine_NormalizedKeyReuse(t *testing.T) {
	mock := &mockEngine{}
	engine := NewCachedEngine(mock, DefaultMemoryLimit)
	ctx := context.Background()

	// 第一次翻译：原始文本
	result1, err := engine.Translate(ctx, "hello   world", "zh-CN")
	if err != nil {
		t.Fatalf("第一次翻译失败: %v", err)
	}

	// 第二次翻译：空白不同但语义相同的文本
	result2, err := engine.Translate(ctx, "hello world", "zh-CN")
	if err != nil {
		t.Fatalf("第二次翻译失败: %v", err)
	}

	// 应该命中缓存，底层引擎只调用一次
	if mock.callCount.Load() != 1 {
		t.Errorf("期望底层引擎调用 1 次，实际调用 %d 次", mock.callCount.Load())
	}

	// 翻译结果应相同
	if result1 != result2 {
		t.Errorf("规范化后缓存未命中:\n  result1: %q\n  result2: %q", result1, result2)
	}
}

func TestCachedEngine_DifferentTextNotReuse(t *testing.T) {
	mock := &mockEngine{}
	engine := NewCachedEngine(mock, DefaultMemoryLimit)
	ctx := context.Background()

	// 两个语义不同的文本
	_, _ = engine.Translate(ctx, "hello world", "zh-CN")
	_, _ = engine.Translate(ctx, "goodbye world", "zh-CN")

	// 底层引擎应调用两次
	if mock.callCount.Load() != 2 {
		t.Errorf("期望底层引擎调用 2 次，实际调用 %d 次", mock.callCount.Load())
	}
}

func TestCachedEngine_CRLFReuse(t *testing.T) {
	mock := &mockEngine{}
	engine := NewCachedEngine(mock, DefaultMemoryLimit)
	ctx := context.Background()

	// CRLF vs LF 应命中同一缓存
	_, _ = engine.Translate(ctx, "line1\r\nline2", "zh-CN")
	_, _ = engine.Translate(ctx, "line1\nline2", "zh-CN")

	if mock.callCount.Load() != 1 {
		t.Errorf("CRLF/LF 应共享缓存，期望调用 1 次，实际调用 %d 次", mock.callCount.Load())
	}
}
