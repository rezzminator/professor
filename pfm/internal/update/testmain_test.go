package update

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMain(m *testing.M) {
	code := testjail.Run(m)
	if err := removeUpdateFixtureTemplates(); err != nil {
		fmt.Fprintf(os.Stderr, "update tests: remove fixture templates: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

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
