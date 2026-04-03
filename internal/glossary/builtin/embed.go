// Package builtin 提供内嵌的默认术语词汇本资源。
//
// 构建时通过 go:embed 将 *.toml 文件嵌入二进制，
// 首次启动时调用 [Extract] 释放到用户的词汇本目录。
// 已存在的文件不会被覆盖，确保用户自行编辑的内容不受影响。
package builtin

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// FS 内嵌所有 *.toml 词汇本文件（构建时自动打包进二进制）
//
//go:embed *.toml
var FS embed.FS

// Extract 将内嵌的默认词汇本释放到指定目录。
//
// 释放策略（保护用户数据）：
//   - 目标文件不存在 → 写入内嵌内容
//   - 目标文件已存在 → 跳过，不覆盖（用户可能已编辑）
//
// dir 为词汇本目录路径（如 ~/.local/share/lsp-proxy/glossary/），
// 目录不存在时自动创建。
//
// 返回实际写入的文件数量和错误（单个文件写入失败不中断，继续处理其余文件）。
func Extract(dir string) (written int, firstErr error) {
	// 确保目标目录存在
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, fmt.Errorf("创建词汇本目录失败 [%s]: %w", dir, err)
	}

	entries, err := FS.ReadDir(".")
	if err != nil {
		return 0, fmt.Errorf("读取内嵌词汇本列表失败: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		destPath := filepath.Join(dir, name)

		// 目标文件已存在 → 跳过，保护用户数据
		if _, err := os.Stat(destPath); err == nil {
			continue
		}

		// 读取内嵌文件内容
		data, err := fs.ReadFile(FS, name)
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("读取内嵌词汇本 [%s] 失败: %w", name, err)
			}
			continue
		}

		// 写入目标文件
		if err := os.WriteFile(destPath, data, 0o644); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("释放词汇本 [%s] 失败: %w", destPath, err)
			}
			continue
		}

		written++
	}

	return written, firstErr
}

// List 返回所有内嵌词汇本的文件名列表。
func List() []string {
	entries, err := FS.ReadDir(".")
	if err != nil {
		return nil
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names
}
