package mockengine

import (
	"testing"
)

func TestScriptRefusesUnscriptedRequests(t *testing.T) {
	running := newScript(Scenario{Steps: []Step{{Type: StepTurn, Reply: "first", BusyMS: 9}}}, engineClaude)
	if running.Model != DefaultClaudeModel || running.Version != DefaultClaudeVersion || running.Reply != DefaultReply {
		t.Fatalf("defaults = %+v", running.Scenario)
	}
	reply, busy, usage := running.turnReply(running.next())
	if reply != "first" || busy != 9 || usage.Input == 0 {
		t.Fatalf("first turn = (%q, %d, %+v)", reply, busy, usage)
	}
	step := running.next()
	if step.Type != StepCrash || step.ExitCode != ExitUnpinned ||
		step.Reply != "mock-engine: unscripted request: scenario exhausted" {
		t.Fatalf("exhausted list gave %+v", step)
	}
	for _, step := range []Step{{Type: StepTurn}, {Type: StepMenu}, {Type: StepCrash}, {Type: StepExit}} {
		if !step.terminal() {
			t.Fatalf("%s must end a turn", step.Type)
		}
	}
	for _, step := range []Step{{Type: StepHold}, {Type: StepToolCall}, {Type: StepBackgroundAgent}, {Type: StepCompact}, {Type: StepMCP}} {
		if step.terminal() {
			t.Fatalf("%s must run inside a turn", step.Type)
		}
	}
}
