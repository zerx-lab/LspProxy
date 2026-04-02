// Package lsp 提供 LSP（Language Server Protocol）协议的消息读写与处理。
// 本文件实现消息处理器，负责识别需要翻译的 LSP 消息并调用翻译引擎。
//
// # 翻译策略
//
// ## hover / completion / signatureHelp（请求-响应）
//
// LSP 协议规定每个请求只能收到一次响应，因此无法在翻译完成后"更新"已发出的响应。
// 采用"两阶段超时"策略：
//
//  1. 第一阶段：以 [cacheCheckTimeout]（50ms）为超时尝试快速路径翻译：
//     - 缓存命中（通常 < 1ms）→ 直接返回翻译结果，编辑器看到中文
//  2. 第二阶段：若快速路径未命中，继续等待直到 TranslationTimeout 到达：
//     - 在超时窗口内翻译完成 → 直接返回翻译结果（首次请求也能看到中文）
//     - 超过等待窗口仍未完成 → 立即返回原文，同时在后台 goroutine 继续翻译以预热缓存
//     - TranslationTimeout == 0 时无限等待直到翻译完成或出错
//
// ## publishDiagnostics（通知，可重复推送）
//
// 通知没有一对一的应答约束，可以随时主动推送：
//  1. 采用与响应消息相同的两阶段超时策略尝试同步返回翻译版本
//  2. 超过等待窗口则立即转发原始（英文）通知，后台 goroutine 完成翻译后通过 asyncPush 推送中文版本
//
// ## 关于 mouse-out 事件
//
// LSP 协议没有"hover 关闭"通知，编辑器自行管理 popup 生命周期。
// 当鼠标移到其他符号时，编辑器会发起新的 hover 请求（此时缓存通常已预热完毕）。
package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"sync"
	"time"

	"LspProxy/internal/markdown"
	"LspProxy/internal/translate"
)

// 确保 translateText 使用占位符法后，不再需要逐段处理，strings 仍被其他地方使用

// cacheCheckTimeout 是第一阶段快速缓存路径的超时阈值。
// 若翻译能在此时间内完成（通常意味着内存/磁盘缓存命中），则直接返回翻译结果。
// 未命中时进入第二阶段，继续等待直到 Handler.translationTimeout 到达。
const cacheCheckTimeout = 50 * time.Millisecond

// asyncDiagTimeout 是后台异步翻译诊断的最长等待时间。
const asyncDiagTimeout = 30 * time.Second

// ─────────────────────────────────────────────────────────────────────────────
// pending 请求信息（用于响应时反查请求上下文）
// ─────────────────────────────────────────────────────────────────────────────

// pendingInfo 保存一个 LSP 请求的上下文信息，用于在响应时提供更丰富的日志。
type pendingInfo struct {
	Method string // LSP 方法名，如 "textDocument/hover"
	URI    string // 文件 URI，如 "file:///home/user/main.rs"
	Line   int    // 触发位置行号（0-based）
	Char   int    // 触发位置列号（0-based）
}

// textDocPositionParams 用于解析 hover / completion / signatureHelp 的请求参数。
// 只提取需要的字段，其余字段忽略。
type textDocPositionParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	Position struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"position"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Handler
// ─────────────────────────────────────────────────────────────────────────────

// Handler 处理 LSP 消息，将需要翻译的字段翻译为目标语言。
// 它维护一个 pending 请求映射，以便在收到响应时知道对应的请求方法和触发位置。
type Handler struct {
	// engine 翻译引擎实例
	engine translate.Engine
	// targetLang 目标语言代码，例如 "zh-CN"
	targetLang string
	// logger 结构化日志记录器
	logger *slog.Logger
	// pending 记录已发出但尚未收到响应的请求，key 为 ID 字符串
	pending map[string]pendingInfo
	// mu 保护 pending 并发访问
	mu sync.Mutex
	// translationTimeout 第二阶段等待窗口。
	// 0 表示无限等待直到翻译完成或出错；正值为最大等待时长。
	translationTimeout time.Duration
}

// NewHandler 创建并返回一个新的 Handler 实例。
// translationTimeoutMs 为翻译等待超时毫秒数，0 表示无限等待。
func NewHandler(engine translate.Engine, targetLang string, logger *slog.Logger, translationTimeoutMs int) *Handler {
	var timeout time.Duration
	if translationTimeoutMs > 0 {
		timeout = time.Duration(translationTimeoutMs) * time.Millisecond
	}
	// translationTimeoutMs == 0 时 timeout 保持零值，表示无限等待
	return &Handler{
		engine:             engine,
		targetLang:         targetLang,
		logger:             logger,
		pending:            make(map[string]pendingInfo),
		translationTimeout: timeout,
	}
}

// idKey 将 json.RawMessage 形式的 ID 转换为可用作 map key 的字符串。
func idKey(id json.RawMessage) string {
	return string(id)
}

// TrackRequest 记录一个客户端请求，用于响应到达时反查对应的方法名和触发位置。
// 对于通知（无 ID）不做任何处理。
func (h *Handler) TrackRequest(msg *BaseMessage) {
	if !msg.IsRequest() {
		return
	}

	info := pendingInfo{Method: msg.Method}

	// 对于带有文档位置参数的方法，解析 URI 和光标位置用于日志
	switch msg.Method {
	case "textDocument/hover",
		"textDocument/completion",
		"textDocument/signatureHelp",
		"textDocument/definition",
		"textDocument/references":
		var p textDocPositionParams
		if err := json.Unmarshal(msg.Params, &p); err == nil {
			info.URI = p.TextDocument.URI
			info.Line = p.Position.Line
			info.Char = p.Position.Character
		}

		h.logger.Info("LSP 请求触发",
			slog.String("method", shortMethod(msg.Method)),
			slog.String("file", uriBasename(info.URI)),
			slog.Int("line", info.Line+1), // 转为 1-based 方便阅读
			slog.Int("char", info.Char+1),
		)
	}

	key := idKey(msg.ID)
	h.mu.Lock()
	h.pending[key] = info
	h.mu.Unlock()
}

// popInfo 从 pending 中取出并删除 ID 对应的请求信息。
// 若不存在则返回零值。
func (h *Handler) popInfo(id json.RawMessage) pendingInfo {
	key := idKey(id)
	h.mu.Lock()
	defer h.mu.Unlock()
	info, ok := h.pending[key]
	if ok {
		delete(h.pending, key)
	}
	return info
}

// ─────────────────────────────────────────────────────────────────────────────
// ProcessServerMessage — 主入口
// ─────────────────────────────────────────────────────────────────────────────

// ProcessServerMessage 处理服务端（LSP）发来的原始消息字节。
//
// asyncPush 是一个可选回调，用于在翻译完成后主动向客户端推送新消息（仅用于通知类消息）。
// 调用方须保证 asyncPush 是并发安全的（通常用 mutex 保护写操作）。
//
// 返回值是应立即发往客户端的消息：
//   - 对于响应消息（hover 等）：缓存命中则返回已翻译内容，否则返回原文并后台预热缓存
//   - 对于诊断通知：立即返回原文，翻译完成后通过 asyncPush 推送翻译版本
//   - 其他消息：原样透传
func (h *Handler) ProcessServerMessage(
	ctx context.Context,
	raw []byte,
	asyncPush func([]byte),
) ([]byte, error) {
	var msg BaseMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		h.logger.Warn("无法解析服务端消息，原样透传", slog.String("error", err.Error()))
		return raw, nil
	}

	switch {
	case msg.IsResponse():
		return h.processResponse(ctx, &msg, raw)
	case msg.IsNotification():
		return h.processNotification(ctx, &msg, raw, asyncPush)
	default:
		return raw, nil
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 响应消息处理（hover / completion / signatureHelp）
// 策略：快速缓存路径 + 后台预热
// ─────────────────────────────────────────────────────────────────────────────

// processResponse 处理响应消息。
// 使用 [cacheCheckTimeout] 超时尝试翻译：
//   - 缓存命中（极快）→ 直接返回翻译结果
//   - 超时（首次请求）→ 返回原文 + 后台goroutine预热缓存
func (h *Handler) processResponse(ctx context.Context, msg *BaseMessage, raw []byte) ([]byte, error) {
	if msg.Error != nil {
		return raw, nil
	}

	info := h.popInfo(msg.ID)
	if info.Method == "" {
		return raw, nil
	}

	switch info.Method {
	case "textDocument/hover":
		return h.handleResponseWithFastPath(ctx, msg, raw, info,
			func(fastCtx context.Context) (json.RawMessage, error) {
				return h.translateHover(fastCtx, msg.Result)
			},
			func(bgCtx context.Context) {
				h.translateHover(bgCtx, msg.Result) //nolint:errcheck // 仅预热缓存
			},
		)

	case "textDocument/completion":
		return h.handleResponseWithFastPath(ctx, msg, raw, info,
			func(fastCtx context.Context) (json.RawMessage, error) {
				return h.translateCompletion(fastCtx, msg.Result)
			},
			func(bgCtx context.Context) {
				h.translateCompletion(bgCtx, msg.Result) //nolint:errcheck
			},
		)

	case "textDocument/signatureHelp":
		return h.handleResponseWithFastPath(ctx, msg, raw, info,
			func(fastCtx context.Context) (json.RawMessage, error) {
				return h.translateSignatureHelp(fastCtx, msg.Result)
			},
			func(bgCtx context.Context) {
				h.translateSignatureHelp(bgCtx, msg.Result) //nolint:errcheck
			},
		)

	default:
		return raw, nil
	}
}

// translateResult 封装单次翻译调用的结果。
type translateResult struct {
	result json.RawMessage
	err    error
}

// handleResponseWithFastPath 对响应消息执行两阶段翻译，并记录详细日志。
//
// 核心设计：翻译只发起一次（在独立 goroutine 中），主路径通过 channel 等待。
//
// 两阶段等待策略：
//  1. 第一阶段（[cacheCheckTimeout] 50ms）：主要针对缓存命中场景（< 1ms），
//     若在此窗口内翻译完成则立即返回译文。
//  2. 第二阶段（translationTimeout - elapsed）：给在线翻译继续等待的窗口；
//     translationTimeout == 0 表示无限等待直到翻译完成或出错；
//     超过总超时仍未完成则返回原文，翻译继续在后台运行并写入缓存（供下次命中）。
//
// 参数：
//   - info:        原始请求的上下文（方法名、文件、位置），用于日志
//   - translateFn: 翻译函数，接受一个 context（仅用于取消，不用于超时控制）
//   - warmFn:      超时返回原文后，在后台 goroutine 中调用以确保缓存被填充
func (h *Handler) handleResponseWithFastPath(
	ctx context.Context,
	msg *BaseMessage,
	raw []byte,
	info pendingInfo,
	translateFn func(context.Context) (json.RawMessage, error),
	warmFn func(context.Context),
) ([]byte, error) {
	start := time.Now()

	// 启动单次翻译 goroutine，结果写入带缓冲的 channel（避免 goroutine 泄漏）
	ch := make(chan translateResult, 1)
	// bgCtx 不受超时控制：即使主路径超时返回原文，翻译仍继续以填充缓存
	bgCtx, bgCancel := context.WithTimeout(context.Background(), asyncDiagTimeout)
	go func() {
		defer bgCancel()
		r, err := translateFn(bgCtx)
		ch <- translateResult{result: r, err: err}
	}()

	// ── 第一阶段：cacheCheckTimeout（50ms）等待缓存命中 ──
	select {
	case res := <-ch:
		elapsed := time.Since(start)
		if res.err == nil {
			return h.buildTranslatedResponse(ctx, msg, raw, info, res.result, elapsed, "缓存命中")
		}
		// 翻译报错，直接返回原文（不进入第二阶段）
		h.logger.Warn("翻译失败，返回原文",
			slog.String("method", shortMethod(info.Method)),
			slog.String("file", uriBasename(info.URI)),
			slog.Int("line", info.Line+1),
			slog.String("error", res.err.Error()),
			slog.String("elapsed", fmtDuration(elapsed)),
		)
		return raw, nil

	case <-time.After(cacheCheckTimeout):
		// 第一阶段超时，进入第二阶段
	}

	// ── 第二阶段：继续等待直到 translationTimeout ──
	elapsed := time.Since(start)
	var secondWait <-chan time.Time
	if h.translationTimeout > 0 {
		remaining := h.translationTimeout - elapsed
		if remaining > 0 {
			secondWait = time.After(remaining)
		}
		// remaining <= 0：已超出总超时，secondWait 为 nil（select 该 case 永不触发）
	}
	// translationTimeout == 0：secondWait 为 nil，select 只等 ch，即无限等待

	select {
	case res := <-ch:
		elapsed = time.Since(start)
		if res.err == nil {
			return h.buildTranslatedResponse(ctx, msg, raw, info, res.result, elapsed, "等待完成")
		}
		h.logger.Warn("翻译失败，返回原文",
			slog.String("method", shortMethod(info.Method)),
			slog.String("file", uriBasename(info.URI)),
			slog.Int("line", info.Line+1),
			slog.String("error", res.err.Error()),
			slog.String("elapsed", fmtDuration(elapsed)),
		)
		return raw, nil

	case <-secondWait:
		// 超出总超时：返回原文；翻译 goroutine 仍在运行，会在完成后写入缓存
		elapsed = time.Since(start)
		h.logger.Info("翻译超时，返回原文（后台预热中）",
			slog.String("method", shortMethod(info.Method)),
			slog.String("file", uriBasename(info.URI)),
			slog.Int("line", info.Line+1),
			slog.String("elapsed", fmtDuration(elapsed)),
			slog.String("hint", "缓存预热后再次触发将直接返回中文"),
		)
		// 无需额外启动 warmFn goroutine：上面的翻译 goroutine 本身就在继续运行
		// 此处启动 warmFn 仅作为安全兜底（防止翻译 goroutine 意外退出）
		go func() {
			// 等待主翻译 goroutine 结束（channel 有缓冲，不会阻塞它）
			select {
			case res := <-ch:
				if res.err == nil {
					// 主翻译 goroutine 已完成并写入缓存，不需要再次调用 warmFn
					h.logger.Info("后台缓存预热完成（翻译 goroutine）",
						slog.String("method", shortMethod(info.Method)),
						slog.String("file", uriBasename(info.URI)),
						slog.Int("line", info.Line+1),
						slog.String("elapsed", fmtDuration(time.Since(start))),
					)
					return
				}
			default:
			}
			// 主翻译 goroutine 失败或尚未完成（不太可能），用 warmFn 兜底
			warmStart := time.Now()
			warmCtx, warmCancel := context.WithTimeout(context.Background(), asyncDiagTimeout)
			defer warmCancel()
			warmFn(warmCtx)
			h.logger.Info("后台缓存预热完成（warmFn 兜底）",
				slog.String("method", shortMethod(info.Method)),
				slog.String("file", uriBasename(info.URI)),
				slog.Int("line", info.Line+1),
				slog.String("elapsed", fmtDuration(time.Since(warmStart))),
			)
		}()
		return raw, nil
	}
}

// buildTranslatedResponse 将翻译结果写入消息并序列化，同时记录日志。
func (h *Handler) buildTranslatedResponse(
	ctx context.Context,
	msg *BaseMessage,
	raw []byte,
	info pendingInfo,
	newResult json.RawMessage,
	elapsed time.Duration,
	hitKind string,
) ([]byte, error) {
	h.logger.Info("翻译完成（"+hitKind+"）",
		slog.String("method", shortMethod(info.Method)),
		slog.String("file", uriBasename(info.URI)),
		slog.Int("line", info.Line+1),
		slog.String("elapsed", fmtDuration(elapsed)),
	)

	if h.logger.Enabled(ctx, slog.LevelDebug) {
		origPreview := previewJSON(msg.Result)
		transPreview := previewJSON(newResult)
		h.logger.Debug("翻译响应内容",
			slog.String("method", shortMethod(info.Method)),
			slog.String("id", string(msg.ID)),
			slog.String("original", origPreview),
			slog.String("translated", transPreview),
			slog.Bool("changed", origPreview != transPreview),
		)
	}

	msg.Result = newResult
	out, err := json.Marshal(msg)
	if err != nil {
		return raw, fmt.Errorf("序列化翻译响应失败: %w", err)
	}
	return out, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 通知消息处理（publishDiagnostics）
// 策略：立即返回原文 + asyncPush 推送翻译版本
// ─────────────────────────────────────────────────────────────────────────────

// processNotification 处理通知消息。
// 对于 publishDiagnostics：立即返回原始通知，后台翻译完成后通过 asyncPush 推送翻译版本。
func (h *Handler) processNotification(
	ctx context.Context,
	msg *BaseMessage,
	raw []byte,
	asyncPush func([]byte),
) ([]byte, error) {
	switch msg.Method {
	case "textDocument/publishDiagnostics":
		return h.handleDiagnosticsAsync(ctx, msg, raw, asyncPush)
	default:
		return raw, nil
	}
}

// handleDiagnosticsAsync 实现诊断消息的"原文先行 + 异步翻译推送"策略。
//
// 流程：
//  1. 立即返回原始（英文）诊断通知 → 编辑器零延迟显示诊断
//  2. 若 asyncPush 非 nil，则启动后台 goroutine 翻译诊断消息
//  3. 翻译完成后调用 asyncPush 发送翻译版本 → 编辑器诊断面板更新为中文
//
// 若 asyncPush 为 nil（调用方不支持异步推送），则退回到同步翻译。
func (h *Handler) handleDiagnosticsAsync(
	ctx context.Context,
	msg *BaseMessage,
	raw []byte,
	asyncPush func([]byte),
) ([]byte, error) {
	// 解析诊断参数，用于日志（不影响主流程）
	var diagParams PublishDiagnosticsParams
	_ = json.Unmarshal(msg.Params, &diagParams)
	fileName := uriBasename(diagParams.URI)
	diagCount := len(diagParams.Diagnostics)

	if diagCount == 0 {
		// 无诊断（文件无错误），直接透传，不需要翻译
		h.logger.Debug("诊断通知（无诊断条目），透传",
			slog.String("file", fileName),
		)
		return raw, nil
	}

	h.logger.Info("收到诊断通知",
		slog.String("file", fileName),
		slog.Int("count", diagCount),
		slog.String("uri", diagParams.URI),
	)

	// Debug 级别：打印前几条诊断原文预览
	if h.logger.Enabled(ctx, slog.LevelDebug) {
		for i, d := range diagParams.Diagnostics {
			if i >= 3 {
				h.logger.Debug("  ... 更多诊断条目已省略",
					slog.Int("remaining", diagCount-3),
				)
				break
			}
			h.logger.Debug("  诊断条目",
				slog.Int("index", i+1),
				slog.Int("severity", d.Severity),
				slog.String("message", truncate(d.Message, 120)),
			)
		}
	}

	if asyncPush == nil {
		// 降级：同步翻译
		h.logger.Debug("asyncPush 为 nil，退回同步翻译诊断",
			slog.String("file", fileName),
		)
		start := time.Now()
		newParams, err := h.translateDiagnostics(ctx, msg.Params)
		if err != nil {
			h.logger.Warn("同步翻译诊断失败，返回原文",
				slog.String("file", fileName),
				slog.String("error", err.Error()),
			)
			return raw, nil
		}
		h.logger.Info("诊断同步翻译完成",
			slog.String("file", fileName),
			slog.Int("count", diagCount),
			slog.String("elapsed", fmtDuration(time.Since(start))),
		)
		msg.Params = newParams
		out, err := json.Marshal(msg)
		if err != nil {
			return raw, fmt.Errorf("序列化诊断消息失败: %w", err)
		}
		return out, nil
	}

	// 深拷贝 params 和 msg（避免 goroutine 与主路径共享底层数据）
	paramsCopy := make(json.RawMessage, len(msg.Params))
	copy(paramsCopy, msg.Params)
	msgCopy := *msg
	msgCopy.Params = paramsCopy

	// 启动单次翻译 goroutine，结果写入带缓冲 channel（不因主路径超时而泄漏）
	type diagResult struct {
		params json.RawMessage
		err    error
	}
	diagCh := make(chan diagResult, 1)
	bgCtx, bgCancel := context.WithTimeout(context.Background(), asyncDiagTimeout)
	go func() {
		defer bgCancel()
		newParams, err := h.translateDiagnostics(bgCtx, msgCopy.Params)
		diagCh <- diagResult{params: newParams, err: err}
	}()

	// 将翻译结果序列化并通过 asyncPush 推送的辅助闭包
	pushTranslated := func(newParams json.RawMessage, elapsed time.Duration, hitKind string) ([]byte, error) {
		h.logger.Info("诊断翻译完成（"+hitKind+"）",
			slog.String("file", fileName),
			slog.Int("count", diagCount),
			slog.String("elapsed", fmtDuration(elapsed)),
		)
		if h.logger.Enabled(bgCtx, slog.LevelDebug) {
			var translated PublishDiagnosticsParams
			if err := json.Unmarshal(newParams, &translated); err == nil {
				for i, d := range translated.Diagnostics {
					if i >= 3 {
						break
					}
					h.logger.Debug("  诊断翻译结果",
						slog.Int("index", i+1),
						slog.String("translated", truncate(d.Message, 120)),
					)
				}
			}
		}
		msgCopy.Params = newParams
		out, err := json.Marshal(&msgCopy)
		if err != nil {
			return nil, fmt.Errorf("序列化诊断消息失败: %w", err)
		}
		return out, nil
	}

	start := time.Now()

	// ── 第一阶段：cacheCheckTimeout（50ms）等待缓存命中 ──
	select {
	case res := <-diagCh:
		elapsed := time.Since(start)
		if res.err != nil {
			h.logger.Warn("诊断翻译失败，返回原文",
				slog.String("file", fileName),
				slog.String("error", res.err.Error()),
				slog.String("elapsed", fmtDuration(elapsed)),
			)
			return raw, nil
		}
		out, err := pushTranslated(res.params, elapsed, "缓存命中")
		if err != nil {
			return raw, err
		}
		return out, nil

	case <-time.After(cacheCheckTimeout):
		// 进入第二阶段
	}

	// ── 第二阶段：继续等待直到 translationTimeout ──
	elapsed := time.Since(start)
	var secondWait <-chan time.Time
	if h.translationTimeout > 0 {
		remaining := h.translationTimeout - elapsed
		if remaining > 0 {
			secondWait = time.After(remaining)
		}
	}
	// translationTimeout == 0：secondWait 为 nil，即无限等待

	select {
	case res := <-diagCh:
		elapsed = time.Since(start)
		if res.err != nil {
			h.logger.Warn("诊断翻译失败，返回原文",
				slog.String("file", fileName),
				slog.String("error", res.err.Error()),
				slog.String("elapsed", fmtDuration(elapsed)),
			)
			return raw, nil
		}
		out, err := pushTranslated(res.params, elapsed, "等待完成")
		if err != nil {
			return raw, err
		}
		return out, nil

	case <-secondWait:
		// 超出总超时：立即返回原文，翻译 goroutine 完成后通过 asyncPush 推送中文版本
		elapsed = time.Since(start)
		h.logger.Info("诊断翻译后台进行中，先推送原文",
			slog.String("file", fileName),
			slog.Int("count", diagCount),
			slog.String("elapsed", fmtDuration(elapsed)),
		)

		go func() {
			bgStart := time.Now()
			res := <-diagCh
			if res.err != nil {
				h.logger.Warn("后台异步翻译诊断失败",
					slog.String("file", fileName),
					slog.Int("count", diagCount),
					slog.String("error", res.err.Error()),
					slog.String("elapsed", fmtDuration(time.Since(bgStart))),
				)
				return
			}
			out, err := pushTranslated(res.params, time.Since(bgStart), "后台完成")
			if err != nil {
				h.logger.Warn("序列化异步翻译诊断失败",
					slog.String("file", fileName),
					slog.String("error", err.Error()),
				)
				return
			}
			asyncPush(out)
		}()

		return raw, nil
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 各消息类型的翻译实现
// ─────────────────────────────────────────────────────────────────────────────

// translateHover 翻译 textDocument/hover 的响应结果。
// HoverResult.Contents 可能是 string、MarkupContent 或 []MarkedString。
func (h *Handler) translateHover(ctx context.Context, result json.RawMessage) (json.RawMessage, error) {
	if len(result) == 0 || string(result) == "null" {
		return result, nil
	}

	var hover HoverResult
	if err := json.Unmarshal(result, &hover); err != nil {
		return result, fmt.Errorf("解析 HoverResult 失败: %w", err)
	}

	translatedContents, err := h.translateMarkupContent(ctx, hover.Contents)
	if err != nil {
		return result, err
	}

	hover.Contents = translatedContents
	out, err := json.Marshal(hover)
	if err != nil {
		return result, fmt.Errorf("序列化翻译后的 HoverResult 失败: %w", err)
	}
	return out, nil
}

// translateCompletion 翻译 textDocument/completion 的响应结果。
// 结果可能是 CompletionList 或 []CompletionItem。
func (h *Handler) translateCompletion(ctx context.Context, result json.RawMessage) (json.RawMessage, error) {
	if len(result) == 0 || string(result) == "null" {
		return result, nil
	}

	// 先尝试作为 CompletionList 解析
	var list CompletionList
	if err := json.Unmarshal(result, &list); err == nil && list.Items != nil {
		translated, total := h.translateCompletionItems(ctx, list.Items)
		h.logger.Debug("补全条目文档翻译",
			slog.Int("total", len(list.Items)),
			slog.Int("translated", translated),
			slog.Int("skipped", total-translated),
		)
		out, err := json.Marshal(list)
		if err != nil {
			return result, fmt.Errorf("序列化翻译后的 CompletionList 失败: %w", err)
		}
		return out, nil
	}

	// 再尝试作为 []CompletionItem 解析
	var items []CompletionItem
	if err := json.Unmarshal(result, &items); err != nil {
		return result, nil
	}
	translated, total := h.translateCompletionItems(ctx, items)
	h.logger.Debug("补全条目文档翻译",
		slog.Int("total", len(items)),
		slog.Int("translated", translated),
		slog.Int("skipped", total-translated),
	)
	out, err := json.Marshal(items)
	if err != nil {
		return result, fmt.Errorf("序列化翻译后的 CompletionItems 失败: %w", err)
	}
	return out, nil
}

// translateCompletionItems 原地翻译补全条目列表中的文档字段。
// 返回（成功翻译数, 有文档的总数）。
func (h *Handler) translateCompletionItems(ctx context.Context, items []CompletionItem) (translated, total int) {
	for i := range items {
		if len(items[i].Documentation) == 0 {
			continue
		}
		total++
		result, err := h.translateMarkupContent(ctx, items[i].Documentation)
		if err != nil {
			h.logger.Warn("翻译补全条目文档失败",
				slog.String("label", items[i].Label),
				slog.String("error", err.Error()),
			)
			continue
		}
		items[i].Documentation = result
		translated++
	}
	return translated, total
}

// translateSignatureHelp 翻译 textDocument/signatureHelp 的响应结果。
func (h *Handler) translateSignatureHelp(ctx context.Context, result json.RawMessage) (json.RawMessage, error) {
	if len(result) == 0 || string(result) == "null" {
		return result, nil
	}

	var sigHelp SignatureHelp
	if err := json.Unmarshal(result, &sigHelp); err != nil {
		return result, fmt.Errorf("解析 SignatureHelp 失败: %w", err)
	}

	translated := 0
	for i := range sigHelp.Signatures {
		if len(sigHelp.Signatures[i].Documentation) == 0 {
			continue
		}
		result, err := h.translateMarkupContent(ctx, sigHelp.Signatures[i].Documentation)
		if err != nil {
			h.logger.Warn("翻译签名文档失败",
				slog.String("label", sigHelp.Signatures[i].Label),
				slog.String("error", err.Error()),
			)
			continue
		}
		sigHelp.Signatures[i].Documentation = result
		translated++
	}

	h.logger.Debug("签名帮助翻译",
		slog.Int("signatures", len(sigHelp.Signatures)),
		slog.Int("translated", translated),
	)

	out, err := json.Marshal(sigHelp)
	if err != nil {
		return result, fmt.Errorf("序列化翻译后的 SignatureHelp 失败: %w", err)
	}
	return out, nil
}

// translateDiagnostics 翻译 textDocument/publishDiagnostics 的参数。
func (h *Handler) translateDiagnostics(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	if len(params) == 0 || string(params) == "null" {
		return params, nil
	}

	var p PublishDiagnosticsParams
	if err := json.Unmarshal(params, &p); err != nil {
		return params, fmt.Errorf("解析 PublishDiagnosticsParams 失败: %w", err)
	}

	for i := range p.Diagnostics {
		if p.Diagnostics[i].Message == "" {
			continue
		}

		// 检查 context 是否已取消（避免超时后继续无效翻译）
		if ctx.Err() != nil {
			return params, ctx.Err()
		}

		translated, err := h.translateText(ctx, p.Diagnostics[i].Message)
		if err != nil {
			h.logger.Warn("翻译诊断消息失败",
				slog.String("message", truncate(p.Diagnostics[i].Message, 80)),
				slog.String("error", err.Error()),
			)
			continue
		}
		p.Diagnostics[i].Message = translated
	}

	out, err := json.Marshal(p)
	if err != nil {
		return params, fmt.Errorf("序列化翻译后的 PublishDiagnosticsParams 失败: %w", err)
	}
	return out, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 底层翻译辅助函数
// ─────────────────────────────────────────────────────────────────────────────

// translateMarkupContent 翻译 MarkupContent 或纯字符串形式的文档内容。
//
// 支持以下三种格式：
//  1. JSON 字符串（"..."）→ 直接翻译文本
//  2. MarkupContent（{"kind":"...","value":"..."}）→ 翻译 value 字段
//  3. 其他格式 → 原样返回
func (h *Handler) translateMarkupContent(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return raw, nil
	}

	// 情况1：纯字符串
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		original := str
		translated, err := h.translateText(ctx, str)
		if err != nil {
			return raw, err
		}
		h.logger.Debug("文档字符串翻译",
			slog.String("original", truncate(original, 80)),
			slog.String("translated", truncate(translated, 80)),
		)
		out, err := json.Marshal(translated)
		if err != nil {
			return raw, fmt.Errorf("序列化翻译后的字符串失败: %w", err)
		}
		return out, nil
	}

	// 情况2：MarkupContent 对象
	var mc MarkupContent
	if err := json.Unmarshal(raw, &mc); err == nil && mc.Kind != "" {
		original := mc.Value
		translated, err := h.translateText(ctx, mc.Value)
		if err != nil {
			return raw, err
		}
		h.logger.Debug("MarkupContent 翻译",
			slog.String("kind", mc.Kind),
			slog.String("original", truncate(original, 80)),
			slog.String("translated", truncate(translated, 80)),
		)
		mc.Value = translated
		out, err := json.Marshal(mc)
		if err != nil {
			return raw, fmt.Errorf("序列化翻译后的 MarkupContent 失败: %w", err)
		}
		return out, nil
	}

	// 情况3：无法识别的格式，原样返回
	return raw, nil
}

// translateText 翻译一段文本。
//
// 采用"占位符替换法"（Placeholder Substitution）：
//  1. 调用 [markdown.Protect] 将代码块替换为编号占位符（$CODE_N$），得到纯文本
//  2. 将带占位符的完整文本作为一个整体发送给翻译引擎（保留上下文，翻译质量更高）
//  3. 调用 [markdown.Restore] 将占位符还原为原始代码块
//
// 相比旧的"分段翻译"方案，此方案能正确处理代码块与文字混排在同一行的情况，
// 避免翻译结果中代码块与文字发生错位。
func (h *Handler) translateText(ctx context.Context, text string) (string, error) {
	if text == "" {
		return text, nil
	}

	// 空白文本无需翻译
	if strings.TrimSpace(text) == "" {
		return text, nil
	}

	// 检查 context 是否已取消
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	// 第一步：用占位符保护代码块，得到可安全翻译的纯文本
	masked, codes := markdown.Protect(text)

	h.logger.Debug("占位符保护完成",
		slog.Int("code_blocks", len(codes)),
		slog.String("masked_preview", truncate(masked, 80)),
	)

	// 若掩码后文本全为空白，说明原文只有代码块，直接返回原文
	if strings.TrimSpace(masked) == "" {
		return text, nil
	}

	// 第二步：将带占位符的整体文本发送给翻译引擎
	translated, err := h.engine.Translate(ctx, masked, h.targetLang)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		h.logger.Warn("翻译失败，返回原文",
			slog.String("error", err.Error()),
			slog.String("masked", truncate(masked, 80)),
		)
		return text, err
	}

	// 第三步：还原占位符为原始代码块
	result := markdown.Restore(translated, codes)

	h.logger.Debug("占位符还原完成",
		slog.String("translated", truncate(translated, 80)),
		slog.String("result", truncate(result, 80)),
	)

	return result, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 工具函数
// ─────────────────────────────────────────────────────────────────────────────

// uriBasename 从 LSP 文件 URI 中提取文件名（最后一段）。
// 例如："file:///home/user/project/src/main.rs" → "main.rs"
func uriBasename(uri string) string {
	if uri == "" {
		return "(unknown)"
	}
	// 去掉 file:// 前缀后取 path.Base
	u := strings.TrimPrefix(uri, "file://")
	base := path.Base(u)
	if base == "" || base == "." || base == "/" {
		return uri
	}
	return base
}

// shortMethod 将 LSP 方法名缩短为更易读的形式。
// 例如："textDocument/hover" → "hover"
func shortMethod(method string) string {
	if idx := strings.LastIndex(method, "/"); idx >= 0 {
		return method[idx+1:]
	}
	return method
}

// truncate 将字符串截断到最多 n 个字符，超出部分用 "…" 替代。
func truncate(s string, n int) string {
	runes := []rune(s)
	// 去掉换行，避免日志换行
	clean := strings.ReplaceAll(s, "\n", " ")
	runes = []rune(clean)
	if len(runes) <= n {
		return clean
	}
	return string(runes[:n]) + "…"
}

// fmtDuration 将 time.Duration 格式化为简短易读的字符串。
// 例如：0.3ms、52ms、1.2s
func fmtDuration(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return fmt.Sprintf("%.2fµs", float64(d.Microseconds()))
	case d < time.Second:
		return fmt.Sprintf("%.1fms", float64(d.Microseconds())/1000.0)
	default:
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
}

// previewJSON 从 json.RawMessage 中提取可读预览文本（最多 100 字符）。
// 尝试解析为字符串或 MarkupContent，否则直接截断 JSON。
func previewJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	// 尝试作为字符串
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return truncate(s, 100)
	}
	// 尝试作为 MarkupContent
	var mc MarkupContent
	if err := json.Unmarshal(raw, &mc); err == nil && mc.Value != "" {
		return truncate(mc.Value, 100)
	}
	// 直接截断 JSON
	return truncate(string(raw), 100)
}
