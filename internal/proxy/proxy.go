// Package proxy 实现 LSP 透明代理的核心逻辑。
// 代理以子进程方式启动真实的 LSP 服务器，在编辑器与 LSP 之间转发消息，
// 并对服务端响应中的文档注释进行实时翻译。
package proxy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"

	"github.com/zerx-lab/LspProxy/internal/config"
	"github.com/zerx-lab/LspProxy/internal/lsp"
	"github.com/zerx-lab/LspProxy/internal/translate"
)

// lspExitError 携带 LSP 子进程的原始退出码，用于在 cmd/run.go 中透传给编辑器进程。
type lspExitError struct {
	code int
	err  error
}

func (e *lspExitError) Error() string { return e.err.Error() }
func (e *lspExitError) Unwrap() error { return e.err }
func (e *lspExitError) ExitCode() int { return e.code }

// Proxy 是 LSP 透明代理，插入编辑器与真实 LSP 进程之间。
type Proxy struct {
	cfg    *config.Config
	engine translate.Engine
	logger *slog.Logger
}

// New 创建一个新的 Proxy 实例。
//
//   - cfg:    代理配置（目标语言、缓存大小等）
//   - engine: 翻译引擎（已包装 LRU 缓存）
//   - logger: 结构化日志（必须写到文件，不能写到 stdout/stderr，否则污染 LSP 协议）
func New(cfg *config.Config, engine translate.Engine, logger *slog.Logger) *Proxy {
	return &Proxy{
		cfg:    cfg,
		engine: engine,
		logger: logger,
	}
}

// Run 启动代理。
//
// 它会以子进程方式启动 command（携带 args），然后开启两个转发 goroutine：
//   - forwardClientToLsp：os.Stdin → lsp 子进程 stdin
//   - forwardLspToClient：lsp 子进程 stdout → os.Stdout（含翻译处理）
//
// 任意一个 goroutine 退出后（通常是 LSP 进程关闭），Run 会等待另一个也退出后返回。
// ctx 取消时会通过 exec.CommandContext 终止子进程，进而使两个 goroutine 都退出。
func (p *Proxy) Run(ctx context.Context, command string, args []string) error {
	p.logger.Info("启动 LSP 子进程",
		slog.String("command", command),
		slog.Any("args", args),
		slog.String("engine", p.engine.Name()),
		slog.String("targetLang", p.cfg.Proxy.TargetLang),
	)

	// 启动 LSP 子进程
	cmd := exec.CommandContext(ctx, command, args...)

	// 将 LSP 进程的 stderr 转发到我们的 stderr，方便排查 LSP 本身的错误
	cmd.Stderr = os.Stderr

	// 获取 LSP 进程的标准输入管道（代理 → LSP）
	lspStdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("获取 LSP 子进程 stdin 管道失败: %w", err)
	}

	// 获取 LSP 进程的标准输出管道（LSP → 代理）
	lspStdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("获取 LSP 子进程 stdout 管道失败: %w", err)
	}

	// 启动子进程
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("启动 LSP 子进程失败: %w", err)
	}

	p.logger.Info("LSP 子进程已启动", slog.Int("pid", cmd.Process.Pid))

	// 创建消息处理器（负责翻译）
	handler := lsp.NewHandler(p.engine, p.cfg.Proxy.TargetLang, p.logger, p.cfg.Proxy.TranslationTimeout, p.cfg.Proxy.DisplayMode)

	// 使用 WaitGroup 等待两个转发 goroutine 都退出
	var wg sync.WaitGroup
	wg.Add(2)

	// goroutine 1：编辑器 stdin → LSP 子进程 stdin
	go func() {
		defer wg.Done()
		defer lspStdin.Close() // 关闭写端，通知 LSP 进程 EOF
		p.forwardClientToLsp(ctx, os.Stdin, lspStdin, handler)
	}()

	// goroutine 2：LSP 子进程 stdout → 编辑器 stdout（含翻译）
	go func() {
		defer wg.Done()
		p.forwardLspToClient(ctx, lspStdout, os.Stdout, handler)
	}()

	// 等待两个转发 goroutine 结束
	wg.Wait()

	// 等待子进程退出并获取退出状态
	waitErr := cmd.Wait()
	if waitErr != nil && ctx.Err() != nil {
		// 进程被 context 取消时退出不算异常
		p.logger.Info("LSP 子进程因 context 取消而退出")
		return nil
	}

	if waitErr != nil {
		// LSP 子进程异常退出：获取退出码，向编辑器透传崩溃信息，再退出
		exitCode := 1
		var exitErr *exec.ExitError
		if ok := errors.As(waitErr, &exitErr); ok {
			exitCode = exitErr.ExitCode()
		}
		p.logger.Warn("LSP 子进程异常退出",
			slog.String("error", waitErr.Error()),
			slog.Int("exitCode", exitCode),
		)
		// 向编辑器发送崩溃通知，并对所有 in-flight 请求返回错误响应
		p.notifyLspCrash(handler, waitErr, exitCode)
		return &lspExitError{code: exitCode, err: fmt.Errorf("LSP 子进程退出: %w", waitErr)}
	}

	p.logger.Info("LSP 子进程正常退出")
	return nil
}

// notifyLspCrash 在 LSP 子进程异常退出后，通过 os.Stdout 直接向编辑器发送：
//  1. window/showMessage 通知：在编辑器 UI 上弹出错误提示
//  2. 所有 in-flight 请求的 RequestFailed 错误响应：解除编辑器侧挂起的请求
//
// 此时两个转发 goroutine 均已退出，os.Stdout 的写出 goroutine 也已结束，
// 直接写入 os.Stdout 是安全的（无并发竞争）。
func (p *Proxy) notifyLspCrash(handler *lsp.Handler, waitErr error, exitCode int) {
	crashMsg := fmt.Sprintf("LSP 进程已崩溃（exit %d）：%s", exitCode, waitErr.Error())

	// ── 1. window/showMessage 通知 ──────────────────────────────────────
	type showMessageParams struct {
		// Type: 1=Error 2=Warning 3=Info 4=Log
		Type    int    `json:"type"`
		Message string `json:"message"`
	}
	notifParams, _ := json.Marshal(showMessageParams{Type: 1, Message: crashMsg})
	notif := lsp.BaseMessage{
		JSONRPC: "2.0",
		Method:  "window/showMessage",
		Params:  notifParams,
	}
	if data, err := json.Marshal(notif); err == nil {
		if werr := lsp.WriteMessage(os.Stdout, data); werr != nil {
			p.logger.Warn("发送 window/showMessage 失败", slog.String("error", werr.Error()))
		}
	}

	// ── 2. 对所有 in-flight 请求发送 RequestFailed 错误响应 ──────────────
	// LSP 错误码 -32803 = RequestFailed（LSP 3.17）
	const codeRequestFailed = -32803
	for _, id := range handler.DrainPending() {
		resp := lsp.BaseMessage{
			JSONRPC: "2.0",
			ID:      id,
			Error: &lsp.ResponseError{
				Code:    codeRequestFailed,
				Message: crashMsg,
			},
		}
		data, err := json.Marshal(resp)
		if err != nil {
			continue
		}
		if werr := lsp.WriteMessage(os.Stdout, data); werr != nil {
			p.logger.Warn("发送 RequestFailed 响应失败",
				slog.String("id", string(id)),
				slog.String("error", werr.Error()),
			)
			break // stdout 已不可写，后续也会失败，直接停止
		}
	}
}

// forwardClientToLsp 从 clientReader（os.Stdin）读取 JSON-RPC 帧，
// 调用 handler.TrackRequest 记录请求，然后原样转发到 lspWriter（LSP 子进程 stdin）。
//
// 该函数在 EOF 或错误时返回，由调用方的 goroutine 负责清理。
func (p *Proxy) forwardClientToLsp(
	ctx context.Context,
	clientReader io.Reader,
	lspWriter io.Writer,
	handler *lsp.Handler,
) {
	reader := bufio.NewReaderSize(clientReader, 1<<20) // 1 MiB 读缓冲

	for {
		// 注意：ReadMessage 阻塞在 io.Reader 上，无法被 context 取消。
		// 此处的 context 检查仅是"尽力而为"：只有在上一条消息处理完毕、
		// 下一条消息尚未开始读取时才能响应取消信号。
		// 实际退出依赖管道关闭（LSP 进程退出或 stdin 关闭）触发 EOF。
		select {
		case <-ctx.Done():
			p.logger.Debug("forwardClientToLsp: context 已取消，退出")
			return
		default:
		}

		// 读取一帧完整的 LSP 消息
		raw, err := lsp.ReadMessage(reader)
		if err != nil {
			if err == io.EOF {
				p.logger.Info("客户端连接已关闭（EOF），停止转发客户端 → LSP")
			} else {
				p.logger.Error("读取客户端消息失败", slog.String("error", err.Error()))
			}
			return
		}

		// 解析消息以追踪请求（用于响应时找到对应方法名）
		var msg lsp.BaseMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			p.logger.Warn("解析客户端消息失败，原样转发",
				slog.String("error", err.Error()),
			)
		} else {
			// 拦截代理注入请求的响应（如 workspace/diagnostic/refresh），
			// 这些响应不应转发给 LSP 服务端，否则会导致 LSP 崩溃。
			if handler.InterceptProxyResponse(&msg) {
				continue
			}
			// 记录请求：建立 ID → Method 的映射
			handler.TrackRequest(&msg)
			p.logger.Debug("收到客户端请求",
				slog.String("method", msg.Method),
				slog.String("id", string(msg.ID)),
			)
		}

		// 原样写入 LSP 子进程
		if err := lsp.WriteMessage(lspWriter, raw); err != nil {
			p.logger.Error("转发消息到 LSP 失败", slog.String("error", err.Error()))
			return
		}
	}
}

// forwardLspToClient 从 lspReader（LSP 子进程 stdout）读取 JSON-RPC 帧，
// 每帧启动独立 goroutine 进行翻译处理，完成后投入写出队列由专用写出 goroutine 串行消费。
//
// 设计要点：
//   - 读取循环不阻塞：管道始终被持续消费，翻译耗时不影响读取
//   - 写出与翻译解耦：翻译 goroutine 将结果投入 writeCh 后立即返回，
//     不因 stdout 管道暂时写满而阻塞，彻底避免死锁
//   - 写出 goroutine 串行消费 writeCh，保证写入 clientWriter 的帧原子性
//   - asyncPush 同样投入 writeCh，与普通帧共享同一写出路径
func (p *Proxy) forwardLspToClient(
	ctx context.Context,
	lspReader io.Reader,
	clientWriter io.Writer,
	handler *lsp.Handler,
) {
	reader := bufio.NewReaderSize(lspReader, 1<<20) // 1 MiB 读缓冲

	// writeCh 是翻译 goroutine 与写出 goroutine 之间的解耦队列。
	// 深度 512：足以应对突发翻译完成，翻译 goroutine 投入后立即返回不阻塞。
	writeCh := make(chan []byte, 512)

	// writeMu + writeClosed 保护 writeCh 的发送操作。
	// 当 LSP 进程退出后 writeCh 会被关闭，但后台异步翻译 goroutine
	// 可能仍在运行，完成后尝试发送会导致 panic。
	var writeMu sync.Mutex
	writeClosed := false

	// 写出 goroutine：串行从 writeCh 取帧，逐一写入 clientWriter。
	// 串行写入保证帧不交叉；若 clientWriter（os.Stdout）暂时阻塞，
	// 只有此 goroutine 挂起，翻译 goroutine 和读取循环不受影响。
	var writerDone sync.WaitGroup
	writerDone.Add(1)
	go func() {
		defer writerDone.Done()
		for data := range writeCh {
			if err := lsp.WriteMessage(clientWriter, data); err != nil {
				p.logger.Error("写入客户端失败", slog.String("error", err.Error()))
				// 写出失败后排空队列，防止翻译 goroutine 因 writeCh 满而永久阻塞
				go func() {
					for range writeCh {
					}
				}()
				return
			}
		}
	}()

	// safeClose 安全关闭 writeCh，确保不会有 goroutine 再向其发送。
	safeClose := func() {
		writeMu.Lock()
		defer writeMu.Unlock()
		if !writeClosed {
			writeClosed = true
			close(writeCh)
		}
	}

	// enqueue 将一帧投入写出队列，供翻译 goroutine 和 asyncPush 共用。
	// 使用 writeMu 保护，防止向已关闭的 channel 发送导致 panic。
	enqueue := func(data []byte) {
		writeMu.Lock()
		defer writeMu.Unlock()
		if writeClosed {
			p.logger.Warn("写出队列已关闭，丢弃帧", slog.Int("bytes", len(data)))
			return
		}
		select {
		case writeCh <- data:
		default:
			p.logger.Warn("写出队列已满，丢弃帧", slog.Int("bytes", len(data)))
		}
	}

	// asyncPush 供后台 goroutine（如诊断异步翻译）主动推送消息到客户端
	asyncPush := func(data []byte) { enqueue(data) }

	// 限制并发翻译 goroutine 数量，防止高频场景瞬间创建大量 goroutine
	sem := make(chan struct{}, 32)

	for {
		// 注意：ReadMessage 阻塞在 io.Reader 上，无法被 context 取消。
		// 此处的 context 检查仅是"尽力而为"：只有在上一条消息处理完毕、
		// 下一条消息尚未开始读取时才能响应取消信号。
		// 实际退出依赖管道关闭（LSP 进程退出或 stdin 关闭）触发 EOF。
		select {
		case <-ctx.Done():
			p.logger.Debug("forwardLspToClient: context 已取消，退出")
			safeClose()
			writerDone.Wait()
			return
		default:
		}

		raw, err := lsp.ReadMessage(reader)
		if err != nil {
			if err == io.EOF {
				p.logger.Info("LSP 子进程已关闭输出（EOF），停止转发 LSP → 客户端")
			} else {
				p.logger.Error("读取 LSP 消息失败", slog.String("error", err.Error()))
			}
			safeClose()
			writerDone.Wait()
			return
		}

		// 每帧独立 goroutine：翻译完成后投入 writeCh，不阻塞读取循环
		sem <- struct{}{} // 获取信号量，限制并发翻译 goroutine 数量
		go func(rawFrame []byte) {
			defer func() { <-sem }() // 释放信号量
			processed, err := handler.ProcessServerMessage(ctx, rawFrame, asyncPush)
			if err != nil {
				p.logger.Warn("处理 LSP 消息失败，原样透传",
					slog.String("error", err.Error()),
				)
				processed = rawFrame
			}
			// processed 为 nil 表示该帧已被代理内部消费（如 diagnostic/refresh 响应），
			// 无需转发给编辑器，直接丢弃。
			if processed == nil {
				return
			}
			enqueue(processed)
		}(raw)
	}
}
