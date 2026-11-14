package action

import (
	"path/filepath"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestLauncherRunNativeCursorFollowsConfig(t *testing.T) {
	for _, want := range []bool{false, true} {
		run, err := LauncherRun(
			"/bin/claude",
			nil,
			"",
			"/home/test",
			pfmconfig.Config{},
			pfmconfig.ClaudePrefs{NativeCursor: want},
			"",
		)
		if err != nil {
			t.Fatalf("LauncherRun(nativeCursor=%v) error = %v", want, err)
		}
		if got := parsedShell(t, run).SettingsEnv["CLAUDE_CODE_NATIVE_CURSOR"] == "1"; got != want {
			t.Fatalf("nativeCursor=%v: settings present=%v", want, got)
		}
	}
}

func TestClaudeSpawnEnvironmentNativeCursorFollowsConfig(t *testing.T) {
	// A real executable, so ClaudeSpawn.Command resolves the binary.
	claudeBinary := filepath.Join(t.TempDir(), "claude")
	if err := testjail.WriteExecutableNeverRun(claudeBinary, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("stage claude stand-in %s: %v", claudeBinary, err)
	}
	for _, want := range []bool{false, true} {
		spawn := ClaudeSpawn{
			Purpose: PurposeInteractive,
			Home:    "/home/test",
			Machine: pfmconfig.Config{Claude: pfmconfig.ClaudePrefs{NativeCursor: want}},
			binary:  claudeBinary,
		}
		if got := parsedSpawn(t, spawn).SettingsEnv["CLAUDE_CODE_NATIVE_CURSOR"] == "1"; got != want {
			t.Fatalf("nativeCursor=%v: settings present=%v", want, got)
		}
	}
}
