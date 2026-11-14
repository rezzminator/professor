//go:build jail

package update

// Real git is the subject here: the jail's environment must reach a real git
// process, which the fake git in testmain_test.go never starts.

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestGitConfigIsJailed(t *testing.T) {
	if got := os.Getenv("GIT_CONFIG_GLOBAL"); got != "/dev/null" {
		t.Fatalf("GIT_CONFIG_GLOBAL = %q, want /dev/null", got)
	}
	if got := os.Getenv("GIT_CONFIG_NOSYSTEM"); got != "1" {
		t.Fatalf("GIT_CONFIG_NOSYSTEM = %q, want 1", got)
	}
	cmd := exec.Command("git", "config", "--get", "maintenance.auto")
	cmd.Dir = t.TempDir() // a fenced worktree's gitdir is unreadable; the env config is what's under test
	out, err := cmd.Output()
	if got := strings.TrimSpace(string(out)); err != nil || got != "false" {
		t.Fatalf("git config maintenance.auto = %q (err %v), want false", got, err)
	}
}
