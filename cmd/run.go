// Package cmd 包含 lsp-proxy 的所有 CLI 命令定义。
// 本文件注册根命令的运行逻辑（直接代理模式与 TUI 模式）。
package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/zerx-lab/LspProxy/internal/config"
	"github.com/zerx-lab/LspProxy/internal/proxy"
	"github.com/zerx-lab/LspProxy/internal/translate"
	"github.com/zerx-lab/LspProxy/tui"
)

// tuiFlag 若为 true，则启动 TUI 管理界面而非直接运行代理
var tuiFlag bool

func init() {
	// 将运行逻辑绑定到根命令
	rootCmd.RunE = runProxy

	// --tui：启动 TUI 管理界面
	rootCmd.Flags().BoolVar(
		&tuiFlag,
		"tui",
		false,
		"启动 TUI 管理界面（交互式配置与日志查看）",
	)

	// -e / --engine：覆盖配置文件中的翻译引擎设置
	rootCmd.Flags().StringP(
		"engine",
		"e",
		"",
		"翻译引擎（google|openai），覆盖配置文件中的设置",
	)

	// 允许在 -- 之后传入 LSP 命令参数（如 rust-analyzer、clangd 等）
	// ArbitraryArgs 使 cobra 不对位置参数数量做限制
	rootCmd.Args = cobra.ArbitraryArgs
}

// runProxy 是根命令的主执行函数。
//
// 两种运行模式：
//  1. --tui 模式：启动 Bubble Tea TUI 管理界面
//  2. 代理模式：args 中包含 LSP 命令，例如 ["rust-analyzer"] 或 ["clangd", "--background-index"]
func runProxy(cmd *cobra.Command, args []string) error {
	// ── 1. 确定配置文件路径 ─────────────────────────────────────────────
	cfgPath := cfgFile
	if cfgPath == "" {
		cfgPath = config.DefaultPath()
	}

	// ── 2. 加载配置 ──────────────────────────────────────────────────────
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}

	// ── 3. 命令行标志覆盖配置文件 ────────────────────────────────────────
	if engineFlag, _ := cmd.Flags().GetString("engine"); engineFlag != "" {
		cfg.Translate.Engine = engineFlag
	}

	// ── 4. TUI 模式 ───────────────────────────────────────────────────────
	if tuiFlag {
		return tui.Run(cfg, cfgPath)
	}

	// ── 5. 代理模式：校验参数 ─────────────────────────────────────────────
	if len(args) == 0 {
		return fmt.Errorf(
			"代理模式需要指定 LSP 命令，例如：\n  lsp-proxy -- rust-analyzer\n  lsp-proxy -- clangd --background-index\n\n" +
				"若要启动管理界面，请使用 --tui 标志",
		)
	}

	lspCommand := args[0]
	lspArgs := args[1:]

	// ── 6. 初始化文件日志（严禁写入 stdout，否则污染 LSP 协议）────────────
	logger, logFile, err := buildFileLogger(cfg)
	if err != nil {
		// 日志初始化失败时降级为 stderr（不影响 LSP 协议）
		fmt.Fprintf(os.Stderr, "警告：初始化日志文件失败，降级到 stderr：%v\n", err)
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: parseLogLevel(cfg.Log.Level),
		}))
	} else {
		defer logFile.Close()
	}

	// ── 7. 创建翻译引擎 ───────────────────────────────────────────────────
	engine, err := translate.New(cfg, logger)
	if err != nil {
		return fmt.Errorf("初始化翻译引擎失败: %w", err)
	}

	// 若引擎实现了 io.Closer（如 DictEngine），在退出时关闭以确保资源释放和数据持久化
	if closer, ok := engine.(interface{ Close() error }); ok {
		defer closer.Close()
	}

	logger.Info("翻译引擎已初始化",
		slog.String("engine", engine.Name()),
		slog.String("targetLang", cfg.Proxy.TargetLang),
	)

	// ── 8. 创建代理实例 ───────────────────────────────────────────────────
	p := proxy.New(cfg, engine, logger)

	// ── 9. 监听系统信号，实现优雅退出 ────────────────────────────────────
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// ── 10. 运行代理（阻塞直到 LSP 进程退出或收到终止信号）───────────────
	return p.Run(ctx, lspCommand, lspArgs)
}

// buildFileLogger 根据配置创建写入文件的 slog.Logger。
// 返回 logger 实例、打开的文件句柄（调用方需负责 Close）以及错误信息。
//
// 日志文件路径取自 cfg.Log.File，若目录不存在会自动创建。
func buildFileLogger(cfg *config.Config) (*slog.Logger, *os.File, error) {
	logPath := cfg.Log.File

	// 确保日志目录存在
	logDir := filepath.Dir(logPath)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("创建日志目录失败 [%s]: %w", logDir, err)
	}

	// 以追加方式打开日志文件（不存在时自动创建）
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("打开日志文件失败 [%s]: %w", logPath, err)
	}

	level := parseLogLevel(cfg.Log.Level)

	// 使用 JSON 格式便于后续解析，写入文件
	handler := slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level:     level,
		AddSource: level == slog.LevelDebug, // 仅在 debug 级别时附加源码位置
	})

	return slog.New(handler), f, nil
}

// parseLogLevel 将配置字符串转换为 slog.Level。
// 支持 "debug"、"info"（默认）、"warn"、"error"，不区分大小写。
func parseLogLevel(level string) slog.Level {
	level = strings.ToLower(level)
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		// 未知值或空字符串均默认为 info
		return slog.LevelInfo
	}
}
