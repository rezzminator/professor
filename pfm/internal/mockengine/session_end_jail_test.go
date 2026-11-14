//go:build jail

package mockengine

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// A real engine runs its SessionEnd hooks before it exits however the pane ends: /exit, its stdin
// closing, or the terminal hanging up (tmux kill-server, a closed Herdr pane), when the signal has
// already cancelled the process context. Claude names the reason; Codex's input carries none.
func TestSessionEndRunsOnEveryPaneExit(t *testing.T) {
	for _, row := range []struct{ engine, exit, reason string }{
		{"claude", "command", "prompt_input_exit"},
		{"claude", "eof", "other"},
		{"claude", "hangup", "other"},
		{"codex", "command", ""},
		{"codex", "eof", ""},
		{"codex", "hangup", ""},
	} {
		t.Run(row.engine+" "+row.exit, func(t *testing.T) {
			fix := newFixture(t)
			t.Chdir(fix.work)
			record := filepath.Join(fix.recordDir, "session-end.jsonl")
			// The sleep outlasts the hangup: a hook cut off by the cancelled
			// process context never reaches its write.
			command := `sleep 0.3; cat >> "` + record + `"; printf '\n' >> "` + record + `"`
			var args []string
			composer, session := "❯", fixtureSession
			if row.engine == "codex" {
				writeCodexHooks(t, fix, []codexHandler{{hookSessionEnd, "", command}}, []bool{true})
				fix.write(Scenario{SessionID: fixtureThread, Pane: codexShapes})
				args, composer, session = codexArgs(), "›", fixtureThread
			} else {
				fix.write(Scenario{SessionID: fixtureSession})
				args = []string{
					"--settings",
					inlineHookSettings(t, map[string]any{hookSessionEnd: commandHook(command)}, ""),
				}
			}
			pane := fix.startTUI(row.engine, args, nil)
			pane.waitFrame("the composer", func(frame string) bool { return strings.Contains(frame, composer) })
			switch row.exit {
			case "command":
				pane.typeLine("/exit")
			case "eof":
				if err := pane.stdin.Close(); err != nil {
					t.Fatal(err)
				}
			case "hangup":
				pane.cancel()
			}
			if code := pane.waitExit(); code != 0 {
				t.Fatalf("exit code = %d, want 0; stderr=%q", code, pane.stderr.String())
			}
			got := readFile(t, record)
			lines := strings.Split(strings.TrimSpace(got), "\n")
			if len(lines) != 1 {
				t.Fatalf("SessionEnd ran %d times, want 1; record %s: %q", len(lines), record, got)
			}
			var payload struct {
				Event     string `json:"hook_event_name"`
				Reason    string `json:"reason"`
				SessionID string `json:"session_id"`
			}
			if err := json.Unmarshal([]byte(lines[0]), &payload); err != nil {
				t.Fatalf("decode %s: %v; %q", record, err, lines[0])
			}
			want := [3]string{hookSessionEnd, row.reason, session}
			if gotFields := [3]string{payload.Event, payload.Reason, payload.SessionID}; gotFields != want {
				t.Fatalf("SessionEnd payload = %q, want %q (record %s)", gotFields, want, record)
			}
		})
	}
}
