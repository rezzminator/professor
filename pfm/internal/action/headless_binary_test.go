package action

import (
	"strings"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
)

// A plan names the executable word it embeds so the spawn layer can prove the
// binary is reachable before a tmux server is created around it. A pane whose
// first breath is "command not found" takes the fresh server down with it,
// and from the outside that reads as tmux trouble with the engine never named
// — the silence a systemd-default PATH produced around `claude` on a live box.
func TestHeadlessPlansCarryTheEngineBinaryWord(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		engine pfmengine.ID
		want   string
	}{
		{"claude", pfmengine.Claude, "claude"},
		{"codex", pfmengine.Codex, "codex"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			plan, err := headlessWithTestConfig(HeadlessRequest{
				Engine:         testCase.engine,
				Name:           "worker",
				CWD:            "/work/alpha",
				Home:           "/home/tester",
				PrimaryAccount: 1,
			})
			if err != nil {
				t.Fatalf("HeadlessRun() = %v", err)
			}
			if plan.Binary == "" {
				t.Fatal("plan carries no Binary word; spawn cannot preflight it")
			}
			if !strings.Contains(plan.Binary, testCase.want) {
				t.Fatalf("plan.Binary = %q, want the %q word", plan.Binary, testCase.want)
			}
			if !strings.Contains(plan.Run, plan.Binary) {
				t.Fatalf(
					"plan.Run = %q does not contain plan.Binary = %q — the preflight would prove a different file than the pane runs",
					plan.Run,
					plan.Binary,
				)
			}
		})
	}
}

func TestHeadlessForkCarriesTheEngineBinaryWord(t *testing.T) {
	plan, err := HeadlessFork(HeadlessForkRequest{
		Engine:         pfmengine.Claude,
		SessionID:      "b1111111-1111-4111-8111-111111111111",
		Name:           "fork",
		CWD:            "/work/alpha",
		Home:           "/home/tester",
		PrimaryAccount: 1,
		Config:         testMachineConfig("/home/tester"),
	})
	if err != nil {
		t.Fatalf("HeadlessFork() = %v", err)
	}
	if plan.Binary == "" {
		t.Fatal("fork plan carries no Binary word; spawn cannot preflight it")
	}
	if !strings.Contains(plan.Run, plan.Binary) {
		t.Fatalf("plan.Run = %q does not contain plan.Binary = %q", plan.Run, plan.Binary)
	}
}
