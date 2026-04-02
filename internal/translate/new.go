// Package translate 提供翻译引擎的工厂函数。
package translate

import (
	"fmt"

	"LspProxy/internal/config"
)

// New 根据配置创建对应的翻译引擎，并包装三级缓存（内存 LRU + 磁盘词典）。
//
// 支持的引擎类型：
//   - "google"：使用 Google 免费翻译 API，无需密钥
//   - "openai"：使用 OpenAI 兼容 API（支持 DeepSeek、Qwen、Ollama 等）
//
// 缓存策略：
//  1. 内存 LRU：按字节大小限制（cfg.Proxy.CacheSize MB，默认 30MB）
//  2. 磁盘词典：持久化到 cfg.Proxy.DictFile（JSON 格式），进程退出不丢失
//  3. 在线翻译：前两级均未命中时调用底层引擎
func New(cfg *config.Config) (Engine, error) {
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
		base = NewOpenAIEngine(oaiCfg.BaseURL, oaiCfg.APIKey, oaiCfg.Model)

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

	disk, err := NewDiskDict(dictPath)
	if err != nil {
		// 磁盘词典初始化失败时降级为纯内存缓存，不影响代理正常运行
		return NewCachedEngine(base, memoryLimit), nil
	}

	return NewDictEngine(base, memoryLimit, disk), nil
}
