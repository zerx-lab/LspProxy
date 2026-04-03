// Package translate 提供翻译引擎的工厂函数。
package translate

import (
	"fmt"
	"log/slog"

	"github.com/zerx-lab/LspProxy/internal/config"
	"github.com/zerx-lab/LspProxy/internal/glossary"
)

// New 根据配置创建对应的翻译引擎，并包装三级缓存（内存 LRU + 磁盘词典）和术语词汇本。
//
// lspName 为当前代理的 LSP 可执行文件名（如 "rust-analyzer"），用于加载 LSP 专属词汇本。
// 传空字符串时仅使用全局词汇本。
//
// 引擎链路（从外到内）：
//
//	GlossaryEngine → DictEngine（内存LRU + 磁盘词典 + 在线翻译）
//
// 查询顺序：
//  1. LSP 专属词汇本
//  2. 全局词汇本
//  3. 内存 LRU 缓存
//  4. 磁盘 JSON 词典
//  5. 在线翻译 API
func New(cfg *config.Config, lspName string, logger *slog.Logger) (Engine, error) {
	var base Engine

	switch cfg.Translate.Engine {
	case "google", "":
		// 空字符串时默认使用 Google 引擎
		base = NewGoogleEngine()

	case "openai":
		oaiCfg := cfg.Translate.OpenAI
		if oaiCfg.BaseURL == "" {
			return nil, fmt.Errorf("translate: openai 引擎需要配置 base_url")
		}
		if oaiCfg.Model == "" {
			return nil, fmt.Errorf("translate: openai 引擎需要配置 model")
		}
		// 解析提示词文件路径
		promptFile := oaiCfg.PromptFile
		if promptFile == "" {
			promptFile = config.DefaultPromptFile()
		}
		loader := NewPromptLoader(promptFile, logger)
		base = NewOpenAIEngine(oaiCfg.BaseURL, oaiCfg.APIKey, oaiCfg.Model, oaiCfg.ThinkingMode, loader)

	default:
		return nil, fmt.Errorf("translate: 不支持的翻译引擎 %q（可选值：google、openai）", cfg.Translate.Engine)
	}

	// 内存缓存上限（MB → 字节）
	cacheMB := cfg.Proxy.CacheSize
	if cacheMB <= 0 {
		cacheMB = 30
	}
	memoryLimit := int64(cacheMB) * 1024 * 1024

	// 磁盘词典路径
	dictPath := cfg.Proxy.DictFile
	if dictPath == "" {
		dictPath = config.DefaultDictFile()
	}

	// 磁盘词典最大条目数（0 表示不限制）
	dictMaxEntries := cfg.Proxy.DictMaxEntries

	disk, err := NewDiskDict(dictPath, dictMaxEntries)
	if err != nil {
		// 磁盘词典初始化失败时降级为纯内存缓存，不影响代理正常运行
		base = NewCachedEngine(base, memoryLimit)
	} else {
		base = NewDictEngine(base, memoryLimit, disk)
	}

	// ── 术语词汇本层（最高优先级）──
	glossaryDir := cfg.Proxy.GlossaryDir
	if glossaryDir == "" {
		glossaryDir = config.DefaultGlossaryDir()
	}

	var lspNames []string
	if lspName != "" {
		lspNames = []string{lspName}
	}

	g := glossary.New(glossaryDir, lspNames, logger)
	return glossary.NewGlossaryEngine(base, g, lspName, logger), nil
}
