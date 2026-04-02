// Package cmd 包含 lsp-proxy 的所有 CLI 命令定义。
package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// cfgFile 保存用户通过 --config 标志指定的配置文件路径
var cfgFile string

// rootCmd 是 lsp-proxy 的根命令。
// 当用户直接执行 lsp-proxy 时，会触发 RunE（在 run.go 中注册）。
var rootCmd = &cobra.Command{
	Use:   "lsp-proxy",
	Short: "LSP 中文翻译代理",
	Long: `LspProxy 透明代理 LSP 消息，将文档注释实时翻译为中文。

使用方式：
  # 直接运行代理（将 LSP 命令作为参数传入）
  lsp-proxy -- rust-analyzer

  # 传入额外参数给 LSP 服务器
  lsp-proxy -- clangd --background-index

  # 打开 TUI 管理界面
  lsp-proxy --tui

  # 指定翻译引擎
  lsp-proxy -e openai -- rust-analyzer`,
}

// Execute 是程序入口调用的根命令执行函数。
// 所有子命令的错误都会向上冒泡至此处。
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// --config 是全局持久标志，对所有子命令均生效
	rootCmd.PersistentFlags().StringVar(
		&cfgFile,
		"config",
		"",
		"配置文件路径（默认 ~/.config/lsp-proxy/config.yaml）",
	)

	// 将 --config 标志绑定到 viper，以便在子命令中通过 viper.GetString("config") 读取
	_ = viper.BindPFlag("config", rootCmd.PersistentFlags().Lookup("config"))
}
