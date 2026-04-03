// Package config 提供 LspProxy 的配置管理功能。
// 支持从 YAML 文件加载配置，若文件不存在则使用默认值。
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config 全局配置结构
type Config struct {
	Translate TranslateConfig `mapstructure:"translate"`
	Proxy     ProxyConfig     `mapstructure:"proxy"`
	Log       LogConfig       `mapstructure:"log"`
}

// TranslateConfig 翻译引擎相关配置
type TranslateConfig struct {
	// Engine 翻译引擎类型，支持 "google" 和 "openai"
	Engine string       `mapstructure:"engine"`
	OpenAI OpenAIConfig `mapstructure:"openai"`
}

// OpenAIConfig OpenAI 兼容接口配置（支持 DeepSeek、Qwen、Ollama 等）
type OpenAIConfig struct {
	// BaseURL API 基础地址，例如 https://api.openai.com/v1
	BaseURL string `mapstructure:"base_url"`
	// APIKey 认证密钥
	APIKey string `mapstructure:"api_key"`
	// Model 使用的模型名称，例如 gpt-4o-mini
	Model string `mapstructure:"model"`
	// ThinkingMode 控制模型的思考/推理模式。
	// "auto"     - 自动判断：对已知思考模型（Qwen3、Doubao-thinking 等）自动关闭思考
	// "enabled"  - 强制开启思考（适用于需要更精确推理的场景）
	// "disabled" - 强制关闭思考（更快，适合翻译等简单任务）
	ThinkingMode string `mapstructure:"thinking_mode"`
	// PromptFile 系统提示词模板文件路径，默认 ~/.config/lsp-proxy/prompt.txt
	// 文件内容为 text/template 格式，可使用 {{.TargetLang}} 变量。
	// 文件不存在时自动创建默认提示词文件。
	PromptFile string `mapstructure:"prompt_file"`
}

// ProxyConfig 代理行为相关配置
type ProxyConfig struct {
	// TargetLang 目标翻译语言，默认 "zh-CN"
	TargetLang string `mapstructure:"target_lang"`
	// CacheSize 内存 LRU 缓存上限（MB），默认 30MB；超出此上限时 LRU 驱逐最久未使用条目
	CacheSize int `mapstructure:"cache_size"`
	// DictFile 磁盘词典文件路径，默认 ~/.local/share/lsp-proxy/dict.json
	// 词典作为二级持久化缓存：内存未命中时查磁盘，磁盘未命中才调用在线翻译
	DictFile string `mapstructure:"dict_file"`
	// DictMaxEntries 磁盘词典最大条目数，超出时按访问顺序 LRU 驱逐最久未访问的条目。
	// 默认 100000 条；0 表示不限制容量（词典无限增长）。
	DictMaxEntries int `mapstructure:"dict_max_entries"`
	// TranslationTimeout 翻译等待超时时间（毫秒）。
	// 0 表示无限等待直到翻译完成或出错；其他正值为最大等待毫秒数。
	// 超时后立即返回原文并在后台继续翻译以预热缓存。
	// 默认 600ms，对大多数在线 API 首次请求体验较好。
	TranslationTimeout int `mapstructure:"translation_timeout"`
	// GlossaryDir 专业术语词汇本目录路径，默认 ~/.local/share/lsp-proxy/glossary/
	// 目录下每个 .toml 文件对应一个词汇本：
	//   _global.toml    — 全局词汇本，对所有 LSP 生效
	//   <lsp>.toml      — LSP 专属词汇本，如 rust-analyzer.toml、clangd.toml
	// 查询优先级：LSP 专属 > 全局 > 缓存 > 在线翻译
	// 设为空字符串可禁用词汇本功能。
	GlossaryDir string `mapstructure:"glossary_dir"`
}

// LogConfig 日志相关配置
type LogConfig struct {
	// Level 日志级别，支持 "debug"、"info"、"warn"、"error"，默认 "info"
	Level string `mapstructure:"level"`
	// File 日志文件路径，默认 ~/.local/share/lsp-proxy/proxy.log
	File string `mapstructure:"file"`
}

// DefaultPath 返回默认配置文件路径 (~/.config/lsp-proxy/config.yaml)
func DefaultPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		// 无法获取家目录时降级使用相对路径
		return filepath.Join(".config", "lsp-proxy", "config.yaml")
	}
	return filepath.Join(homeDir, ".config", "lsp-proxy", "config.yaml")
}

// defaultLogFile 返回默认日志文件路径 (~/.local/share/lsp-proxy/proxy.log)
func defaultLogFile() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share", "lsp-proxy", "proxy.log")
	}
	return filepath.Join(homeDir, ".local", "share", "lsp-proxy", "proxy.log")
}

// DefaultPromptFile 返回默认提示词文件路径 (~/.config/lsp-proxy/prompt.txt)
func DefaultPromptFile() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".config", "lsp-proxy", "prompt.txt")
	}
	return filepath.Join(homeDir, ".config", "lsp-proxy", "prompt.txt")
}

// DefaultDictFile 返回默认磁盘词典文件路径 (~/.local/share/lsp-proxy/dict.json)
func DefaultDictFile() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share", "lsp-proxy", "dict.json")
	}
	return filepath.Join(homeDir, ".local", "share", "lsp-proxy", "dict.json")
}

// DefaultGlossaryDir 返回默认术语词汇本目录路径 (~/.local/share/lsp-proxy/glossary/)
func DefaultGlossaryDir() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".local", "share", "lsp-proxy", "glossary")
	}
	return filepath.Join(homeDir, ".local", "share", "lsp-proxy", "glossary")
}

// setDefaults 向 viper 实例注册所有默认值
func setDefaults(v *viper.Viper) {
	// 翻译引擎默认使用 Google 免费接口
	v.SetDefault("translate.engine", "google")
	v.SetDefault("translate.openai.base_url", "https://api.openai.com/v1")
	v.SetDefault("translate.openai.api_key", "")
	v.SetDefault("translate.openai.model", "gpt-4o-mini")
	v.SetDefault("translate.openai.thinking_mode", "auto")
	v.SetDefault("translate.openai.prompt_file", DefaultPromptFile())

	// 代理默认配置
	v.SetDefault("proxy.target_lang", "zh-CN")
	v.SetDefault("proxy.cache_size", 30) // 单位 MB
	v.SetDefault("proxy.dict_file", DefaultDictFile())
	v.SetDefault("proxy.dict_max_entries", 100000) // 磁盘词典最大条目数
	v.SetDefault("proxy.translation_timeout", 600) // 单位毫秒，0 表示无限等待
	v.SetDefault("proxy.glossary_dir", DefaultGlossaryDir())

	// 日志默认配置
	v.SetDefault("log.level", "info")
	v.SetDefault("log.file", defaultLogFile())
}

// Load 从指定路径加载配置文件。
// 若 path 为空，则使用 DefaultPath()。
// 若配置文件不存在，则使用全部默认值，不返回错误。
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultPath()
	}

	v := viper.New()
	v.SetConfigType("yaml")

	// 注册默认值
	setDefaults(v)

	// 设置配置文件路径
	v.SetConfigFile(path)

	// 尝试读取配置文件；文件不存在时忽略错误，使用默认值
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(*os.PathError); !ok {
			// 文件存在但解析失败，返回错误
			if !isNotFoundError(err) {
				return nil, fmt.Errorf("解析配置文件失败 [%s]: %w", path, err)
			}
		}
		// 文件不存在，继续使用默认值
	}

	cfg := &Config{}
	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("反序列化配置失败: %w", err)
	}

	// 兼容旧版配置迁移：旧版 cache_size 单位为"条目数"（典型值 1000），
	// 新版单位为 MB（合理范围 1–500）。超过 500 视为旧格式，重置为默认 30MB。
	if cfg.Proxy.CacheSize > 500 {
		cfg.Proxy.CacheSize = 30
	}

	return cfg, nil
}

// isNotFoundError 判断 viper 的错误是否属于"文件未找到"类型
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	// viper 在文件不存在时返回 ConfigFileNotFoundError 或 os.PathError
	if _, ok := err.(viper.ConfigFileNotFoundError); ok {
		return true
	}
	if _, ok := err.(*os.PathError); ok {
		return true
	}
	return false
}

// Save 将当前配置序列化为 YAML 并写入指定路径。
// 若目录不存在会自动创建。
func (c *Config) Save(path string) error {
	if path == "" {
		path = DefaultPath()
	}

	// 确保目标目录存在
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建配置目录失败 [%s]: %w", dir, err)
	}

	// 将结构体写入 viper 后再持久化
	v := viper.New()
	v.SetConfigType("yaml")
	v.SetConfigFile(path)

	setDefaults(v)

	// 将结构体字段逐一写入 viper（覆盖默认值）
	v.Set("translate.engine", c.Translate.Engine)
	v.Set("translate.openai.base_url", c.Translate.OpenAI.BaseURL)
	v.Set("translate.openai.api_key", c.Translate.OpenAI.APIKey)
	v.Set("translate.openai.model", c.Translate.OpenAI.Model)
	v.Set("translate.openai.thinking_mode", c.Translate.OpenAI.ThinkingMode)
	v.Set("translate.openai.prompt_file", c.Translate.OpenAI.PromptFile)
	v.Set("proxy.target_lang", c.Proxy.TargetLang)
	v.Set("proxy.cache_size", c.Proxy.CacheSize)
	v.Set("proxy.dict_file", c.Proxy.DictFile)
	v.Set("proxy.dict_max_entries", c.Proxy.DictMaxEntries)
	v.Set("proxy.translation_timeout", c.Proxy.TranslationTimeout)
	v.Set("proxy.glossary_dir", c.Proxy.GlossaryDir)
	v.Set("log.level", c.Log.Level)
	v.Set("log.file", c.Log.File)

	if err := v.WriteConfigAs(path); err != nil {
		return fmt.Errorf("写入配置文件失败 [%s]: %w", path, err)
	}

	return nil
}
