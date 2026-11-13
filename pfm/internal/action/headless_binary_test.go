package action

import (
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// Every headless plan names its engine binary as one word of its Run, so the
// spawn preflight proves the file the pane runs.
func TestHeadlessPlansCarryTheEngineBinaryWord(t *testing.T) {
	home := t.TempDir()
	claude, claudeErr := headlessWithTestConfig(HeadlessRequest{
		Engine: pfmengine.Claude, Name: "worker", CWD: "/work/alpha", Home: home, PrimaryAccount: 1,
	})
	codex, codexErr := headlessWithTestConfig(HeadlessRequest{
		Engine: pfmengine.Codex, Name: "worker", CWD: "/work/alpha", Home: home, PrimaryAccount: 1,
	})
	fork, forkErr := HeadlessFork(HeadlessForkRequest{
		Engine: pfmengine.Claude, SessionID: "b1111111-1111-4111-8111-111111111111", Name: "fork",
		CWD: "/work/alpha", Home: home, PrimaryAccount: 1, Config: testMachineConfig(home),
	})
	for _, plan := range []struct {
		name, want string
		plan       HeadlessPlan
		err        error
	}{
		{"claude", "claude", claude, claudeErr},
		{"codex", "codex", codex, codexErr},
		{"fork", "claude", fork, forkErr},
	} {
		words, err := cmdparse.SplitShellWords(plan.plan.Run)
		if plan.err != nil || err != nil {
			t.Fatalf("%s: plan error %v, run words error %v", plan.name, plan.err, err)
		}
		if got := rooted(t, plan.plan.Binary); got != plan.want || !slices.Contains(words, plan.plan.Binary) {
			t.Errorf(
				"%s: Binary = %q (a Run word: %t), want %q",
				plan.name,
				got,
				slices.Contains(words, plan.plan.Binary),
				plan.want,
			)
		}
	}
}
