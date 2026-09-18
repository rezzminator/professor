// Package main is mock-engine, the test-only stand-in binary for claude, codex
// and opencode. A test installs it under one of those names on PATH (symlink or
// copy) and pfm drives it exactly as it drives the real engine; the name it
// was invoked by selects the engine and MOCK_ENGINE_SCENARIO scripts the
// behaviour. Everything lives in internal/mockengine; this is the exec shell.
package main

import (
	"context"
	"os"

	"hostops/pfm/internal/mockengine"
)

func main() {
	os.Exit(mockengine.Main(context.Background(), os.Args[0], os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}
