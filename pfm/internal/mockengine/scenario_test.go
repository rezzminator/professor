package mockengine

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	for name, tc := range map[string]struct{ body, want string }{
		"unknown field":     {`{"steps":[],"delay":3}`, "decode scenario {dir}/unknown-field.json: json: unknown field \"delay\""},
		"hold without gate": {`{"steps":[{"type":"hold"}]}`, "scenario {dir}/hold-without-gate.json step 0: hold needs until_gone"},
		"unknown step":      {`{"steps":[{"type":"dance"}]}`, "scenario {dir}/unknown-step.json step 0: unknown step type \"dance\""},
		"menu no options":   {`{"steps":[{"type":"menu"}]}`, "scenario {dir}/menu-no-options.json step 0: menu needs options"},
		"menu selected":     {`{"steps":[{"type":"menu","options":["a"],"selected":4}]}`, "scenario {dir}/menu-selected.json step 0: menu selected 4 is outside 1..1"},
		"tool no name":      {`{"steps":[{"type":"tool_call"}]}`, "scenario {dir}/tool-no-name.json step 0: tool_call needs tool"},
		"perform a read":    {`{"steps":[{"type":"tool_call","tool":"Read","perform":true}]}`, "scenario {dir}/perform-a-read.json step 0: tool_call performs only Bash and Write, not \"Read\""},
		"busy scenario":     {`{"busy_ms":200}`, "scenario {dir}/busy-scenario.json busy_ms 200: " + uncontrolled},
		"busy turn":         {`{"steps":[{"type":"turn","busy_ms":1}]}`, "scenario {dir}/busy-turn.json step 0 (turn): " + uncontrolled},
		"busy api error":    {`{"steps":[{"type":"api_error","busy_ms":5}]}`, "scenario {dir}/busy-api-error.json step 0 (api_error): " + uncontrolled},
		"hold":              {`{"steps":[{"type":"turn"},{"type":"hold","until_gone":"/g"}]}`, "scenario {dir}/hold.json step 1 (hold): " + uncontrolled},
		"gated agent":       {`{"steps":[{"type":"background_agent","name":"a","until_gone":"/g"}]}`, "scenario {dir}/gated-agent.json step 0 (background_agent): " + uncontrolled},
		"agent busy report": {`{"steps":[{"type":"background_agent","name":"a","busy_ms":4}]}`, "scenario {dir}/agent-busy-report.json step 0 (background_agent): " + uncontrolled},
		"crash":             {`{"steps":[{"type":"crash","exit_code":7}]}`, "scenario {dir}/crash.json step 0 (crash): " + uncontrolled},
		"compact":           {`{"steps":[{"type":"compact"}]}`, "scenario {dir}/compact.json step 0 (compact): " + uncontrolled},
		"launch crash":      {`{"launch_crash":"died"}`, "scenario {dir}/launch-crash.json launch_crash: " + uncontrolled},
		"no timed act":      {`{"busy_ms":0,"steps":[{"type":"turn"},{"type":"background_agent","name":"a"}]}`, "<nil>"},
		"controlled":        {`{"control":"/c","busy_ms":200,"launch_crash":"x","steps":[{"type":"hold","until_gone":"/g"},{"type":"crash"}]}`, "<nil>"},
	} {
		path := filepath.Join(dir, strings.ReplaceAll(name, " ", "-")+".json")
		if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadScenario(path, engineClaude)
		if got := strings.ReplaceAll(fmt.Sprint(err), dir, "{dir}"); got != tc.want {
			t.Errorf("%s: err = %q, want %q", name, got, tc.want)
		}
	}
	_, err := LoadScenario(filepath.Join(dir, "absent.json"), engineClaude)
	if got, want := strings.ReplaceAll(
		fmt.Sprint(err),
		dir,
		"{dir}",
	), "read scenario {dir}/absent.json: open {dir}/absent.json: no such file or directory"; got != want {
		t.Fatalf("absent scenario err = %q, want %q", got, want)
	}
	if scenario, err := LoadScenario("", engineClaude); err != nil || len(scenario.Steps) != 0 {
		t.Fatalf("empty path = (%+v, %v), want the default scenario", scenario, err)
	}
}

// uncontrolled is the refusal of a timed act in a scenario that names no control FIFO.
const uncontrolled = "is a timed act, and the scenario names no control FIFO to move its clock"

func TestScenarioRoundTripsThroughWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	busy := 5
	want := Scenario{
		SessionID: "abc", BusyMS: &busy, Pane: PaneShapes{Busy: "b"}, Jail: Jail{ProcRoot: "/p"}, Control: "/c",
		Steps: []Step{{Type: StepTurn, Reply: "r"}, {Type: StepHold, UntilGone: "/g"}},
	}
	if err := want.Write(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadScenario(path, engineClaude)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "abc" || *got.BusyMS != 5 || got.Control != "/c" || got.Pane.Busy != "b" ||
		got.Jail.ProcRoot != "/p" ||
		len(got.Steps) != 2 ||
		got.Steps[1].UntilGone != "/g" {
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
