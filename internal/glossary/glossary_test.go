package glossary_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"log/slog"

	"github.com/zerx-lab/LspProxy/internal/glossary"
)

// newLogger 创建一个丢弃所有日志的 slog.Logger，避免测试输出噪音
func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// ─────────────────────────────────────────────
// Glossary 基础查询测试
// ─────────────────────────────────────────────

func TestGlossary_Lookup_GlobalTerm(t *testing.T) {
	dir := t.TempDir()

	// 写入全局词汇本
	globalContent := `[terms]
"borrow checker" = "借用检查器"
"lifetime"       = "生命周期"
`
	writeToml(t, dir, "_global.toml", globalContent)

	g := glossary.New(dir, nil, newLogger())
	defer g.Close()

	v, ok := g.Lookup("borrow checker", "")
	if !ok || v != "借用检查器" {
		t.Errorf("期望 (借用检查器, true)，得到 (%q, %v)", v, ok)
	}

	v, ok = g.Lookup("lifetime", "")
	if !ok || v != "生命周期" {
		t.Errorf("期望 (生命周期, true)，得到 (%q, %v)", v, ok)
	}
}

func TestGlossary_Lookup_LSPSpecific(t *testing.T) {
	dir := t.TempDir()

	// rust-analyzer 专属词汇本
	lspContent := `[terms]
"trait bound" = "trait 约束"
`
	writeToml(t, dir, "rust-analyzer.toml", lspContent)

	g := glossary.New(dir, []string{"rust-analyzer"}, newLogger())
	defer g.Close()

	// LSP 专属命中
	v, ok := g.Lookup("trait bound", "rust-analyzer")
	if !ok || v != "trait 约束" {
		t.Errorf("期望 (trait 约束, true)，得到 (%q, %v)", v, ok)
	}

	// 其他 LSP 不命中
	_, ok = g.Lookup("trait bound", "clangd")
	if ok {
		t.Error("clangd 不应命中 rust-analyzer 专属词汇本")
	}
}

func TestGlossary_Lookup_Priority(t *testing.T) {
	dir := t.TempDir()

	// 全局词汇本中有 "panic" 的通用译文
	globalContent := `[terms]
"panic" = "panic（程序崩溃）"
`
	// rust-analyzer 专属词汇本覆盖为更精准的译文
	lspContent := `[terms]
"panic" = "panic（运行时恐慌）"
`
	writeToml(t, dir, "_global.toml", globalContent)
	writeToml(t, dir, "rust-analyzer.toml", lspContent)

	g := glossary.New(dir, []string{"rust-analyzer"}, newLogger())
	defer g.Close()

	// rust-analyzer 上下文应命中专属词汇本
	v, ok := g.Lookup("panic", "rust-analyzer")
	if !ok || v != "panic（运行时恐慌）" {
		t.Errorf("LSP 专属词汇本应优先于全局词汇本，得到 (%q, %v)", v, ok)
	}

	// 无 LSP 上下文时命中全局词汇本
	v, ok = g.Lookup("panic", "")
	if !ok || v != "panic（程序崩溃）" {
		t.Errorf("无 LSP 时应命中全局词汇本，得到 (%q, %v)", v, ok)
	}
}

func TestGlossary_Lookup_CaseSensitive(t *testing.T) {
	dir := t.TempDir()

	content := `[terms]
"Borrow Checker" = "借用检查器"
"Send" = "Send"
`
	writeToml(t, dir, "_global.toml", content)

	g := glossary.New(dir, nil, newLogger())
	defer g.Close()

	// 精确大小写匹配应命中
	v, ok := g.Lookup("Borrow Checker", "")
	if !ok || v != "借用检查器" {
		t.Errorf("精确匹配失败，期望 (借用检查器, true)，得到 (%q, %v)", v, ok)
	}

	v, ok = g.Lookup("Send", "")
	if !ok || v != "Send" {
		t.Errorf("精确匹配失败，期望 (Send, true)，得到 (%q, %v)", v, ok)
	}

	// 不同大小写不应命中
	for _, input := range []string{"borrow checker", "BORROW CHECKER", "borrow Checker", "send", "SEND"} {
		_, ok := g.Lookup(input, "")
		if ok {
			t.Errorf("大小写敏感模式下 %q 不应命中词汇本", input)
		}
	}
}

func TestGlossary_Lookup_Miss(t *testing.T) {
	dir := t.TempDir()

	content := `[terms]
"lifetime" = "生命周期"
`
	writeToml(t, dir, "_global.toml", content)

	g := glossary.New(dir, nil, newLogger())
	defer g.Close()

	_, ok := g.Lookup("unknown term here", "")
	if ok {
		t.Error("不存在的术语不应命中词汇本")
	}

	_, ok = g.Lookup("", "")
	if ok {
		t.Error("空字符串不应命中词汇本")
	}
}

func TestGlossary_EnsureLSP_CreatesFile(t *testing.T) {
	dir := t.TempDir()

	g := glossary.New(dir, nil, newLogger())
	defer g.Close()

	// EnsureLSP 应为未加载的 LSP 创建示例文件
	g.EnsureLSP("pyright")

	path := filepath.Join(dir, "pyright.toml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Errorf("EnsureLSP 未创建 pyright.toml")
	}
}

// ─────────────────────────────────────────────
// GlossaryEngine 测试
// ─────────────────────────────────────────────

// mockEngine 是用于测试的假翻译引擎
type mockEngine struct {
	called bool
	result string
}

func (m *mockEngine) Translate(_ context.Context, text, _ string) (string, error) {
	m.called = true
	return m.result, nil
}

func (m *mockEngine) Name() string { return "mock" }

func TestGlossaryEngine_HitSkipsBase(t *testing.T) {
	dir := t.TempDir()

	content := `[terms]
"lifetime" = "生命周期"
`
	writeToml(t, dir, "_global.toml", content)

	g := glossary.New(dir, nil, newLogger())
	defer g.Close()

	mock := &mockEngine{result: "should not be called"}
	eng := glossary.NewGlossaryEngine(mock, g, "", newLogger())

	result, err := eng.Translate(context.Background(), "lifetime", "zh-CN")
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if result != "生命周期" {
		t.Errorf("期望 '生命周期'，得到 %q", result)
	}
	if mock.called {
		t.Error("词汇本命中时不应调用底层引擎")
	}
}

func TestGlossaryEngine_MissFallsThrough(t *testing.T) {
	dir := t.TempDir()

	g := glossary.New(dir, nil, newLogger())
	defer g.Close()

	mock := &mockEngine{result: "mock translation"}
	eng := glossary.NewGlossaryEngine(mock, g, "", newLogger())

	result, err := eng.Translate(context.Background(), "some unknown text", "zh-CN")
	if err != nil {
		t.Fatalf("意外错误: %v", err)
	}
	if result != "mock translation" {
		t.Errorf("未命中时应透传底层引擎结果，得到 %q", result)
	}
	if !mock.called {
		t.Error("词汇本未命中时应调用底层引擎")
	}
}

// ─────────────────────────────────────────────
// 热重载测试
// ─────────────────────────────────────────────

func TestGlossary_HotReload(t *testing.T) {
	dir := t.TempDir()

	// 初始内容
	initialContent := `[terms]
"lifetime" = "生命周期（旧）"
`
	writeToml(t, dir, "_global.toml", initialContent)

	g := glossary.New(dir, nil, newLogger())
	defer g.Close()

	// 确认初始值
	v, ok := g.Lookup("lifetime", "")
	if !ok || v != "生命周期（旧）" {
		t.Fatalf("初始加载失败: (%q, %v)", v, ok)
	}

	// 更新词汇本文件
	updatedContent := `[terms]
"lifetime" = "生命周期"
`
	writeToml(t, dir, "_global.toml", updatedContent)

	// 等待热重载生效（最多 2 秒）
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v, ok := g.Lookup("lifetime", ""); ok && v == "生命周期" {
			return // 测试通过
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Error("热重载 2 秒内未生效")
}

// ─────────────────────────────────────────────
// 辅助函数
// ─────────────────────────────────────────────

func writeToml(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入测试 TOML 文件失败 [%s]: %v", path, err)
	}
}
