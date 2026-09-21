package update

import (
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }

func TestGitConfigIsJailed(t *testing.T) {
	if got := os.Getenv("GIT_CONFIG_GLOBAL"); got != "/dev/null" {
		t.Fatalf("GIT_CONFIG_GLOBAL = %q, want /dev/null", got)
	}
	if got := os.Getenv("GIT_CONFIG_NOSYSTEM"); got != "1" {
		t.Fatalf("GIT_CONFIG_NOSYSTEM = %q, want 1", got)
	}
}
