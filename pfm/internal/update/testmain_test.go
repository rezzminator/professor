package update

import (
	"fmt"
	"os"
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
}
