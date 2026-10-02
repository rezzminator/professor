// Package main is mock-engine, the test-only stand-in binary for claude, codex
// and opencode. A test installs it under one of those names on PATH (symlink or
// copy) and pfm drives it exactly as it drives the real engine; the name it
// was invoked by selects the engine and MOCK_ENGINE_SCENARIO scripts the
// behaviour. Everything lives in internal/mockengine; this is the exec shell.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/mockengine"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGHUP, syscall.SIGTERM)
	code := mockengine.Main(ctx, os.Args[0], os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv)
	stop()
	os.Exit(code)
}
