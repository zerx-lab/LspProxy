// LspProxy 程序入口
package main

import (
	"fmt"
	"os"

	"github.com/zerx-lab/LspProxy/cmd"
)

func main() {
	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
