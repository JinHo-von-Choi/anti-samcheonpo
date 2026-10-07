// Command samcheonpo-hook is the lightweight hook transport. It intentionally
// imports no CLI, SQLite, configuration parser, or model SDK.
package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hookclient"
)

func main() {
	// One request, one socket, then exit. Avoid a large scheduler for this
	// serial transport on many-core hosts; honor an explicit operator setting.
	// This changes only this process, not the separately spawned daemon.
	if os.Getenv("GOMAXPROCS") == "" {
		runtime.GOMAXPROCS(1)
	}
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(hookclient.Version)
		return
	}
	if (len(os.Args) != 3 && len(os.Args) != 4) || os.Args[1] != "hook" {
		return
	}
	agent := "claude"
	if len(os.Args) == 4 {
		agent = os.Args[3]
	}
	os.Exit(hookclient.MainAgent(os.Args[2], agent, os.Stdin, os.Stdout))
}
