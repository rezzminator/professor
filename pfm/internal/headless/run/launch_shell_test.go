package run

import (
	"os"
	"slices"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/loginshell"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// A headless Claude run's PATH is the environment's own, judged against that
// environment itself, with the login shell's folders merged in as the login
// PATH orders them; the inherited PATH entry is replaced, never duplicated.
func TestWithLaunchShellMergesTheLoginPATH(t *testing.T) {
	const loginShell = "/login/bin/zsh"
	runner := &deps.FakeRunner{}
	runner.ScriptLookPath("bash", "/scripted/bin/bash", nil)
	loginshell.ScriptAccount(runner, os.Getuid(), loginShell, nil)
	// The markers claudelaunch's probe prints around the login shell's PATH.
	runner.Script([]string{loginShell}, deps.RunResult{
		Stdout: []byte("__PFM_LOGIN_PATH_START__/shims:/u/.local/bin:/usr/bin:/opt/x__PFM_LOGIN_PATH_END__"),
	}, nil)
	environment := []string{"PATH=/u/.local/bin:/usr/bin:/bin", "HOME=/h", "SHELL=" + loginShell}
	testwait.ProcessClock(t, clock.NewFake(time.Unix(0, 0)))
	got := withLaunchShellThrough(environment, runner)
	want := []string{
		"HOME=/h", "SHELL=" + loginShell, "CLAUDE_CODE_SHELL=/scripted/bin/bash",
		"PATH=/shims:/u/.local/bin:/usr/bin:/bin:/opt/x",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("withLaunchShell(%q) = %q, want %q", environment, got, want)
	}
}
