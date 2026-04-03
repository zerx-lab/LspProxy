package builtin_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zerx-lab/LspProxy/internal/glossary/builtin"
)

// ─────────────────────────────────────────────
// 辅助函数
// ─────────────────────────────────────────────

// writeManifest 向测试目录写入指定的 manifest 快照文件。
// 用于模拟「上次内嵌状态」，测试增量合并的各种场景。
func writeManifest(t *testing.T, dir string, files map[string]map[string]string) {
	t.Helper()
	type manifestShape struct {
		Files map[string]map[string]string `json:"files"`
	}
	data, err := json.MarshalIndent(manifestShape{Files: files}, "", "  ")
	if err != nil {
		t.Fatalf("序列化 manifest 失败: %v", err)
	}
	path := filepath.Join(dir, "_builtin_manifest.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("写入 manifest 失败: %v", err)
	}
}

// readFile 读取测试目录中的文件内容，失败时终止测试。
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取文件失败 [%s]: %v", path, err)
	}
	return string(data)
}

// countOccurrences 统计 substr 在 s 中出现的次数。
func countOccurrences(s, substr string) int {
	count := 0
	idx := 0
	for {
		pos := strings.Index(s[idx:], substr)
		if pos == -1 {
			break
		}
		count++
		idx += pos + len(substr)
	}
	return count
}

// builtinFileNames 返回所有内嵌文件名列表（通过 builtin.List()）。
func builtinFileNames(t *testing.T) []string {
	t.Helper()
	names := builtin.List()
	if len(names) == 0 {
		t.Fatal("builtin.List() 返回空列表，内嵌词汇本未正确嵌入")
	}
	return names
}

// ─────────────────────────────────────────────
// 测试用例
// ─────────────────────────────────────────────

// TestMergeExtract_FirstRun 验证文件不存在时，MergeExtract 直接写入整个内嵌文件。
func TestMergeExtract_FirstRun(t *testing.T) {
	dir := t.TempDir()

	added, err := builtin.MergeExtract(dir)
	if err != nil {
		t.Fatalf("MergeExtract 返回错误: %v", err)
	}

	names := builtinFileNames(t)

	// 验证每个内嵌文件都已写入目标目录
	for _, name := range names {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("内嵌文件 %s 未被写入: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("内嵌文件 %s 写入后内容为空", name)
		}

		// 验证写入内容与内嵌原始内容一致
		original, err := builtin.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("读取内嵌原始内容失败 [%s]: %v", name, err)
		}
		written := readFile(t, path)
		if written != string(original) {
			t.Errorf("文件 %s 内容与内嵌原始不一致\n期望长度: %d\n实际长度: %d",
				name, len(original), len(written))
		}
	}

	// 验证 added 数量 > 0（首次写入应有术语计数）
	if added == 0 {
		t.Error("首次运行 added 为 0，期望大于 0")
	}

	// 验证 manifest 文件已生成
	manifestPath := filepath.Join(dir, "_builtin_manifest.json")
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		t.Error("manifest 文件未生成")
	}

	t.Logf("首次运行：added=%d，内嵌文件数=%d", added, len(names))
}

// TestMergeExtract_NewTermsAppended 验证已有文件时，内嵌中新增的词条被追加到文件末尾。
func TestMergeExtract_NewTermsAppended(t *testing.T) {
	dir := t.TempDir()

	// ── 模拟用户已有文件（只包含部分旧词条）──
	userContent := `# rust-analyzer 专属术语词汇本（用户版本）
[terms]
"ownership" = "所有权"
"lifetime" = "生命周期"
`
	targetPath := filepath.Join(dir, "rust-analyzer.toml")
	if err := os.WriteFile(targetPath, []byte(userContent), 0o644); err != nil {
		t.Fatalf("写入用户文件失败: %v", err)
	}

	// ── 模拟上次 manifest：上次内嵌只有这两个词条 ──
	// 因此内嵌中其他词条对于 manifest 来说都是"新增"的
	writeManifest(t, dir, map[string]map[string]string{
		"rust-analyzer.toml": {
			"ownership": "所有权",
			"lifetime":  "生命周期",
		},
	})

	added, err := builtin.MergeExtract(dir)
	if err != nil {
		t.Fatalf("MergeExtract 返回错误: %v", err)
	}

	// 应有新词条被追加（内嵌中有大量词条，但 manifest 只记录了 2 个）
	if added == 0 {
		t.Error("期望有新词条被追加，但 added=0")
	}

	content := readFile(t, targetPath)

	// 验证用户原有内容未被修改（仍在文件头部）
	if !strings.HasPrefix(content, userContent) {
		t.Errorf("用户原有内容被修改\n期望文件以原始内容开头:\n%s\n实际内容头部:\n%s",
			userContent, content[:min(len(content), 200)])
	}

	// 验证追加分隔注释存在
	if !strings.Contains(content, "内嵌默认术语") {
		t.Error("追加内容中缺少分隔注释")
	}

	// 验证原有词条没有重复
	if countOccurrences(content, `"ownership"`) != 1 {
		t.Errorf("ownership 词条出现了多次，期望恰好 1 次")
	}
	if countOccurrences(content, `"lifetime"`) != 1 {
		t.Errorf("lifetime 词条出现了多次，期望恰好 1 次")
	}

	t.Logf("新词条追加：added=%d", added)
}

// TestMergeExtract_UserDeletedTermSkipped 验证用户主动删除的词条（在 manifest 中有记录）不会被重新追加。
func TestMergeExtract_UserDeletedTermSkipped(t *testing.T) {
	dir := t.TempDir()

	// ── 用户文件：不含 "borrow checker"（用户主动删除了它）──
	userContent := `# 用户词汇本（用户删除了 borrow checker）
[terms]
"ownership" = "所有权"
"lifetime" = "生命周期"
`
	targetPath := filepath.Join(dir, "rust-analyzer.toml")
	if err := os.WriteFile(targetPath, []byte(userContent), 0o644); err != nil {
		t.Fatalf("写入用户文件失败: %v", err)
	}

	// ── manifest：上次内嵌时包含 "borrow checker"，说明用户是主动删除的 ──
	writeManifest(t, dir, map[string]map[string]string{
		"rust-analyzer.toml": {
			"ownership":      "所有权",
			"lifetime":       "生命周期",
			"borrow checker": "借用检查器",
		},
	})

	_, err := builtin.MergeExtract(dir)
	if err != nil {
		t.Fatalf("MergeExtract 返回错误: %v", err)
	}

	content := readFile(t, targetPath)

	// "borrow checker" 在 manifest 中有记录，用户主动删除，不应被重新追加
	if strings.Contains(content, `"borrow checker"`) {
		t.Error(`用户主动删除的词条 "borrow checker" 被重新追加了，期望跳过`)
	}

	// 用户原有的词条应保持不变
	if !strings.Contains(content, `"ownership"`) {
		t.Error(`"ownership" 词条丢失`)
	}
	if !strings.Contains(content, `"lifetime"`) {
		t.Error(`"lifetime" 词条丢失`)
	}

	t.Logf("用户删除词条验证通过，文件长度=%d", len(content))
}

// TestMergeExtract_UserEditPreserved 验证用户文件中已有的词条（包括用户自行修改的译文）不被覆盖。
func TestMergeExtract_UserEditPreserved(t *testing.T) {
	dir := t.TempDir()

	// ── 用户文件：ownership 的译文被用户改成了「持有权」（与内嵌版本不同）──
	userContent := `# 用户自定义词汇本
[terms]
"ownership" = "持有权（用户自定义）"
"lifetime" = "生命周期"
"my_custom_term" = "我的自定义术语"
`
	targetPath := filepath.Join(dir, "rust-analyzer.toml")
	if err := os.WriteFile(targetPath, []byte(userContent), 0o644); err != nil {
		t.Fatalf("写入用户文件失败: %v", err)
	}

	// ── manifest 为空（模拟全新内嵌场景）──
	writeManifest(t, dir, map[string]map[string]string{
		"rust-analyzer.toml": {},
	})

	_, err := builtin.MergeExtract(dir)
	if err != nil {
		t.Fatalf("MergeExtract 返回错误: %v", err)
	}

	content := readFile(t, targetPath)

	// 用户自定义的译文必须保留，不能被内嵌版本覆盖
	if !strings.Contains(content, `"ownership" = "持有权（用户自定义）"`) {
		t.Errorf("用户对 ownership 的自定义译文被覆盖了\n实际内容片段:\n%s",
			content[:min(len(content), 300)])
	}

	// 内嵌的 "ownership" = "所有权" 不应出现（不能追加已存在的 key）
	if strings.Contains(content, `"ownership" = "所有权"`) {
		t.Error(`内嵌的 "ownership" = "所有权" 被追加了，期望跳过已存在的 key`)
	}

	// 用户自定义术语应保留
	if !strings.Contains(content, `"my_custom_term"`) {
		t.Error(`用户自定义术语 "my_custom_term" 丢失`)
	}

	// ownership 在文件中只应出现一次
	if countOccurrences(content, `"ownership"`) != 1 {
		t.Errorf("ownership 在文件中出现了 %d 次，期望恰好 1 次",
			countOccurrences(content, `"ownership"`))
	}

	t.Logf("用户编辑保护验证通过")
}

// TestMergeExtract_Idempotent 验证二次调用不会重复追加相同词条。
func TestMergeExtract_Idempotent(t *testing.T) {
	dir := t.TempDir()

	// ── 用户已有文件 ──
	userContent := `# 用户词汇本
[terms]
"ownership" = "所有权"
`
	targetPath := filepath.Join(dir, "rust-analyzer.toml")
	if err := os.WriteFile(targetPath, []byte(userContent), 0o644); err != nil {
		t.Fatalf("写入用户文件失败: %v", err)
	}

	// manifest 为空，第一次调用会追加所有内嵌新词条
	writeManifest(t, dir, map[string]map[string]string{
		"rust-analyzer.toml": {},
	})

	// ── 第一次调用 ──
	added1, err := builtin.MergeExtract(dir)
	if err != nil {
		t.Fatalf("第一次 MergeExtract 失败: %v", err)
	}

	contentAfter1 := readFile(t, targetPath)

	// ── 第二次调用（manifest 已更新，不应再追加任何词条）──
	added2, err := builtin.MergeExtract(dir)
	if err != nil {
		t.Fatalf("第二次 MergeExtract 失败: %v", err)
	}

	contentAfter2 := readFile(t, targetPath)

	// 第二次调用不应追加任何词条
	if added2 != 0 {
		t.Errorf("第二次调用 added=%d，期望 0（幂等性违反）", added2)
	}

	// 文件内容不应改变
	if contentAfter1 != contentAfter2 {
		t.Errorf("第二次调用后文件内容发生了变化，幂等性违反\n第一次后长度=%d\n第二次后长度=%d",
			len(contentAfter1), len(contentAfter2))
	}

	// 验证每个术语在文件中只出现一次（以 "lifetime" 为例）
	if count := countOccurrences(contentAfter2, `"lifetime"`); count > 1 {
		t.Errorf(`"lifetime" 在文件中出现了 %d 次，期望最多 1 次（重复追加）`, count)
	}

	t.Logf("幂等性验证通过：第一次 added=%d，第二次 added=%d", added1, added2)
}

// TestMergeExtract_ManifestUpdated 验证每次运行后 manifest 都会更新为当前内嵌状态。
func TestMergeExtract_ManifestUpdated(t *testing.T) {
	dir := t.TempDir()

	// 首次运行（文件不存在）
	_, err := builtin.MergeExtract(dir)
	if err != nil {
		t.Fatalf("MergeExtract 返回错误: %v", err)
	}

	// 读取生成的 manifest
	manifestPath := filepath.Join(dir, "_builtin_manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("读取 manifest 失败: %v", err)
	}

	type manifestShape struct {
		Files map[string]map[string]string `json:"files"`
	}
	var m manifestShape
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("解析 manifest JSON 失败: %v", err)
	}

	// manifest 应包含内嵌文件记录
	names := builtinFileNames(t)
	for _, name := range names {
		terms, ok := m.Files[name]
		if !ok {
			t.Errorf("manifest 中缺少文件记录: %s", name)
			continue
		}
		if len(terms) == 0 {
			t.Errorf("manifest 中 %s 的术语快照为空", name)
		}
	}

	// manifest 中应包含内嵌词汇本的核心术语
	raTerms, ok := m.Files["rust-analyzer.toml"]
	if !ok {
		t.Fatal("manifest 中缺少 rust-analyzer.toml 记录")
	}

	expectedKeys := []string{"ownership", "lifetime", "borrow checker", "trait"}
	for _, key := range expectedKeys {
		if _, found := raTerms[key]; !found {
			t.Errorf("manifest 中缺少核心术语 %q", key)
		}
	}

	t.Logf("manifest 更新验证通过，记录了 %d 个文件", len(m.Files))
}

// TestMergeExtract_CreatesDirectory 验证目标目录不存在时自动创建。
func TestMergeExtract_CreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "glossary", "dir")

	added, err := builtin.MergeExtract(dir)
	if err != nil {
		t.Fatalf("MergeExtract 失败: %v", err)
	}
	if added == 0 {
		t.Error("MergeExtract 未写入任何词条")
	}
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("MergeExtract 未创建目标目录")
	}
}

// ─────────────────────────────────────────────
// 辅助
// ─────────────────────────────────────────────

// min 返回两个整数中的较小值（Go 1.21 前标准库没有泛型 min）。
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
