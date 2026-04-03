package glossary

import (
	"context"
	"log/slog"
)

// ─────────────────────────────────────────────────────────────────────────────
// GlossaryEngine — 术语词汇本 Engine 包装器
//
// 将 Glossary 插入翻译链路的最前端：
//
//	Translate(text) →
//	    1. Glossary.Lookup(text, lspName)  命中 → 直接返回译文，不调用底层引擎
//	    2. 未命中 → 透传给底层 Engine
//
// 这种设计使词汇本对 handler.go 完全透明，无需修改消息处理逻辑。
// ─────────────────────────────────────────────────────────────────────────────

// Translator 是翻译引擎的最小接口定义，与 translate.Engine 结构一致。
// 使用本地接口避免与 translate 包产生循环依赖。
type Translator interface {
	Translate(ctx context.Context, text, targetLang string) (string, error)
	Name() string
}

// GlossaryEngine 是带术语词汇本查询的翻译引擎包装器。
// 实现 [Translator]（即 translate.Engine）接口，可嵌入现有引擎链路。
type GlossaryEngine struct {
	base    Translator
	g       *Glossary
	lspName string
	logger  *slog.Logger
}

// NewGlossaryEngine 创建词汇本引擎包装器。
//
//   - base：底层翻译引擎（命中词汇本时不调用）
//   - g：Glossary 实例
//   - lspName：当前代理的 LSP 可执行文件名（如 "rust-analyzer"），用于专属词汇本查询
func NewGlossaryEngine(base Translator, g *Glossary, lspName string, logger *slog.Logger) *GlossaryEngine {
	return &GlossaryEngine{
		base:    base,
		g:       g,
		lspName: lspName,
		logger:  logger,
	}
}

// Translate 先在词汇本中查询，命中则直接返回译文；未命中则调用底层引擎。
func (e *GlossaryEngine) Translate(ctx context.Context, text, targetLang string) (string, error) {
	if v, ok := e.g.Lookup(text, e.lspName); ok {
		e.logger.Debug("词汇本命中",
			slog.String("lsp", e.lspName),
			slog.String("text", text),
			slog.String("translation", v),
		)
		return v, nil
	}
	return e.base.Translate(ctx, text, targetLang)
}

// Name 返回引擎名称（带词汇本标识）
func (e *GlossaryEngine) Name() string {
	return e.base.Name() + "(glossary)"
}

// Close 关闭词汇本监听，并关闭底层引擎（若实现了 io.Closer）。
func (e *GlossaryEngine) Close() error {
	var firstErr error
	if err := e.g.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if closer, ok := e.base.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
