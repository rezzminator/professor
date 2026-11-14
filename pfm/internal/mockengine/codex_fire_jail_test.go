//go:build jail

package mockengine

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A real Codex runs a hooks.json handler only while config.toml's hooks.state trusts it at its current
// hash, and fires each event at its beat: SessionStart, UserPromptSubmit, PreToolUse and PostToolUse
// around an allowed tool call, Stop at the turn's end, SessionEnd as the pane ends. A trusted PreToolUse
// deny refuses the call before it runs, and the rollout's output for it is the reason.
func TestCodexFiresItsTrustedHooksAtEachBeat(t *testing.T) {
	type outcome struct {
		events  string
		outputs map[string]string
		row     string
	}
	for _, row := range []struct {
		name  string
		trust []bool
		want  outcome
	}{
		{
			"trusted",
			[]bool{true, true, true, true, true, true, true},
			outcome{
				"SessionStart\nUserPromptSubmit\nPreToolUse\nPostToolUse\nPreToolUse\nStop\nSessionEnd\n",
				map[string]string{"printf performed": "performed", "printf refused": "no git writes"},
				"⏺ Bash — denied by hook: no git writes",
			},
		},
		{
			"untrusted",
			[]bool{false, false, false, false, false, false, false},
			outcome{"", map[string]string{"printf performed": "performed", "printf refused": "refused"}, "⏺ Bash(…)"},
		},
		{
			// The deny alone is trusted: an untrusted handler of the same matcher never answers.
			"deny trusted alone",
			[]bool{false, false, false, false, false, false, true},
			outcome{
				"",
				map[string]string{"printf performed": "performed", "printf refused": "no git writes"},
				"⏺ Bash — denied by hook: no git writes",
			},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			record := filepath.Join(fix.recordDir, "codex-events")
			handlers := []codexHandler{}
			for _, event := range []string{
				hookSessionStart, hookUserPromptSubmit, hookPreToolUse, hookPostToolUse, hookStop, hookSessionEnd,
			} {
				handlers = append(handlers, codexHandler{event, "", "printf '" + event + `\n' >> "` + record + `"`})
			}
			deny := `case "$(cat)" in *refused*) printf '%s' '{"hookSpecificOutput":` +
				`{"permissionDecision":"deny","permissionDecisionReason":"no git writes"}}';; esac`
			handlers = append(handlers, codexHandler{hookPreToolUse, "Bash", deny})
			writeCodexHooks(t, fix, handlers, row.trust)
			fix.write(Scenario{SessionID: fixtureThread, Pane: codexShapes, BusyMS: intPtr(0), Steps: []Step{
				{
					Type:    StepToolCall,
					Tool:    "Bash",
					Input:   json.RawMessage(`{"command":"printf performed"}`),
					Perform: true,
				},
				{
					Type:    StepToolCall,
					Tool:    "Bash",
					Input:   json.RawMessage(`{"command":"printf refused"}`),
					Perform: true,
				},
				{Type: StepTurn, Reply: "finished"},
			}})
			session := fix.startTUI("codex", codexArgs(), nil)
			session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, "›") })
			session.typeLine("go")
			session.waitFrame("finished", func(frame string) bool { return strings.Contains(frame, "finished") })
			if frame := session.out.frame(); !strings.Contains(frame, row.want.row) {
				t.Fatalf("pane shows no row %q:\n%s", row.want.row, frame)
			}
			session.typeLine("/exit")
			if code := session.waitExit(); code != 0 {
				t.Fatalf("exit=%d stderr=%s", code, session.stderr.String())
			}
			events := ""
			if content, err := os.ReadFile(record); err == nil {
				events = string(content)
			} else if !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if events != row.want.events {
				t.Fatalf("hooks fired %q, want %q (record %s)", events, row.want.events, record)
			}
			path := fix.rolloutPath()
			if got := codexToolOutputs(t, path); !maps.Equal(got, row.want.outputs) {
				t.Fatalf("tool outputs in %s = %q, want %q", path, got, row.want.outputs)
			}
		})
	}
}
