package builtin_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zerx-lab/LspProxy/internal/glossary/builtin"
)

// TestList_ReturnsBuiltinFiles 验证 List() 能返回内嵌的词汇本文件列表。
func TestList_ReturnsBuiltinFiles(t *testing.T) {
	names := builtin.List()
	if len(names) == 0 {
		t.Fatal("List() 返回空列表，期望至少包含 rust-analyzer.toml")
	}

	found := false
	for _, name := range names {
		if name == "rust-analyzer.toml" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("List() 未包含 rust-analyzer.toml，实际返回: %v", names)
	}
}

// TestList_AllFilesAreTOML 验证内嵌文件全部为 .toml 后缀。
func TestList_AllFilesAreTOML(t *testing.T) {
	for _, name := range builtin.List() {
		if filepath.Ext(name) != ".toml" {
			t.Errorf("内嵌文件 %q 不是 .toml 后缀", name)
		}
	}
}

// TestExtract_CreatesFiles 验证 Extract 能将内嵌文件释放到目标目录。
func TestExtract_CreatesFiles(t *testing.T) {
	dir := t.TempDir()

	written, err := builtin.Extract(dir)
	if err != nil {
		t.Fatalf("Extract 失败: %v", err)
	}

	names := builtin.List()
	if written != len(names) {
		t.Errorf("Extract 写入 %d 个文件，期望 %d", written, len(names))
	}

	// 检查每个文件都存在且内容非空
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("释放的文件 %s 不存在: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("释放的文件 %s 内容为空", name)
		}
	}
}

// TestExtract_DoesNotOverwriteExisting 验证已存在的文件不会被覆盖。
func TestExtract_DoesNotOverwriteExisting(t *testing.T) {
	dir := t.TempDir()

	// 预先写入一个同名文件，内容与内嵌版本不同
	customContent := []byte("# 用户自定义内容，不应被覆盖\n[terms]\n\"hello\" = \"你好\"\n")
	targetPath := filepath.Join(dir, "rust-analyzer.toml")
	if err := os.WriteFile(targetPath, customContent, 0o644); err != nil {
		t.Fatalf("写入预置文件失败: %v", err)
	}

	written, err := builtin.Extract(dir)
	if err != nil {
		t.Fatalf("Extract 失败: %v", err)
	}

	// rust-analyzer.toml 已存在，不应被计入 written（除非有其他新文件）
	names := builtin.List()
	expectedWritten := len(names) - 1 // rust-analyzer.toml 被跳过
	if written != expectedWritten {
		t.Errorf("Extract 写入 %d 个文件，期望 %d（rust-analyzer.toml 应被跳过）", written, expectedWritten)
	}

	// 验证文件内容未被覆盖
	data, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("读取文件失败: %v", err)
	}
	if string(data) != string(customContent) {
		t.Errorf("文件内容被覆盖了！\n期望: %s\n实际: %s", customContent, data)
	}
}

// TestExtract_IdempotentSecondCall 验证二次调用不会重复写入。
func TestExtract_IdempotentSecondCall(t *testing.T) {
	dir := t.TempDir()

	// 第一次释放
	written1, err := builtin.Extract(dir)
	if err != nil {
		t.Fatalf("第一次 Extract 失败: %v", err)
	}
	if written1 == 0 {
		t.Fatal("第一次 Extract 未写入任何文件")
	}

	// 第二次释放：所有文件已存在，不应写入
	written2, err := builtin.Extract(dir)
	if err != nil {
		t.Fatalf("第二次 Extract 失败: %v", err)
	}
	if written2 != 0 {
		t.Errorf("第二次 Extract 写入了 %d 个文件，期望 0（所有文件已存在）", written2)
	}
}

// TestExtract_CreatesDirectory 验证目标目录不存在时自动创建。
func TestExtract_CreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "glossary", "dir")

	written, err := builtin.Extract(dir)
	if err != nil {
		t.Fatalf("Extract 失败: %v", err)
	}
	if written == 0 {
		t.Error("Extract 未写入任何文件")
	}

	// 验证目录已创建
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("Extract 未创建目标目录")
	}
}

// TestExtract_RustAnalyzerContent 验证内嵌的 rust-analyzer.toml 内容可被解析。
func TestExtract_RustAnalyzerContent(t *testing.T) {
	// 直接从 embed.FS 读取内嵌内容
	data, err := builtin.FS.ReadFile("rust-analyzer.toml")
	if err != nil {
		t.Fatalf("读取内嵌 rust-analyzer.toml 失败: %v", err)
	}

	content := string(data)

	// 验证包含 [terms] 节
	if !contains(content, "[terms]") {
		t.Error("rust-analyzer.toml 缺少 [terms] 节")
	}

	// 验证包含一些关键的 Rust 术语
	expectedTerms := []string{
		"borrow checker",
		"lifetime",
		"ownership",
		"trait",
		"generic",
		"closure",
		"iterator",
		"unsafe",
		"macro",
	}
	for _, term := range expectedTerms {
		if !contains(content, term) {
			t.Errorf("rust-analyzer.toml 缺少核心术语 %q", term)
		}
	}
}

// TestFS_ReadDir 验证内嵌的 FS 可正常读取目录。
func TestFS_ReadDir(t *testing.T) {
	entries, err := builtin.FS.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir 失败: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("内嵌 FS 为空")
	}

	for _, entry := range entries {
		if entry.IsDir() {
			t.Errorf("内嵌 FS 中不应包含目录: %s", entry.Name())
		}
	}
}

// contains 检查字符串是否包含子串（简单辅助函数，避免导入 strings）
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
