// Command samcheonpo catches AI coding agents drifting off task and shows the
// cost as a receipt.
package main

import (
	"fmt"
	"os"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cli"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
)

func main() {
	// The hook path runs on every tool call: branch before building the CLI.
	if (len(os.Args) == 3 || len(os.Args) == 4) && os.Args[1] == "hook" {
		agent := "claude"
		if len(os.Args) == 4 {
			agent = os.Args[3]
		}
		os.Exit(hookclient.MainAgent(os.Args[2], agent, os.Stdin, os.Stdout))
	}
	if err := cli.Root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "samcheonpo:", err)
		os.Exit(1)
	}
}
