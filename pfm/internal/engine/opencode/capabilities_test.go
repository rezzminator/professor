package opencode

import (
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/action"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestHeadlessPlannerPreservesCanonicalEngine(t *testing.T) {
	home := t.TempDir()
	machine := pfmconfig.Defaults(home, nil)
	machine.OpenCodeAccounts = []pfmconfig.OpenCodeAccount{{ID: 1, Home: home}}
	machine.OpenCode.Binary = "/fixture/custom-raw"
	plan, err := (HeadlessPlanner{}).Plan(
		action.HeadlessRequest{
			Name:           "RAW",
			CWD:            home,
			Home:           home,
			PrimaryAccount: 1,
			Config:         machine,
			Model:          "openai/gpt-6.1-sol",
			Effort:         "high",
			Prompt:         "Native initial task",
		},
	)
	if err != nil || plan.Engine != pfmengine.OpenCode || plan.SeatRun == nil ||
		plan.SeatRun.Binary != machine.OpenCode.Binary || !slices.Contains(plan.SeatRun.Args, "Native initial task") ||
		!slices.Contains(plan.SeatRun.Env, "PFM_OPENCODE_VARIANT=high") {
		t.Fatalf("canonical OpenCode planner=%+v err=%v", plan, err)
	}
}
