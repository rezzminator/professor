//go:build jail

package claudelaunch

import (
	"slices"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

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
