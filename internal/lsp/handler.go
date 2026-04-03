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
// ## textDocument/diagnostic（拉取式诊断，LSP 3.17+）
//
// 编辑器主动请求、服务端以响应形式返回。由于每个请求只有一次响应机会，
// 超时后仅返回原文是不够的——编辑器不会再次主动拉取，后台预热的翻译结果永远无法展示。
//
// 解决方案：翻译完成后通过 asyncPush 向编辑器发送 workspace/diagnostic/refresh 请求，
// 让编辑器重新拉取所有文档的诊断。此时缓存已命中，新的拉取将在 50ms 内返回中文结果。
// 代理发出的 refresh 请求使用负数 ID（-1, -2, …），其响应在 ProcessServerMessage 中
// 被 refreshIDs 集合识别后静默丢弃，不进入 pending 映射。
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

	"github.com/zerx-lab/LspProxy/internal/config"
	"github.com/zerx-lab/LspProxy/internal/markdown"
	"github.com/zerx-lab/LspProxy/internal/translate"
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
	// displayMode 翻译展示模式，控制译文如何与原文组合
	displayMode config.DisplayMode
	// logger 结构化日志记录器
	logger *slog.Logger
	// pending 记录已发出但尚未收到响应的请求，key 为 ID 字符串
	pending map[string]pendingInfo
	// mu 保护 pending 并发访问
	mu sync.Mutex
	// translationTimeout 第二阶段等待窗口。
	// 0 表示无限等待直到翻译完成或出错；正值为最大等待时长。
	translationTimeout time.Duration

	// ── 拉取式诊断刷新（workspace/diagnostic/refresh）──────────────────
	// refreshID 是发往编辑器的 workspace/diagnostic/refresh 请求的自增 ID。
	// 使用负数区间（-1, -2, -3…）避免与 LSP 服务端的请求 ID 冲突。
	refreshID int64
	// refreshIDs 记录已发出但尚未收到响应的 refresh 请求 ID 集合。
	// 编辑器对这些请求的响应会被静默丢弃，不进入 pending 映射。
	refreshIDs map[string]struct{}
}

// NewHandler 创建并返回一个新的 Handler 实例。
// translationTimeoutMs 为翻译等待超时毫秒数，0 表示无限等待。
// displayMode 控制译文展示方式，空值时默认为 translation_only。
func NewHandler(engine translate.Engine, targetLang string, logger *slog.Logger, translationTimeoutMs int, displayMode config.DisplayMode) *Handler {
	var timeout time.Duration
	if translationTimeoutMs > 0 {
		timeout = time.Duration(translationTimeoutMs) * time.Millisecond
	}
	if displayMode == "" {
		displayMode = config.DisplayTranslationOnly
	}
	// translationTimeoutMs == 0 时 timeout 保持零值，表示无限等待
	return &Handler{
		engine:             engine,
		targetLang:         targetLang,
		displayMode:        displayMode,
		logger:             logger,
		pending:            make(map[string]pendingInfo),
		translationTimeout: timeout,
		refreshID:          0,
		refreshIDs:         make(map[string]struct{}),
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

	case "textDocument/diagnostic":
		// 拉取式诊断请求只有 URI，没有光标位置
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		if err := json.Unmarshal(msg.Params, &p); err == nil {
			info.URI = p.TextDocument.URI
		}
		h.logger.Info("LSP 请求触发",
			slog.String("method", shortMethod(msg.Method)),
			slog.String("file", uriBasename(info.URI)),
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

// DrainPending 取出并清空所有尚未收到响应的 in-flight 请求 ID。
// 用于 LSP 子进程异常退出后，向编辑器批量发送 RequestFailed 错误响应，
// 避免编辑器侧的请求永久挂起。
func (h *Handler) DrainPending() []json.RawMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.pending) == 0 {
		return nil
	}
	ids := make([]json.RawMessage, 0, len(h.pending))
	for key := range h.pending {
		ids = append(ids, json.RawMessage(key))
	}
	// 清空 pending，防止重复发送
	h.pending = make(map[string]pendingInfo)
	return ids
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
		// 拦截代理自己发出的 workspace/diagnostic/refresh 响应，静默丢弃
		h.mu.Lock()
		_, isRefresh := h.refreshIDs[idKey(msg.ID)]
		if isRefresh {
			delete(h.refreshIDs, idKey(msg.ID))
		}
		h.mu.Unlock()
		if isRefresh {
			return nil, nil
		}
		return h.processResponse(ctx, &msg, raw, asyncPush)
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
func (h *Handler) processResponse(ctx context.Context, msg *BaseMessage, raw []byte, asyncPush func([]byte)) ([]byte, error) {
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
		)

	case "textDocument/completion":
		return h.handleResponseWithFastPath(ctx, msg, raw, info,
			func(fastCtx context.Context) (json.RawMessage, error) {
				return h.translateCompletion(fastCtx, msg.Result)
			},
		)

	case "textDocument/signatureHelp":
		return h.handleResponseWithFastPath(ctx, msg, raw, info,
			func(fastCtx context.Context) (json.RawMessage, error) {
				return h.translateSignatureHelp(fastCtx, msg.Result)
			},
		)

	case "textDocument/diagnostic":
		// LSP 3.17+ 拉取式诊断：编辑器主动请求，服务端以响应形式返回。
		// 与 publishDiagnostics 通知不同，这里走快速路径（缓存命中时同步返回）。
		// 若超时返回原文，后台翻译完成后通过 workspace/diagnostic/refresh 通知编辑器重新拉取。
		return h.handleDiagnosticPullResponse(ctx, msg, raw, info, asyncPush)

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
func (h *Handler) handleResponseWithFastPath(
	ctx context.Context,
	msg *BaseMessage,
	raw []byte,
	info pendingInfo,
	translateFn func(context.Context) (json.RawMessage, error),
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
		// 翻译 goroutine 仍在后台运行，阻塞等待其完成以写入缓存
		go func() {
			res := <-ch
			if res.err == nil {
				h.logger.Info("后台缓存预热完成",
					slog.String("method", shortMethod(info.Method)),
					slog.String("file", uriBasename(info.URI)),
					slog.Int("line", info.Line+1),
					slog.String("elapsed", fmtDuration(time.Since(start))),
				)
			} else {
				h.logger.Warn("后台缓存预热失败",
					slog.String("method", shortMethod(info.Method)),
					slog.String("file", uriBasename(info.URI)),
					slog.String("error", res.err.Error()),
				)
			}
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
		translatedDoc, err := h.translateMarkupContent(ctx, sigHelp.Signatures[i].Documentation)
		if err != nil {
			h.logger.Warn("翻译签名文档失败",
				slog.String("label", sigHelp.Signatures[i].Label),
				slog.String("error", err.Error()),
			)
			continue
		}
		sigHelp.Signatures[i].Documentation = translatedDoc
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

// handleDiagnosticPullResponse 处理 textDocument/diagnostic 拉取式响应的翻译。
//
// 与 hover 等纯快速路径不同，拉取式诊断翻译超时后仅返回原文是不够的——
// 编辑器不会再次主动拉取，缓存预热后的翻译结果永远无法展示。
//
// 解决方案：翻译完成后通过 asyncPush 向编辑器发送一条
// workspace/diagnostic/refresh 请求，让编辑器重新拉取所有文档的诊断。
// 此时缓存已命中，新的拉取请求将在 50ms 内完成翻译并返回中文结果。
func (h *Handler) handleDiagnosticPullResponse(
	ctx context.Context,
	msg *BaseMessage,
	raw []byte,
	info pendingInfo,
	asyncPush func([]byte),
) ([]byte, error) {
	start := time.Now()

	ch := make(chan translateResult, 1)
	bgCtx, bgCancel := context.WithTimeout(context.Background(), asyncDiagTimeout)
	go func() {
		defer bgCancel()
		r, err := h.translateDocumentDiagnosticReport(bgCtx, msg.Result)
		ch <- translateResult{result: r, err: err}
	}()

	// ── 第一阶段：50ms 快速缓存路径 ──
	select {
	case res := <-ch:
		elapsed := time.Since(start)
		if res.err == nil {
			return h.buildTranslatedResponse(ctx, msg, raw, info, res.result, elapsed, "缓存命中")
		}
		h.logger.Warn("拉取式诊断翻译失败，返回原文",
			slog.String("file", uriBasename(info.URI)),
			slog.String("error", res.err.Error()),
		)
		return raw, nil
	case <-time.After(cacheCheckTimeout):
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

	select {
	case res := <-ch:
		elapsed = time.Since(start)
		if res.err == nil {
			return h.buildTranslatedResponse(ctx, msg, raw, info, res.result, elapsed, "等待完成")
		}
		h.logger.Warn("拉取式诊断翻译失败，返回原文",
			slog.String("file", uriBasename(info.URI)),
			slog.String("error", res.err.Error()),
		)
		return raw, nil

	case <-secondWait:
		// 超时：返回原文。后台翻译完成后发 workspace/diagnostic/refresh 请求，
		// 触发编辑器重新拉取——此时缓存已命中，新拉取将直接返回中文结果。
		elapsed = time.Since(start)
		h.logger.Info("拉取式诊断翻译后台进行中，先返回原文",
			slog.String("file", uriBasename(info.URI)),
			slog.String("elapsed", fmtDuration(elapsed)),
			slog.String("hint", "翻译完成后将通过 workspace/diagnostic/refresh 触发编辑器重新拉取"),
		)

		go func() {
			res := <-ch
			if res.err != nil {
				h.logger.Warn("后台拉取式诊断翻译失败，无法触发刷新",
					slog.String("file", uriBasename(info.URI)),
					slog.String("error", res.err.Error()),
				)
				return
			}
			h.logger.Info("拉取式诊断后台翻译完成，发送 diagnostic/refresh",
				slog.String("file", uriBasename(info.URI)),
				slog.String("elapsed", fmtDuration(time.Since(start))),
			)
			if asyncPush != nil {
				h.sendDiagnosticRefresh(asyncPush)
			}
		}()
		return raw, nil
	}
}

// sendDiagnosticRefresh 通过 asyncPush 向编辑器发送 workspace/diagnostic/refresh 请求。
//
// 该请求（LSP 3.17+）告知编辑器重新拉取所有打开文档的诊断信息。
// 使用负数 ID（-1, -2, …）区分于 LSP 服务端的请求，编辑器对该请求的响应
// 会在 ProcessServerMessage 中被 refreshIDs 集合识别并静默丢弃。
func (h *Handler) sendDiagnosticRefresh(asyncPush func([]byte)) {
	h.mu.Lock()
	h.refreshID--
	id := h.refreshID
	idJSON := json.RawMessage(fmt.Sprintf("%d", id))
	h.refreshIDs[idKey(idJSON)] = struct{}{}
	h.mu.Unlock()

	req := BaseMessage{
		JSONRPC: "2.0",
		ID:      idJSON,
		Method:  "workspace/diagnostic/refresh",
	}
	data, err := json.Marshal(req)
	if err != nil {
		h.logger.Warn("序列化 workspace/diagnostic/refresh 失败",
			slog.String("error", err.Error()),
		)
		return
	}
	h.logger.Debug("发送 workspace/diagnostic/refresh",
		slog.Int64("id", id),
	)
	asyncPush(data)
}

// translateDiagnostics 翻译 textDocument/publishDiagnostics 的参数。
//
// 诊断消息使用"模板化翻译"策略：先提取动态标识符（变量名、类型名等），
// 翻译模板后再还原标识符。这使得 "variable 'foo' is unused" 和
// "variable 'bar' is unused" 共享同一个模板缓存条目。
// translateDocumentDiagnosticReport 翻译 textDocument/diagnostic 响应（拉取式诊断）。
//
// 响应格式（LSP 3.17+）：
//
//	{"kind":"full","resultId":"...","items":[{...Diagnostic...}]}
//
// kind 为 "unchanged" 时直接透传（items 为空，无需翻译）。
func (h *Handler) translateDocumentDiagnosticReport(ctx context.Context, result json.RawMessage) (json.RawMessage, error) {
	if len(result) == 0 || string(result) == "null" {
		return result, nil
	}

	var report DocumentDiagnosticReport
	if err := json.Unmarshal(result, &report); err != nil {
		return result, fmt.Errorf("解析 DocumentDiagnosticReport 失败: %w", err)
	}

	// kind = "unchanged" 表示诊断未变化，无条目需要翻译
	if report.Kind != "full" || len(report.Items) == 0 {
		return result, nil
	}

	h.logger.Debug("拉取式诊断翻译",
		slog.Int("count", len(report.Items)),
		slog.String("kind", report.Kind),
	)

	for i := range report.Items {
		if report.Items[i].Message == "" {
			continue
		}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		translated, err := h.translateDiagMessage(ctx, report.Items[i].Message)
		if err != nil {
			h.logger.Warn("翻译拉取式诊断消息失败",
				slog.String("message", truncate(report.Items[i].Message, 80)),
				slog.String("error", err.Error()),
			)
			continue
		}
		report.Items[i].Message = translated
	}

	out, err := json.Marshal(report)
	if err != nil {
		return result, fmt.Errorf("序列化翻译后的 DocumentDiagnosticReport 失败: %w", err)
	}
	return out, nil
}

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

		translated, err := h.translateDiagMessage(ctx, p.Diagnostics[i].Message)
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

// translateDiagMessage 使用模板化策略翻译单条诊断消息。
//
// 流程：
//  1. 提取引号/反引号包裹的标识符，替换为 $ID_N$ 占位符
//  2. 翻译模板文本（共享模板级缓存，大幅提升命中率）
//  3. 还原占位符为原始标识符
//  4. 根据 displayMode 组装最终展示文本
//
// 若消息不含标识符，退回到标准 [translateText] 翻译。
//
// 诊断消息是单行文本，双语格式特殊处理：
//   - bilingual / bilingual_compare：在译文末尾追加 " // 原文" 形式，保持紧凑
func (h *Handler) translateDiagMessage(ctx context.Context, message string) (string, error) {
	tmpl := translate.Templatize(message)

	var translated string
	var err error

	if !tmpl.IsTemplated {
		// 无标识符，走标准翻译路径
		translated, err = h.translateText(ctx, message)
		if err != nil {
			return message, err
		}
	} else {
		h.logger.Debug("诊断消息模板化",
			slog.Int("identifiers", len(tmpl.Identifiers)),
			slog.String("template", truncate(tmpl.Template, 80)),
		)

		// 翻译模板（模板作为缓存 key，命中率远高于包含动态标识符的原始消息）
		translatedTmpl, terr := h.translateText(ctx, tmpl.Template)
		if terr != nil {
			// 模板翻译失败，尝试直接翻译原文（降级）
			h.logger.Debug("模板翻译失败，退回原文翻译",
				slog.String("error", terr.Error()),
			)
			translated, err = h.translateText(ctx, message)
			if err != nil {
				return message, err
			}
		} else {
			// 还原标识符
			translated = translate.RestoreIdentifiers(translatedTmpl, tmpl.Identifiers)
		}
	}

	// 诊断消息是单行文本，双语模式使用紧凑格式：译文 // 原文
	return h.composeDiagBilingual(message, translated), nil
}

// composeDiagBilingual 对诊断消息应用双语格式。
//
// 诊断消息是单行文本（显示在编辑器底部状态栏或问题面板），不适合用 Markdown 分隔线。
// 因此采用更紧凑的内联格式：
//   - translation_only：仅译文
//   - bilingual / bilingual_compare：译文 + "  // " + 原文
//
// 若译文与原文相同（未发生翻译），只返回一份避免重复。
func (h *Handler) composeDiagBilingual(original, translated string) string {
	if h.displayMode == config.DisplayTranslationOnly || h.displayMode == "" {
		return translated
	}
	// 相同则不重复
	if original == translated || strings.TrimSpace(original) == "" {
		return translated
	}
	// 诊断消息双语：译文  //  原文（紧凑内联格式）
	return translated + "  //  " + original
}

// ─────────────────────────────────────────────────────────────────────────────
// 双语/对照展示组装
// ─────────────────────────────────────────────────────────────────────────────

// bilingualSeparator 是双语模式下原文与译文之间的 Markdown 分隔线。
// 使用 --- 水平线，在 markdown hover 渲染器中会显示为视觉分隔。
const bilingualSeparator = "\n\n---\n\n"

// composeBilingual 按 DisplayMode 将原文与译文组合成最终展示文本。
//
// 三种模式的处理逻辑：
//   - translation_only：直接返回 translated
//   - bilingual：        译文 + 分隔线 + 原文
//   - bilingual_compare：不走此函数，由 translateMarkupContent 直接调用
//     translateTextForBilingual + buildBilingualCompare
func (h *Handler) composeBilingual(original, translated string) string {
	switch h.displayMode {
	case config.DisplayBilingual:
		return composeBilingualMode(original, translated)
	default:
		// translation_only 或其他未知值：直接返回译文
		// bilingual_compare 模式由 translateMarkupContent 走独立路径
		return translated
	}
}

// composeBilingualMode 实现 bilingual 模式：译文在前，原文附后。
//
// 若译文与原文完全相同（未实际翻译，如纯代码/数字），则只返回译文避免重复。
// 若原文经过占位符保护后全为代码块（无可翻译文本），同样只返回译文。
func composeBilingualMode(original, translated string) string {
	// 边界：空内容
	if strings.TrimSpace(original) == "" || strings.TrimSpace(translated) == "" {
		return translated
	}
	// 边界：译文与原文相同（未发生翻译变化），避免重复
	if original == translated {
		return translated
	}
	// 检查原文是否全为代码块（占位符保护后没有可翻译文本）
	masked, _ := markdown.Protect(original)
	if strings.TrimSpace(masked) == "" {
		return translated
	}
	// 组装：译文 + 分隔线 + 原文
	return translated + bilingualSeparator + original
}

// ── bilingual_compare 核心：段落对照组装 ─────────────────────────────────────
//
// buildBilingualCompare 是 bilingual_compare 模式的最终组装函数。
//
// 与旧版 composeBilingualCompareMode(original, translated string) 的关键区别：
//   - 旧版：接收两个完整字符串，再按 \n\n 各自分割后尝试对齐
//     → 翻译引擎改变段落内换行数量时导致段落数不一致、内容错位
//   - 新版：接收已经 1:1 对应的段落切片
//     → 由 translateTextForBilingual 在翻译时保持段落边界，此处无需再分割
//
// 每对 (orig, trans) 的处理规则：
//   - 两者均为空：跳过
//   - 只有一侧有内容：直接输出该侧
//   - paraEffectivelyUnchanged(orig, trans)：只输出译文（避免重复）
//     覆盖场景：① 精确相等 ② Protect 后无可翻译词语（纯代码块/符号行）
//   - 否则：译文在上，原文在下（trans + \n\n + orig）
func buildBilingualCompare(origParas, transParas []string) string {
	maxLen := len(origParas)
	if len(transParas) > maxLen {
		maxLen = len(transParas)
	}

	var result []string
	for i := 0; i < maxLen; i++ {
		var orig, trans string
		if i < len(origParas) {
			orig = origParas[i]
		}
		if i < len(transParas) {
			trans = transParas[i]
		}

		origTrimmed := strings.TrimSpace(orig)
		transTrimmed := strings.TrimSpace(trans)

		// 两段均为空：跳过（保持段落间距不需要额外空行）
		if origTrimmed == "" && transTrimmed == "" {
			continue
		}

		// 只有原文（译文越界或为空）：直接输出原文
		if transTrimmed == "" {
			result = append(result, orig)
			continue
		}

		// 只有译文（原文越界或为空）：直接输出译文
		if origTrimmed == "" {
			result = append(result, trans)
			continue
		}

		// 判断此段落是否实质上未发生翻译变化：
		//   ① 精确相等
		//   ② 全为代码块（Protect 后纯文本为空）
		//   ③ 纯文本部分无词语性内容（只有标点/符号/等号等）
		// 满足任意条件只输出一次（用译文，已含还原后代码块），避免重复。
		if paraEffectivelyUnchanged(orig, trans) {
			result = append(result, trans)
			continue
		}

		// 正常对照：译文在上，原文在下
		result = append(result, trans+"\n\n"+orig)
	}

	if len(result) == 0 {
		// 安全兜底：全部段落均被跳过时返回原文（不丢失信息）
		return strings.Join(transParas, "\n\n")
	}
	return strings.Join(result, "\n\n")
}

// ── 段落对照辅助判断 ──────────────────────────────────────────────────────────

// isLetterRune 判断一个 rune 是否为字母类字符（含汉字、假名等 Unicode 字母）。
func isLetterRune(r rune) bool {
	// ASCII 字母
	if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
		return true
	}
	// CJK 统一汉字：U+4E00–U+9FFF
	if r >= 0x4E00 && r <= 0x9FFF {
		return true
	}
	// 其他常见多字节字母（希腊、西里尔等），排除常见标点范围（U+2000 以上多为符号）
	if r > 0x00FF && r < 0x2000 {
		return true
	}
	return false
}

// hasTranslatableWords 判断经过占位符保护后的纯文字部分是否含有"词语性"内容。
//
// 策略：扫描 masked 字符串，若存在连续 2 个及以上的 Unicode 字母（含汉字），
// 则认为有可翻译的词语。纯标点、空格、等号、箭头等符号不计入词语。
//
// 例如：
//   - " = , -> … "        → false（无词语）
//   - " = , Returns the " → true（"Re" 已满足 2 个连续字母）
func hasTranslatableWords(masked string) bool {
	run := 0
	for _, r := range masked {
		if isLetterRune(r) {
			run++
			if run >= 2 {
				return true
			}
		} else {
			run = 0
		}
	}
	return false
}

// paraEffectivelyUnchanged 判断一个段落的原文与译文是否"实质上未发生翻译"。
//
// 判断逻辑：
//  1. 精确相等：直接判定未变化
//  2. 用 markdown.Protect 提取纯文本（去掉代码块/行内代码占位符）：
//     a. 若纯文本部分全为空白（段落几乎全由代码组成）：判定未变化
//     b. 若纯文本部分不含任何"词语性"内容（只有标点、符号、等号等）：判定未变化
//     典型场景："`T` = `Instant`，`U` = `u64`" 这类仅有行内代码+符号的行
func paraEffectivelyUnchanged(orig, trans string) bool {
	if orig == trans {
		return true
	}
	// 提取原文的纯文本部分（代码块和行内代码替换为占位符后的剩余文字）
	maskedOrig, _ := markdown.Protect(orig)
	trimmedMasked := strings.TrimSpace(maskedOrig)

	// 纯文本部分为空：原文段落全为代码块，无实质可翻译内容
	if trimmedMasked == "" {
		return true
	}

	// 纯文本部分不含词语（只有标点/符号/空格/等号等）：无实质可翻译内容
	// 例如：" = ，… -> " 这类行，翻译引擎可能只替换标点，不应视为翻译变化
	if !hasTranslatableWords(trimmedMasked) {
		return true
	}

	return false
}

// composeBilingualCompareMode 是 bilingual_compare 模式的旧版实现（按字符串分割对齐）。
//
// ⚠️  已被新路径取代：translateMarkupContent → translateMarkupContentCompare
//
//	→ translateTextForBilingual + buildBilingualCompare
//
// 保留此函数仅供单元测试或其他调用方回退使用，正常渲染流程不再调用它。
// 新版通过段落级翻译保证 1:1 对应，彻底消除翻译引擎改变段落结构导致的错位。
func composeBilingualCompareMode(original, translated string) string {
	// 边界：空内容
	if strings.TrimSpace(original) == "" || strings.TrimSpace(translated) == "" {
		return translated
	}
	// 边界：整体完全相同
	if original == translated {
		return translated
	}

	origParas := strings.Split(original, "\n\n")
	transParas := strings.Split(translated, "\n\n")

	maxLen := len(origParas)
	if len(transParas) > maxLen {
		maxLen = len(transParas)
	}

	var result []string
	for i := 0; i < maxLen; i++ {
		var orig, trans string
		if i < len(origParas) {
			orig = origParas[i]
		}
		if i < len(transParas) {
			trans = transParas[i]
		}

		origTrimmed := strings.TrimSpace(orig)
		transTrimmed := strings.TrimSpace(trans)

		// 两段均为空：跳过
		if origTrimmed == "" && transTrimmed == "" {
			continue
		}

		// 原文段落仅有一侧（译文越界）：直接输出原文
		if transTrimmed == "" {
			result = append(result, orig)
			continue
		}

		// 原文越界但译文有内容：直接输出译文
		if origTrimmed == "" {
			result = append(result, trans)
			continue
		}

		// 判断此段落是否实质上未发生翻译变化
		if paraEffectivelyUnchanged(orig, trans) {
			result = append(result, trans)
			continue
		}

		// 正常对照：译文在上，原文在下
		result = append(result, trans+"\n\n"+orig)
	}

	if len(result) == 0 {
		return translated
	}
	return strings.Join(result, "\n\n")
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
//
// displayMode 决定翻译路径：
//   - translation_only / bilingual：整体翻译 → composeBilingual 组装
//   - bilingual_compare：段落级翻译（translateTextForBilingual）→ buildBilingualCompare 组装
//     原因：整体翻译时翻译引擎可能改变段落内换行数量，导致按 \n\n 重新分割后
//     原文与译文段落数不一致，进而使对照错位或产生代码块重复。
//     段落级翻译在翻译前就固定了段落边界，保证 1:1 对应。
func (h *Handler) translateMarkupContent(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return raw, nil
	}

	// bilingual_compare 模式走专用段落级路径
	if h.displayMode == config.DisplayBilingualCompare {
		return h.translateMarkupContentCompare(ctx, raw)
	}

	// 情况1：纯字符串
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		original := str
		translated, err := h.translateText(ctx, str)
		if err != nil {
			return raw, err
		}
		final := h.composeBilingual(original, translated)
		h.logger.Debug("文档字符串翻译",
			slog.String("original", truncate(original, 80)),
			slog.String("translated", truncate(translated, 80)),
			slog.String("mode", string(h.displayMode)),
		)
		out, err := json.Marshal(final)
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
		final := h.composeBilingual(original, translated)
		h.logger.Debug("MarkupContent 翻译",
			slog.String("kind", mc.Kind),
			slog.String("original", truncate(original, 80)),
			slog.String("translated", truncate(translated, 80)),
			slog.String("mode", string(h.displayMode)),
		)
		mc.Value = final
		out, err := json.Marshal(mc)
		if err != nil {
			return raw, fmt.Errorf("序列化翻译后的 MarkupContent 失败: %w", err)
		}
		return out, nil
	}

	// 情况3：无法识别的格式，原样返回
	return raw, nil
}

// translateMarkupContentCompare 是 bilingual_compare 模式的专用实现。
//
// 使用 translateTextForBilingual 做段落级翻译（保证 1:1 对应），
// 再用 buildBilingualCompare 组装对照文本，彻底避免段落错位和代码块重复。
func (h *Handler) translateMarkupContentCompare(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	// 辅助：对文本做段落级翻译并组装对照结果
	translateAndBuild := func(text string) (string, error) {
		origParas, transParas, err := h.translateTextForBilingual(ctx, text)
		if err != nil && ctx.Err() != nil {
			return text, err
		}
		return buildBilingualCompare(origParas, transParas), nil
	}

	// 情况1：纯字符串
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		final, err := translateAndBuild(str)
		if err != nil {
			return raw, err
		}
		h.logger.Debug("文档字符串翻译（对照模式）",
			slog.String("original", truncate(str, 80)),
			slog.String("mode", string(h.displayMode)),
		)
		out, err := json.Marshal(final)
		if err != nil {
			return raw, fmt.Errorf("序列化翻译后的字符串失败: %w", err)
		}
		return out, nil
	}

	// 情况2：MarkupContent 对象
	var mc MarkupContent
	if err := json.Unmarshal(raw, &mc); err == nil && mc.Kind != "" {
		final, err := translateAndBuild(mc.Value)
		if err != nil {
			return raw, err
		}
		h.logger.Debug("MarkupContent 翻译（对照模式）",
			slog.String("kind", mc.Kind),
			slog.String("original", truncate(mc.Value, 80)),
			slog.String("mode", string(h.displayMode)),
		)
		mc.Value = final
		out, err := json.Marshal(mc)
		if err != nil {
			return raw, fmt.Errorf("序列化翻译后的 MarkupContent 失败: %w", err)
		}
		return out, nil
	}

	// 情况3：无法识别的格式，原样返回
	return raw, nil
}

// paragraphSep 是 Markdown 段落的分隔符（两个换行）
const paragraphSep = "\n\n"

// minParagraphSplitLen 是触发段落级拆分的最小文本长度。
// 短文本（如单行诊断消息）不拆分，避免无意义的拆分开销。
const minParagraphSplitLen = 120

// translateText 翻译一段文本。
//
// 采用"占位符替换法 + 段落级缓存"策略：
//  1. 调用 [markdown.Protect] 将代码块替换为编号占位符（$CODE_N$），得到纯文本
//  2. 若 masked 文本包含多个段落（以 \n\n 分隔），则按段落拆分并独立翻译，
//     每个段落作为独立的缓存 key，提高跨文档的缓存复用率
//  3. 对于短文本或单段落文本，仍作为整体翻译（保留完整上下文）
//  4. 调用 [markdown.Restore] 将占位符还原为原始代码块
//
// 段落级缓存的收益：不同函数/类型的 hover 文档中，相同的描述段落
// （如 "Parameters:"、"Returns:" 等固定模式）可直接命中缓存。
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

	// ── 第一步：用占位符保护代码块 ──
	masked, codes := markdown.Protect(text)

	h.logger.Debug("占位符保护完成",
		slog.Int("code_blocks", len(codes)),
		slog.String("masked_preview", truncate(masked, 80)),
	)

	// 若掩码后文本全为空白，说明原文只有代码块，直接返回原文
	if strings.TrimSpace(masked) == "" {
		return text, nil
	}

	// ── 第二步：段落级拆分翻译 / 整体翻译 ──
	var translated string
	var err error

	paragraphs := strings.Split(masked, paragraphSep)
	// 多段落且文本足够长时启用段落级翻译
	if len(paragraphs) > 1 && len(masked) >= minParagraphSplitLen {
		translated, err = h.translateParagraphs(ctx, paragraphs)
	} else {
		translated, err = h.engine.Translate(ctx, masked, h.targetLang)
	}

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

	// ── 第三步：还原占位符为原始代码块 ──
	result := markdown.Restore(translated, codes)

	h.logger.Debug("占位符还原完成",
		slog.String("translated", truncate(translated, 80)),
		slog.String("result", truncate(result, 80)),
	)

	return result, nil
}

// translateTextForBilingual 是 bilingual_compare 模式专用的段落级翻译函数。
//
// 与 translateText 的关键区别：
//   - translateText：对整个文本做 Protect → 翻译 → Restore，翻译引擎可能改变
//     段落内换行数量，导致 composeBilingualCompareMode 按 \n\n 重新分割后
//     origParas 与 transParas 数量不一致，进而使对照错位或代码块重复出现。
//   - translateTextForBilingual：先按 \n\n 将原文拆成段落，对每个段落
//     独立做 Protect → 翻译 → Restore，最终返回长度相等的 origParas / transParas。
//     翻译引擎的任何段落内部改动都被限制在单段落内，不影响段落间的1:1对应。
//
// 每个段落的翻译策略（与 translateParagraphs 保持一致）：
//   - 空白段落：原样保留
//   - Protect 后无文字内容（全为代码块）：不翻译，trans = orig
//   - Protect 后无词语性内容（仅标点/符号）：不翻译，trans = orig
//   - 其余：独立调用翻译引擎
//
// 翻译失败时局部降级（trans = orig），不影响其他段落，也不返回 error（
// 除非 ctx 已取消）。
func (h *Handler) translateTextForBilingual(ctx context.Context, text string) (origParas, transParas []string, err error) {
	if strings.TrimSpace(text) == "" {
		return []string{text}, []string{text}, nil
	}

	paras := strings.Split(text, paragraphSep)
	origParas = paras
	transParas = make([]string, len(paras))

	for i, para := range paras {
		// 检查 context 是否已取消
		if ctx.Err() != nil {
			// 将剩余段落填充原文后返回
			for j := i; j < len(paras); j++ {
				transParas[j] = paras[j]
			}
			return origParas, transParas, ctx.Err()
		}

		trimmed := strings.TrimSpace(para)

		// 空白段落原样保留（保持段落间距）
		if trimmed == "" {
			transParas[i] = para
			continue
		}

		// 用占位符保护代码块和技术术语
		masked, codes := markdown.Protect(para)
		maskedTrimmed := strings.TrimSpace(masked)

		// 全为代码块：不翻译
		if maskedTrimmed == "" {
			transParas[i] = para
			continue
		}

		// 无词语性内容（仅标点/符号/等号等）：不翻译
		// 典型场景："`T` = `Instant`，`U` = `u64`" 这类行
		if !hasTranslatableWords(maskedTrimmed) {
			transParas[i] = para
			continue
		}

		// 翻译该段落
		translatedMasked, tErr := h.engine.Translate(ctx, masked, h.targetLang)
		if tErr != nil {
			h.logger.Debug("段落翻译失败，保留原文",
				slog.Int("paragraph", i+1),
				slog.String("error", tErr.Error()),
				slog.String("preview", truncate(para, 60)),
			)
			transParas[i] = para // 局部降级
			continue
		}

		// 还原占位符
		transParas[i] = markdown.Restore(translatedMasked, codes)
	}

	return origParas, transParas, nil
}

// translateParagraphs 将多个段落分别翻译并拼接，每个段落作为独立的缓存单元。
//
// 策略：
//   - 纯空白段落：原样保留（保持段落间距）
//   - 纯占位符段落（如 "$CODE_0$"）：跳过翻译，原样保留
//   - 其余段落：独立调用翻译引擎（各自作为缓存 key）
//
// 翻译失败的段落保留原文（局部降级，不影响其他段落的翻译结果）。
func (h *Handler) translateParagraphs(ctx context.Context, paragraphs []string) (string, error) {
	results := make([]string, len(paragraphs))

	// 段落翻译失败计数
	failCount := 0

	for i, para := range paragraphs {
		// 检查 context 是否已取消
		if ctx.Err() != nil {
			return "", ctx.Err()
		}

		trimmed := strings.TrimSpace(para)

		// 空白段落原样保留
		if trimmed == "" {
			results[i] = para
			continue
		}

		// 纯占位符段落（仅包含 $CODE_N$ 占位符和空白）跳过翻译
		if isPlaceholderOnly(trimmed) {
			results[i] = para
			continue
		}

		// 翻译该段落
		translated, err := h.engine.Translate(ctx, para, h.targetLang)
		if err != nil {
			h.logger.Debug("段落翻译失败，保留原文",
				slog.Int("paragraph", i+1),
				slog.String("error", err.Error()),
				slog.String("preview", truncate(para, 60)),
			)
			results[i] = para // 局部降级
			failCount++
			continue
		}
		results[i] = translated
	}

	if failCount > 0 {
		h.logger.Debug("段落级翻译部分失败",
			slog.Int("total", len(paragraphs)),
			slog.Int("failed", failCount),
		)
	}

	return strings.Join(results, paragraphSep), nil
}

// isPlaceholderOnly 判断文本是否仅由占位符（$CODE_N$）和空白组成。
// 用于跳过纯代码块段落的翻译。
func isPlaceholderOnly(text string) bool {
	// 移除所有占位符后，若剩余内容全为空白则认为是纯占位符段落
	cleaned := markdown.PlaceholderRe().ReplaceAllString(text, "")
	return strings.TrimSpace(cleaned) == ""
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
	// 去掉换行，避免日志换行
	clean := strings.ReplaceAll(s, "\n", " ")
	runes := []rune(clean)
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
