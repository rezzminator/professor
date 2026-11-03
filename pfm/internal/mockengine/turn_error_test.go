package mockengine

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestErrorTurnKeepsEngineAlive(t *testing.T) {
	for _, engine := range []string{"claude", "codex", "opencode"} {
		t.Run(engine, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			capture := filepath.Join(fix.root, "failure.json")
			trace := filepath.Join(fix.root, "events")
			writeHookSettings(t, filepath.Join(fix.configDir, "settings.json"), map[string]any{
				"StopFailure": commandHook(`cat > "` + capture + `"; printf 'StopFailure\n' >> "` + trace + `"`),
				hookStop:      commandHook(`printf 'Stop\n' >> "` + trace + `"`),
			})
			fix.write(
				Scenario{
					SessionID: fixtureSession,
					Pane:      PaneShapes{Busy: "busy", Compacted: "compacted", Composer: "❯"},
					Steps: []Step{
						{Type: StepTurn, Error: "fixture API failure"}, {Type: StepTurn, Reply: "recovered"},
					},
				},
			)
			session := fix.startTUI(engine, nil, nil)
			session.waitFrame(
				"composer",
				func(s string) bool { return strings.Contains(s, "❯") || strings.Contains(s, "›") },
			)
			session.typeLine("fail turn")
			session.waitFrame(
				"failed API turn",
				func(s string) bool { return strings.Contains(s, "API Error: fixture API failure") },
			)
			if engine == engineClaude {
				if got := readFile(t, trace); got != "StopFailure\n" {
					t.Fatalf("%s: events=%q want StopFailure", trace, got)
				}
				var payload map[string]any
				if err := json.Unmarshal([]byte(readFile(t, capture)), &payload); err != nil {
					t.Fatal(err)
				}
				if payload["error"] != "unknown" || payload["error_details"] != "fixture API failure" ||
					payload["last_assistant_message"] != "API Error: fixture API failure" {
					t.Fatalf("%s: payload=%v", capture, payload)
				}
			}
			if engine == engineCodex {
				lines := strings.Split(strings.TrimSpace(readFile(t, fix.rolloutPath())), "\n")
				var record struct {
					Type    string `json:"type"`
					Payload struct {
						Type  string            `json:"type"`
						Last  any               `json:"last_agent_message"`
						Error map[string]string `json:"error"`
					} `json:"payload"`
				}
				if err := json.Unmarshal([]byte(lines[len(lines)-1]), &record); err != nil {
					t.Fatal(err)
				}
				if record.Type != "event_msg" || record.Payload.Type != "task_complete" ||
					record.Payload.Error["message"] != "fixture API failure" ||
					record.Payload.Error["codex_error_info"] != "other" || record.Payload.Last != nil {
					t.Fatalf("error record=%+v", record)
				}
			}
			session.typeLine("try again")
			session.waitFrame("next successful turn", func(s string) bool { return strings.Contains(s, "⏺ recovered") })
			if engine == engineClaude {
				if got := readFile(t, trace); got != "StopFailure\nStop\n" {
					t.Fatalf("%s: events=%q", trace, got)
				}
			}
		})
	}
}

func TestOpenCodeErrorEventPrecedesIdle(t *testing.T) {
	_, session, receipts, _ := openCodePluginFixture(t)
	if err := session.turnError(Step{Error: "fixture API failure"}); err != nil {
		t.Fatal(err)
	}
	want := `{"type":"session.error","properties":{"error":{"data":{"message":"fixture API failure"},"name":"UnknownError"},"sessionID":"ses_atlas01"}}`
	lines := strings.Split(strings.TrimSpace(readFile(t, receipts)), "\n")
	var gotValue, wantValue any
	if err := json.Unmarshal([]byte(lines[0]), &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(gotValue)
	expected, _ := json.Marshal(wantValue)
	if !bytes.Equal(got, expected) {
		t.Fatalf("%s: event=%s want=%s", receipts, got, expected)
	}
}
