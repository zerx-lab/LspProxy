// LspProxy 程序入口
package main

import (
	"fmt"
	"os"

	"github.com/zerx-lab/LspProxy/cmd"
)

// 以下三个变量由 -ldflags 在构建时注入：
//
//	-X main.version=v1.2.3   → release tag
//	-X main.commit=abc1234   → git commit SHA
//	-X main.date=2026-04-03  → 构建日期
//
// 若未注入（本地 go build / go install latest），
// cmd 包会通过 debug.ReadBuildInfo() 从 VCS 元数据中自动填充 commit 和 date。
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() {
	cmd.SetVersionInfo(version, commit, date)
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
