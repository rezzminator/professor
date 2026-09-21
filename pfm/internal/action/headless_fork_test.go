package action

import (
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestHeadlessForkCodexUsesRealForkAndSelectedAccount(t *testing.T) {
	machine := pfmconfig.Defaults("/tmp/fork-home", nil, "/tmp/fork-home/.codex")
	machine.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: "/tmp/fork-home/.codex"}}
	plan, err := HeadlessFork(HeadlessForkRequest{
		Engine: pfmengine.Codex, SessionID: "thread-123", Name: "review fork",
		CWD: "/work/project", Home: "/tmp/fork-home", PrimaryAccount: 1,
		Config: machine,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"-u CODEX_THREAD_ID", "CODEX_HOME=", " 'fork' 'thread-123'"} {
		if !strings.Contains(plan.Run, fragment) {
			t.Fatalf("fork command %q lacks %q", plan.Run, fragment)
		}
	}
	if strings.Contains(plan.Run, "resume") {
		t.Fatalf("Codex fork command used resume: %q", plan.Run)
	}
}
