package claudelaunch

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/loginshell"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
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

// bashRunner is a runner whose LookPath finds bash at path and whose login
// shell adds nothing to a launch's PATH (loginRunner).
func bashRunner(path string) *deps.FakeRunner {
	runner := loginRunner(pinnedLoginShell)
	runner.ScriptLookPath("bash", path, nil)
	return runner
}

// pinnedLoginShell is the account login shell loginRunner scripts.
const pinnedLoginShell = "/pinned/bin/zsh"

// loginRunner is a runner whose account login shell is shell, printing entries
// as its PATH: none adds nothing, so a test of something else neither runs a
// shell nor warns.
func loginRunner(shell string, entries ...string) *deps.FakeRunner {
	runner := &deps.FakeRunner{}
	loginshell.ScriptAccount(runner, os.Getuid(), shell, nil)
	runner.Script([]string{shell, "-l", "-i", "-c", loginPATHScript}, deps.RunResult{
		Stdout: []byte(loginPATHStart + strings.Join(entries, ":") + loginPATHEnd),
	}, nil)
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
	testwait.ProcessClock(t, clock.NewFake(time.Unix(0, 0)))
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
	if got, want := recordMessages(recorder.Records()), ""; got != want {
		t.Fatalf("records = %q, want %q", got, want)
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
	if got := renderShellEnv(t, loginRunner(pinnedLoginShell)); len(got) != 0 {
		t.Fatalf("shell env = %q, want none without a bash", got)
	}
	value := ""
	_, machine := renderMachine(t)
	for _, row := range Resolve(machine, 0) {
		if row.Knob.Name == "shell" {
			value = row.Value
		}
	}
	if got, want := recordMessages(
		recorder.Records(),
	)+" | "+value, "claudelaunch: launch shell runner.lookpath | no bash on PATH ("+testjail.NotOnPath(
		"bash",
	)+"): CLAUDE_CODE_SHELL left unset, Claude Code's Bash tool runs under the login shell"; got != want {
		t.Fatalf("records | shell value = %q, want %q", got, want)
	}
}

// recordMessages is each record's message in log order.
func recordMessages(records []obs.Record) string {
	messages := make([]string, 0, len(records))
	for _, record := range records {
		messages = append(messages, record.Message)
	}
	return strings.Join(messages, " ")
}

// A persona's env is the base of the launch's shell and PATH: its PATH is the
// one the login entries merge into, written as it is; a usable
// CLAUDE_CODE_SHELL of its own stays. Without a persona PATH, LoginPATH names
// the login entries added, which a pane command adds to its shell's own PATH.
func TestRenderPersonaEnvIsTheMergeBase(t *testing.T) {
	const loginShell = "/login/bin/zsh"
	personaBash := filepath.Join(t.TempDir(), "bash")
	if err := testjail.WriteExecutableNeverRun(personaBash, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write persona shell: %v", err)
	}
	for _, test := range []struct {
		name      string
		envs      map[string]string
		want      []string
		loginPATH []string
	}{
		{
			name: "persona PATH is the base",
			envs: map[string]string{"PATH": "/persona/bin"},
			want: []string{"PATH=/persona/bin:/opt/mise/shims", "CLAUDE_CODE_SHELL=/scripted/bin/bash"},
		},
		{
			name:      "persona shell is kept",
			envs:      map[string]string{"CLAUDE_CODE_SHELL": personaBash},
			want:      []string{"CLAUDE_CODE_SHELL=" + personaBash, "PATH=/pfm/own/bin:/persona/bin:/opt/mise/shims"},
			loginPATH: []string{"/persona/bin", "/opt/mise/shims"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			unsetShell(t)
			t.Setenv("SHELL", loginShell)
			t.Setenv("PATH", "/pfm/own/bin")
			runner := loginRunner(loginShell, "/persona/bin", "/opt/mise/shims")
			runner.ScriptLookPath("bash", "/scripted/bin/bash", nil)
			testwait.ProcessClock(t, clock.NewFake(time.Unix(0, 0)))
			home, machine := renderMachine(t)
			launch, err := Render(Request{
				Purpose: PurposeInteractive, Home: home, Envs: test.envs, Runner: runner,
			}, machine)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			got := []string{}
			for _, entry := range launch.Env {
				if strings.HasPrefix(entry, "PATH=") || strings.HasPrefix(entry, "CLAUDE_CODE_SHELL=") {
					got = append(got, entry)
				}
			}
			slices.Sort(got)
			want := slices.Clone(test.want)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("PATH and shell env = %q, want %q", got, want)
			}
			if !slices.Equal(launch.LoginPATH, test.loginPATH) {
				t.Fatalf("LoginPATH = %q, want %q", launch.LoginPATH, test.loginPATH)
			}
		})
	}
}
