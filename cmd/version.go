// Package cmd — version 子命令：输出构建版本信息。
package cmd

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// ─────────────────────────────────────────────
// 版本信息（由 main 包通过 SetVersionInfo 注入）

var (
	buildVersion = "dev"
	buildCommit  = ""
	buildDate    = ""
)

// SetVersionInfo 由 main 包在启动时调用，将 ldflags 注入的版本信息传递给 cmd 包。
// 若 commit / date 为空（本地 go build），则自动从 VCS 元数据（go install latest 也携带）读取。
// 同时将 rootCmd.Version 设置为完整版本字符串，使 --version 标志生效。
func SetVersionInfo(version, commit, date string) {
	buildVersion = version

	// ── 1. 优先使用 ldflags 注入值 ────────────────────────────
	buildCommit = commit
	buildDate = date

	// ── 2. 回退：从 debug.ReadBuildInfo() 读取 VCS 元数据 ────
	// go install latest 或 go install <module>@<version> 会在二进制中嵌入 vcs.* 信息
	if buildCommit == "" || buildDate == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, s := range info.Settings {
				switch s.Key {
				case "vcs.revision":
					if buildCommit == "" && s.Value != "" {
						// 只取前 7 位，与 git 短 SHA 对齐
						if len(s.Value) > 7 {
							buildCommit = s.Value[:7]
						} else {
							buildCommit = s.Value
						}
					}
				case "vcs.time":
					if buildDate == "" {
						// 格式示例：2026-04-03T12:00:00Z，截取日期部分
						if len(s.Value) >= 10 {
							buildDate = s.Value[:10]
						} else {
							buildDate = s.Value
						}
					}
				}
			}
		}
	}

	// ── 3. 设置 rootCmd.Version，让 cobra 自动注册 --version 标志 ────
	// cobra 会将此字符串直接打印，因此构造为单行完整信息
	shortCommit := buildCommit
	if shortCommit == "" {
		shortCommit = "unknown"
	}
	shortDate := buildDate
	if shortDate == "" {
		shortDate = "unknown"
	}
	rootCmd.Version = fmt.Sprintf("%s (commit: %s, date: %s)", buildVersion, shortCommit, shortDate)
}

// versionCmd 输出构建版本信息。
var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "显示版本信息",
	Long:  "显示 lsp-proxy 的版本号、构建 commit 和构建日期。",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, args []string) {
		printVersion()
	},
}

// printVersion 格式化并输出版本信息。
func printVersion() {
	commit := buildCommit
	if commit == "" {
		commit = "unknown"
	}
	date := buildDate
	if date == "" {
		date = "unknown"
	}
	fmt.Printf("lsp-proxy %s\n", buildVersion)
	fmt.Printf("  commit : %s\n", commit)
	fmt.Printf("  date   : %s\n", date)
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
