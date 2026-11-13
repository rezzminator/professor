package claudelaunch

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// shellPATH puts a directory holding a file with an executable mode named
// `bash` alone on PATH and returns its absolute path; nothing runs it.
func shellPATH(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bash := filepath.Join(dir, "bash")
	if err := testjail.WriteExecutableNeverRun(bash, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake bash: %v", err)
	}
	t.Setenv("PATH", dir)
	return bash
}

// bashRunner is a runner whose LookPath finds bash at path.
func bashRunner(path string) *deps.FakeRunner {
	runner := &deps.FakeRunner{}
	runner.ScriptLookPath("bash", path, nil)
	return runner
}

// unsetShell clears CLAUDE_CODE_SHELL for the test; t.Setenv restores it.
func unsetShell(t *testing.T) {
	t.Helper()
	t.Setenv("CLAUDE_CODE_SHELL", "")
	if err := os.Unsetenv("CLAUDE_CODE_SHELL"); err != nil {
		t.Fatalf("unset CLAUDE_CODE_SHELL: %v", err)
	}
}

func renderShellEnv(t *testing.T, runner deps.Runner) []string {
	t.Helper()
	home, machine := renderMachine(t)
	launch, err := Render(Request{Purpose: PurposeInteractive, Home: home, Runner: runner}, machine)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	var shell []string
	for _, entry := range launch.Env {
		if strings.HasPrefix(entry, "CLAUDE_CODE_SHELL=") {
			shell = append(shell, entry)
		}
	}
	return shell
}

func TestRenderShellUnsetTakesBashFromPATH(t *testing.T) {
	bash := "/scripted/bin/bash"
	unsetShell(t)
	got, want := renderShellEnv(t, bashRunner(bash)), []string{"CLAUDE_CODE_SHELL=" + bash}
	if !slices.Equal(got, want) {
		t.Fatalf("shell env = %q, want %q", got, want)
	}
	bash = shellPATH(t)
	if got := SessionEnv(pfmconfig.ClaudePrefs{}); !slices.Contains(got, "CLAUDE_CODE_SHELL="+bash) {
		t.Fatalf("SessionEnv = %q, want CLAUDE_CODE_SHELL=%s", got, bash)
	}
}

func TestRenderShellPresetIsKept(t *testing.T) {
	preset := filepath.Join(t.TempDir(), "zsh")
	if err := testjail.WriteExecutableNeverRun(preset, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write preset shell: %v", err)
	}
	t.Setenv("CLAUDE_CODE_SHELL", preset)
	_, recorder := obs.Test(t)
	if got := renderShellEnv(t, bashRunner("/scripted/bin/bash")); len(got) != 0 {
		t.Fatalf("shell env = %q, want the inherited %s left untouched", got, preset)
	}
	if strings.Contains(recorder.Raw(), "CLAUDE_CODE_SHELL") {
		t.Fatalf("a valid preset was warned about: %s", recorder.Raw())
	}
}

func TestRenderShellInvalidPresetIsReportedAndReplaced(t *testing.T) {
	bash := "/scripted/bin/bash"
	for _, preset := range []string{"bash", filepath.Join(t.TempDir(), "missing-bash")} {
		t.Run(preset, func(t *testing.T) {
			t.Setenv("CLAUDE_CODE_SHELL", preset)
			_, recorder := obs.Test(t)
			got, want := renderShellEnv(t, bashRunner(bash)), []string{"CLAUDE_CODE_SHELL=" + bash}
			if !slices.Equal(got, want) {
				t.Fatalf("shell env = %q, want %q", got, want)
			}
			if !strings.Contains(recorder.Raw(), preset) {
				t.Fatalf("invalid preset %q not reported: %s", preset, recorder.Raw())
			}
		})
	}
}

func TestRenderShellNoBashWarnsAndLaunches(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	unsetShell(t)
	_, recorder := obs.Test(t)
	if got := renderShellEnv(t, &deps.FakeRunner{}); len(got) != 0 {
		t.Fatalf("shell env = %q, want none without a bash", got)
	}
	if raw := recorder.Raw(); !strings.Contains(raw, "no bash on PATH") {
		t.Fatalf("missing bash not warned: %s", raw)
	}
	_, machine := renderMachine(t)
	for _, row := range Resolve(machine, 0) {
		if row.Knob.Name == "shell" && !strings.Contains(row.Value, "no bash on PATH") {
			t.Fatalf("pfm config shell value = %q, want the reason", row.Value)
		}
	}
}
