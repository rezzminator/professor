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
		"unknown field":             `{"steps":[],"delay":3}`,
		"hold without gate":         `{"steps":[{"type":"hold"}]}`,
		"unknown step":              `{"steps":[{"type":"dance"}]}`,
		"menu no options":           `{"steps":[{"type":"menu"}]}`,
		"menu selected":             `{"steps":[{"type":"menu","options":["a"],"selected":4}]}`,
		"tool no name":              `{"steps":[{"type":"tool_call"}]}`,
		"ready relative":            `{"ready_hold":"relative"}`,
		"stdout relative":           `{"app_server_stdout_hold":"relative"}`,
		"ready timeout zero":        `{"ready_timeout_ms":0}`,
		"ready timeout negative":    `{"ready_timeout_ms":-1}`,
		"boot exit negative":        `{"startup_exit_code":-1}`,
		"boot exit too large":       `{"startup_exit_code":256}`,
		"turn quota null":           `{"steps":[{"type":"turn","rate_limits":null}]}`,
		"turn quota scalar":         `{"steps":[{"type":"turn","rate_limits":7}]}`,
		"native title missing name": `{"steps":[{"type":"native_title"}]}`,
		"handoff missing id":        `{"steps":[{"type":"background_handoff"}]}`,
		"handoff path id":           `{"steps":[{"type":"background_handoff","session_id":"../escape"}]}`,
		"idle zero count":           `{"idle_statusline_refresh":{"count":0,"interval_ms":1}}`,
		"idle zero interval":        `{"idle_statusline_refresh":{"count":1,"interval_ms":0}}`,
		"idle relative stop":        `{"idle_statusline_refresh":{"count":1,"interval_ms":1,"stop_when_exists":"relative"}}`,
		"gate relative path":        `{"hook_gates":[{"event":"SessionStart","command":"true","until_gone":"relative"}]}`,
		"during turn":               `{"steps":[{"type":"turn","during":[{"type":"notification"}]}]}`,
		"during tool":               `{"steps":[{"type":"question","during":[{"type":"tool_call","tool":"Write"}]}]}`,
		"during nested":             `{"steps":[{"type":"permission","tool":"Bash","during":[{"type":"notification","during":[{"type":"notification"}]}]}]}`,
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
		SessionID:             "abc",
		BusyMS:                &busy,
		Pane:                  PaneShapes{Busy: "b"},
		Jail:                  Jail{ProcRoot: "/p"},
		ReadyHold:             "/gate",
		ReadyTimeoutMS:        17,
		StartupExitCode:       intPtr(9),
		AppServerStdoutHold:   "/stdout",
		TrustDialogUnnumbered: true,
		IdleStatuslineRefresh: &statuslineRefresh{Count: 1, IntervalMS: 1},
		HookGates:             []hookGate{{Event: hookSessionStart, Command: "true", UntilGone: "/gate"}},
		Steps: []Step{
			{Type: StepTurn, Reply: "r", RateLimits: json.RawMessage(`{}`)},
			{Type: StepHold, UntilGone: "/g"},
			{Type: StepBackgroundHandoff, SessionID: fixtureSession},
			{Type: StepQuestion, During: []Step{{Type: StepHold, UntilGone: "/during"}, {Type: StepNotification}}},
		},
	}
	if err := want.Write(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadScenario(path, engineClaude)
	if err != nil {
		t.Fatal(err)
	}
	if got.SessionID != "abc" || *got.BusyMS != 5 || got.Pane.Busy != "b" || got.Jail.ProcRoot != "/p" ||
		len(got.Steps) != 4 || got.Steps[1].UntilGone != "/g" || string(got.Steps[0].RateLimits) != "{}" ||
		got.ReadyHold != "/gate" || got.ReadyTimeoutMS != 17 || got.StartupExitCode == nil || *got.StartupExitCode != 9 ||
		got.AppServerStdoutHold != "/stdout" || !got.TrustDialogUnnumbered || len(got.Steps[3].During) != 2 || got.Steps[3].During[0].UntilGone != "/during" ||
		got.Steps[2].SessionID != fixtureSession || got.IdleStatuslineRefresh == nil || *got.IdleStatuslineRefresh != (statuslineRefresh{Count: 1, IntervalMS: 1}) {
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
