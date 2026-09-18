package mockengine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadScenarioRefusesUnknownFieldsAndBrokenSteps(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"unknown field":     `{"steps":[],"delay":3}`,
		"hold without gate": `{"steps":[{"type":"hold"}]}`,
		"unknown step":      `{"steps":[{"type":"dance"}]}`,
		"menu no options":   `{"steps":[{"type":"menu"}]}`,
		"menu selected":     `{"steps":[{"type":"menu","options":["a"],"selected":4}]}`,
		"tool no name":      `{"steps":[{"type":"tool_call"}]}`,
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadScenario(path); err == nil || !strings.Contains(err.Error(), path) {
			t.Fatalf("%s: err=%v, want a refusal naming %s", name, err, path)
		}
	}
	if _, err := LoadScenario(filepath.Join(dir, "absent.json")); err == nil {
		t.Fatal("a missing scenario loaded as something")
	}
	if scenario, err := LoadScenario(""); err != nil || len(scenario.Steps) != 0 {
		t.Fatalf("empty path = (%+v, %v), want the default scenario", scenario, err)
	}
}

func TestScenarioRoundTripsThroughWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	busy := 5
	want := Scenario{
		SessionID: "abc", BusyMS: &busy, Pane: PaneShapes{Busy: "b"}, Jail: Jail{ProcRoot: "/p"},
		Steps: []Step{{Type: StepTurn, Reply: "r"}, {Type: StepHold, UntilGone: "/g"}},
	}
	if err := want.Write(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadScenario(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "abc" || *got.BusyMS != 5 || got.Pane.Busy != "b" || got.Jail.ProcRoot != "/p" ||
		len(got.Steps) != 2 || got.Steps[1].UntilGone != "/g" {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestScriptFallsBackToDefaultsWhenStepsRunOut(t *testing.T) {
	running := newScript(Scenario{Steps: []Step{{Type: StepTurn, Reply: "first", BusyMS: 9}}}, engineClaude)
	if running.Model != DefaultClaudeModel || running.Version != DefaultClaudeVersion || running.Reply != DefaultReply {
		t.Fatalf("defaults = %+v", running.Scenario)
	}
	reply, busy, usage := running.turnReply(running.next())
	if reply != "first" || busy != 9 || usage.Input == 0 {
		t.Fatalf("first turn = (%q, %d, %+v)", reply, busy, usage)
	}
	step := running.next()
	reply, busy, _ = running.turnReply(step)
	if step.Type != StepTurn || reply != DefaultReply || busy != DefaultBusyMS {
		t.Fatalf("exhausted list gave (%+v, %q, %d)", step, reply, busy)
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
