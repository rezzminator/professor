package mockengine

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setScenarioField(t *testing.T, fix *fixture, field string, value any) {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(readFile(t, fix.scenario)), &document); err != nil {
		t.Fatal(err)
	}
	document[field] = value
	content, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fix.scenario, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeOnlyScenarioDoors(t *testing.T) {
	for _, field := range []string{"quiet", "no_transcript"} {
		for _, engine := range []string{engineClaude, engineCodex, engineOpenCode} {
			for _, value := range []any{true, false, "yes", 1, nil} {
				t.Run(field+"/"+engine+"/"+string(mustJSON(t, value)), func(t *testing.T) {
					fix := newFixture(t)
					setScenarioField(t, fix, field, value)
					code, stdout, stderr := runOnce(fix, engine, []string{"--version"}, "")
					flag, boolean := value.(bool)
					if boolean && (!flag || engine == engineClaude) {
						if code != 0 || stdout == "" || stderr != "" {
							t.Fatalf("allowed door: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
						}
						return
					}
					if code != ExitUsage || stdout != "" || !strings.Contains(stderr, fix.scenario) ||
						!strings.Contains(stderr, field) || (boolean && !strings.Contains(stderr, "for "+engine)) {
						t.Fatalf("refused door: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
					}
				})
			}
		}
	}
}

func TestInlineDirectiveParser(t *testing.T) {
	for _, row := range []struct {
		name, prompt    string
		count           int
		refused, absent bool
	}{
		{"no marker", "plain prompt", 0, false, true},
		{"object", `hi mock-engine: {"type":"turn","reply":"R1"}`, 1, false, false},
		{"array", `mock-engine: [{"type":"background_agent","name":"a"},{"type":"turn","reply":"R2"}]`, 2, false, false},
		{"empty array", `mock-engine: []`, 0, false, false},
		{"first line", "mock-engine: {\"type\":\"turn\"}\nnext line", 1, false, false},
		{"compact", `mock-engine: {"type":"compact"}`, 1, false, false},
		{"unknown type", `mock-engine: {"type":"nope"}`, 0, true, false},
		{"unknown field", `mock-engine: {"type":"turn","oops":true}`, 0, true, false},
		{"broken JSON", `mock-engine: {"type":`, 0, true, false},
		{"trailing object", `mock-engine: {"type":"turn"} {}`, 0, true, false},
		{"unsigned footer", `mock-engine: {"type":"turn","reply":"R"} — UNSIGNED — sender identity underivable; no session id`, 1, false, false},
		{"signed footer", `mock-engine: [{"type":"turn","reply":"R"}] — sid 1234abcd · to reply: chat_inject lane <message>`, 1, false, false},
		{"force delivered footer", `mock-engine: {"type":"turn","reply":"R"} — ⚠ FORCE-DELIVERED via Esc (…) — sid 1234abcd`, 1, false, false},
		{"trailing garbage", `mock-engine: {"type":"turn"} garbage`, 0, true, false},
		{"null", `mock-engine: null`, 0, true, false},
		{"hold without gate", `mock-engine: {"type":"hold"}`, 0, true, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			steps, err := parseInlineSteps(row.prompt)
			if row.refused {
				if err == nil {
					t.Fatalf("bad directive accepted: %+v", steps)
				}
				if row.name == "trailing garbage" {
					reply := (&script{}).forPrompt(row.prompt).Steps[0].Reply
					if !strings.HasPrefix(reply, "mock-engine: inline steps refused — ") {
						t.Fatalf("refusal reply = %q", reply)
					}
				}
				return
			}
			if err != nil || len(steps) != row.count || (steps == nil) != row.absent {
				t.Fatalf("steps=%+v err=%v, want count=%d absent=%v", steps, err, row.count, row.absent)
			}
			if strings.Contains(row.name, "footer") && steps[0].Reply != "R" {
				t.Fatalf("footer step reply = %q, want R", steps[0].Reply)
			}
		})
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

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
		if _, err := LoadScenario(path, engineClaude); err == nil || !strings.Contains(err.Error(), path) {
			t.Fatalf("%s: err=%v, want a refusal naming %s", name, err, path)
		}
	}
	if _, err := LoadScenario(filepath.Join(dir, "absent.json"), engineClaude); err == nil {
		t.Fatal("a missing scenario loaded as something")
	}
	if scenario, err := LoadScenario("", engineClaude); err != nil || len(scenario.Steps) != 0 {
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
	got, err := LoadScenario(path, engineClaude)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "abc" || *got.BusyMS != 5 || got.Pane.Busy != "b" || got.Jail.ProcRoot != "/p" ||
		len(got.Steps) != 2 || got.Steps[1].UntilGone != "/g" {
		t.Fatalf("round trip = %+v", got)
	}
}

func TestScenarioRateLimitsRoundTripsAndRejectsNonObjects(t *testing.T) {
	fix := newFixture(t)
	setScenarioField(t, fix, "rate_limits", json.RawMessage(fixtureRateLimits))
	scenario, err := LoadScenario(fix.scenario, engineCodex)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(fix.root, "round-trip.json")
	if err := scenario.Write(path); err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readFile(t, path)), &fields); err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(fields["rate_limits"], &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(fixtureRateLimits), &want); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mustJSON(t, got), mustJSON(t, want)) {
		t.Fatalf("round trip=%s", fields["rate_limits"])
	}
	for _, bad := range []any{nil, 7, "limits", []any{}} {
		setScenarioField(t, fix, "rate_limits", bad)
		if _, err := LoadScenario(
			fix.scenario,
			engineCodex,
		); err == nil ||
			!strings.Contains(err.Error(), "rate_limits") {
			t.Fatalf("rate_limits %v: %v", bad, err)
		}
	}
}

func TestLaneDefaultScenarioLoadsForEveryEngine(t *testing.T) {
	path := filepath.Join("..", "..", "..", "infra", "fence", "lanes", "scenarios", "default.json")
	for _, engine := range []string{engineClaude, engineCodex, engineOpenCode} {
		scenario, err := LoadScenario(path, engine)
		if err != nil {
			t.Fatalf("%s default scenario: %v", engine, err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(mustJSON(t, scenario), &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields["rate_limits"]) == 0 || scenario.Pane.Composer == "" || scenario.Pane.Busy == "" ||
			scenario.Pane.Compacted == "" {
			t.Fatalf("%s default scenario lacks lane shapes: %+v", engine, scenario)
		}
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
