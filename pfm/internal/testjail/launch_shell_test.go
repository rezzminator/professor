package testjail

import (
	"errors"
	"os"
	"testing"
)

// TestPinLaunchShellPinsResolvedBashOrClears pins both branches: a resolved
// bash becomes CLAUDE_CODE_SHELL, and no bash clears an inherited value.
func TestPinLaunchShellPinsResolvedBashOrClears(t *testing.T) {
	t.Setenv(launchShellEnv, "/inherited/zsh")
	found := func(string) (string, error) { return "/fixture/bin/bash", nil }
	if err := pinLaunchShell(found); err != nil {
		t.Fatalf("pinLaunchShell(found): %v", err)
	}
	if got := os.Getenv(launchShellEnv); got != "/fixture/bin/bash" {
		t.Fatalf("%s=%q after a resolved bash, want /fixture/bin/bash", launchShellEnv, got)
	}
	missing := func(string) (string, error) { return "", errors.New("bash not on PATH") }
	if err := pinLaunchShell(missing); err != nil {
		t.Fatalf("pinLaunchShell(missing): %v", err)
	}
	if value, set := os.LookupEnv(launchShellEnv); set {
		t.Fatalf("%s=%q after no bash, want unset", launchShellEnv, value)
	}
}
