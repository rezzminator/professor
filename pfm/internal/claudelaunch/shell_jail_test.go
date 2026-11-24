//go:build jail

package claudelaunch

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/loginshell"
)

// TestRenderShellUnsetTakesBashFromPATH also runs that real bash as the
// account's login shell (the account lookup answers it, whatever the host's
// passwd says): the directory its ~/.profile prepends reaches a launch whose PATH
// lacks it, ahead of the launch's folder when the login PATH holds that folder
// after it, else right after the launch's PATH.
func TestRenderShellUnsetTakesBashFromPATH(t *testing.T) {
	realBash, err := deps.RealRunner{}.LookPath("bash")
	if err != nil {
		t.Fatalf("no bash for the login shell: %v", err)
	}
	bash := "/scripted/bin/bash"
	unsetShell(t)
	got, want := renderShellEnv(t, bashRunner(bash)), []string{"CLAUDE_CODE_SHELL=" + bash}
	if !slices.Equal(got, want) {
		t.Fatalf("shell env = %q, want %q", got, want)
	}
	bash = shellPATH(t)
	if got := SessionEnv(nil); !slices.Contains(got, "CLAUDE_CODE_SHELL="+bash) {
		t.Fatalf("SessionEnv = %q, want CLAUDE_CODE_SHELL=%s", got, bash)
	}

	home := t.TempDir()
	shims := filepath.Join(home, "login shims")
	profile := "PATH=\"$HOME/login shims:$PATH\"\nexport PATH\necho profile noise\n"
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte(profile), 0o644); err != nil {
		t.Fatalf("write .profile: %v", err)
	}
	t.Setenv("HOME", home)
	launch := filepath.Dir(realBash)
	account := &deps.FakeRunner{}
	loginshell.ScriptAccount(account, os.Getuid(), realBash, nil)
	account.Handle([]string{realBash}, func(ctx context.Context, call deps.RunCall) (deps.RunResult, error) {
		return deps.RealRunner{}.Run(ctx, call.Argv, call.Opts)
	})
	env := ShellEnvThrough(
		envLookup(map[string]string{"SHELL": realBash, "PATH": launch, "CLAUDE_CODE_SHELL": realBash}), account,
	)
	var entries []string
	if len(env) == 1 {
		if value, found := strings.CutPrefix(env[0], "PATH="); found {
			entries = strings.Split(value, ":")
		}
	}
	first, second := launch, shims
	if slices.Contains(strings.Split(loginBasePATH, ":"), launch) {
		first, second = shims, launch
	}
	if len(entries) < 2 || entries[0] != first || entries[1] != second ||
		slices.Index(entries[2:], shims) != -1 || slices.Index(entries[2:], launch) != -1 {
		t.Fatalf("ShellEnv = %q, want one PATH=%s:%s, then the login PATH's other entries the launch lacks (HOME %s)",
			env, first, second, home)
	}
}
