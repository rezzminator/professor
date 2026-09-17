package main

import (
	"fmt"
	"io"
	"os"

	"hostops/pfm/internal/deps"
)

// Older sourced shims and already-rendered tmux commands still call this entry
// point. Keep their argv/account behavior; native hooks now own prompt delivery.
func runCodexLaunchCompatibility(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: pfm internal codex-launch BINARY [arguments...]")
		return 2
	}
	binary, err := deps.Resolve(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "resolve Codex launcher: %v\n", err)
		return 1
	}
	if err := launchExec(binary, append([]string{binary}, args[1:]...), os.Environ()); err != nil {
		fmt.Fprintf(stderr, "launch Codex: %v\n", err)
		return 1
	}
	return 0
}
