package action

import (
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

type builtinTestPlanner struct{ id pfmengine.ID }

func (planner builtinTestPlanner) Plan(request HeadlessRequest) (HeadlessPlan, error) {
	if planner.id == pfmengine.Codex {
		return PlanCodex(request)
	}
	return PlanClaude(request)
}

func init() {
	RegisterPlanner(pfmengine.Claude, builtinTestPlanner{id: pfmengine.Claude})
	RegisterPlanner(pfmengine.Codex, builtinTestPlanner{id: pfmengine.Codex})
}

func TestUnknownEngineIsANamedError(t *testing.T) {
	_, err := PlannerFor(pfmengine.ID("zz"))
	if err == nil || err.Error() != "engine zz: no headless planner registered" {
		t.Fatalf("PlannerFor(zz) error = %v", err)
	}
}

func TestKnownUnsupportedEngineGetsProductFacingError(t *testing.T) {
	_, err := PlannerFor(pfmengine.OpenCode)
	if err == nil || err.Error() != "OpenCode does not support headless chat" {
		t.Fatalf("PlannerFor(OpenCode) error = %v", err)
	}
}

func TestPlanCodexRejectsUnknownEffortBeforeBuildingTOMLOverride(t *testing.T) {
	home := t.TempDir()
	machine := pfmconfig.Defaults(home, nil)
	machine.CodexAccounts = []pfmconfig.CodexAccount{{ID: 1, Home: home + "/.codex"}}
	_, err := PlanCodex(HeadlessRequest{
		Name: "seat", CWD: home, Home: home, PrimaryAccount: 1, Config: machine,
		Effort: `high"\napproval_policy="never`,
	})
	if err == nil {
		t.Fatal("PlanCodex accepted an unknown/injectable effort")
	}
}
