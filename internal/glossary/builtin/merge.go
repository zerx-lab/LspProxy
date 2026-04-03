// Package builtin 提供内嵌的默认术语词汇本资源。
// 本文件实现增量合并逻辑：将内嵌词汇本中的新增词条追加到用户文件，
// 同时完整保留用户已有的编辑内容。
package builtin

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// ─────────────────────────────────────────────
// 常量
// ─────────────────────────────────────────────

// manifestFileName manifest 文件名，存放在词汇本目录下。
const manifestFileName = "_builtin_manifest.json"

// appendHeader 追加新词条时写入的分隔注释行。
const appendHeader = "\n# ── 内嵌默认术语（由 LspProxy 自动追加）────────────────────\n"

// ─────────────────────────────────────────────
// 类型定义
// ─────────────────────────────────────────────

// builtinManifest 记录上次从内嵌数据写入每个文件的术语快照。
// key 为文件名（如 rust-analyzer.toml），value 为该文件的术语映射快照。
type builtinManifest struct {
	Files map[string]map[string]string `json:"files"`
}

// tomlFileData 是 TOML 词汇本的反序列化结构。
// 与 glossary 包中的 tomlFile 独立定义，避免循环依赖。
type tomlFileData struct {
	Terms map[string]string `toml:"terms"`
}

// ─────────────────────────────────────────────
// 公开接口
// ─────────────────────────────────────────────

// MergeExtract 将内嵌词汇本增量合并到指定目录。
//
// 合并策略：
//   - 目标文件不存在 → 直接写入整个内嵌文件（与 Extract 相同）
//   - 目标文件已存在 → 增量合并：
//     仅追加「内嵌中有、用户文件中没有、且不在上次 manifest 中」的词条。
//     内嵌中有、manifest 中也有、但用户文件中没有 → 用户主动删除，跳过。
//     用户文件中已有的词条永不修改。
//
// 每次运行后更新 manifest 为当前内嵌状态。
// 返回实际追加的词条总数以及第一个遇到的错误（不中断后续文件处理）。
func MergeExtract(dir string) (added int, firstErr error) {
	// 确保目标目录存在
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, fmt.Errorf("创建词汇本目录失败 [%s]: %w", dir, err)
	}

	// 读取内嵌文件列表
	entries, err := FS.ReadDir(".")
	if err != nil {
		return 0, fmt.Errorf("读取内嵌词汇本列表失败: %w", err)
	}

	// 加载上次 manifest
	manifestPath := filepath.Join(dir, manifestFileName)
	manifest := loadManifest(manifestPath)
	if manifest.Files == nil {
		manifest.Files = make(map[string]map[string]string)
	}

	// 本次运行后需要保存的新 manifest 快照
	newManifest := builtinManifest{
		Files: make(map[string]map[string]string),
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		destPath := filepath.Join(dir, name)

		// 读取内嵌文件内容
		data, err := fs.ReadFile(FS, name)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("读取内嵌词汇本 [%s] 失败: %w", name, err)
			}
			continue
		}

		// 解析内嵌词汇本的术语映射
		var builtinFile tomlFileData
		if err := toml.Unmarshal(data, &builtinFile); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("解析内嵌词汇本 TOML [%s] 失败: %w", name, err)
			}
			continue
		}
		builtinTerms := builtinFile.Terms
		if builtinTerms == nil {
			builtinTerms = make(map[string]string)
		}

		// 记录本次内嵌状态到新 manifest
		snapshot := make(map[string]string, len(builtinTerms))
		for k, v := range builtinTerms {
			snapshot[k] = v
		}
		newManifest.Files[name] = snapshot

		// 获取该文件的上次 manifest 快照
		prevManifest := manifest.Files[name]
		if prevManifest == nil {
			prevManifest = make(map[string]string)
		}

		// 目标文件不存在 → 直接写入
		if _, err := os.Stat(destPath); os.IsNotExist(err) {
			if err := os.WriteFile(destPath, data, 0o644); err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("写入词汇本 [%s] 失败: %w", destPath, err)
				}
				continue
			}
			added += len(builtinTerms)
			continue
		}

		// 目标文件已存在 → 增量合并
		n, err := mergeIntoFile(destPath, builtinTerms, prevManifest)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		added += n
	}

	// 更新 manifest 为当前内嵌状态
	if err := saveManifest(manifestPath, newManifest); err != nil && firstErr == nil {
		firstErr = err
	}

	return added, firstErr
}

// ─────────────────────────────────────────────
// 内部实现
// ─────────────────────────────────────────────

// mergeIntoFile 将内嵌词汇本中的新词条增量追加到已存在的用户文件。
//
// 追加条件（三个条件同时满足）：
//  1. 词条在内嵌数据中存在
//  2. 词条在用户文件中不存在（避免覆盖用户编辑）
//  3. 词条不在上次 manifest 中（区分「从未出现」与「用户主动删除」）
//
// 返回追加的词条数量。
func mergeIntoFile(path string, builtinTerms, prevManifest map[string]string) (int, error) {
	// 读取用户文件现有内容
	existingData, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("读取用户词汇本失败 [%s]: %w", path, err)
	}

	// 解析用户文件中已有的术语
	var userFile tomlFileData
	if err := toml.Unmarshal(existingData, &userFile); err != nil {
		// 解析失败时保守处理：不追加，避免破坏文件
		return 0, fmt.Errorf("解析用户词汇本 TOML 失败 [%s]: %w", path, err)
	}
	userTerms := userFile.Terms
	if userTerms == nil {
		userTerms = make(map[string]string)
	}

	// 筛选需要追加的词条：内嵌有 && 用户文件没有 && 上次 manifest 没有
	var toAppend []string
	for key := range builtinTerms {
		_, inUser := userTerms[key]
		if inUser {
			// 用户文件已有，跳过（保护用户编辑）
			continue
		}
		_, inPrev := prevManifest[key]
		if inPrev {
			// 上次 manifest 中存在但用户文件中没有 → 用户主动删除，跳过
			continue
		}
		// 内嵌中新出现的词条，需要追加
		toAppend = append(toAppend, key)
	}

	if len(toAppend) == 0 {
		return 0, nil
	}

	// 按 key 字母排序，保证输出稳定
	sort.Strings(toAppend)

	// 构建追加内容
	var sb strings.Builder
	sb.WriteString(appendHeader)
	for _, key := range toAppend {
		// 对 key 和 value 进行 TOML 字符串转义（使用带引号格式）
		sb.WriteString(fmt.Sprintf("%s = %s\n", tomlQuote(key), tomlQuote(builtinTerms[key])))
	}

	// 追加到文件末尾
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, fmt.Errorf("打开词汇本文件失败 [%s]: %w", path, err)
	}
	defer f.Close()

	if _, err := f.WriteString(sb.String()); err != nil {
		return 0, fmt.Errorf("追加词条到词汇本失败 [%s]: %w", path, err)
	}

	return len(toAppend), nil
}

// loadManifest 从磁盘加载 manifest 文件。
// 文件不存在或解析失败时返回空 manifest，不报错（首次运行属正常情况）。
func loadManifest(path string) builtinManifest {
	data, err := os.ReadFile(path)
	if err != nil {
		// 文件不存在或读取失败均返回空 manifest
		return builtinManifest{Files: make(map[string]map[string]string)}
	}

	var m builtinManifest
	if err := json.Unmarshal(data, &m); err != nil {
		// JSON 解析失败，返回空 manifest（下次运行会重建）
		return builtinManifest{Files: make(map[string]map[string]string)}
	}

	if m.Files == nil {
		m.Files = make(map[string]map[string]string)
	}
	return m
}

// saveManifest 将 manifest 序列化后写入磁盘。
func saveManifest(path string, m builtinManifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 manifest 失败: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("保存 manifest 失败 [%s]: %w", path, err)
	}
	return nil
}

// tomlQuote 对字符串进行 TOML 基本字符串转义，返回带双引号的格式。
// 处理双引号、反斜杠、换行、回车、制表符等特殊字符。
func tomlQuote(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}
