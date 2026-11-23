package herdr

import (
	"fmt"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// foregroundOf is EngineForeground over a pane.process_info body, as its callers read it.
func foregroundOf(body, pane string, engine pfmengine.ID) (bool, error) {
	processes, err := ParsePaneProcesses([]byte(body))
	if err != nil {
		return false, err
	}
	return EngineForeground(processes, pane, engine)
}

func TestEngineForegroundRejectsInvalidEnvelope(t *testing.T) {
	for _, tc := range []struct{ name, body, engine, want string }{
		{"engine", `{"type":"pane_process_info","process_info":{"pane_id":"w1:p2","foreground_processes":[]}}`, "invalid", "herdr foreground engine: unknown engine \"invalid\" (want cc/claude, cx/codex, ox/opencode)"},
		{"json", `{"type":`, "cc", "herdr pane.process_info: decode result \"{\\\"type\\\":\": unexpected end of JSON input"},
		{"type", `{"type":"future","process_info":{"pane_id":"w1:p2"}}`, "cc", "herdr pane.process_info: result type \"future\", want pane_process_info"},
		{"identity", `{"type":"pane_process_info","process_info":{"pane_id":"w1:p9","foreground_processes":[]}}`, "cc", "read Herdr processes in w1:p2: malformed process_info result for pane \"w1:p9\""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := foregroundOf(tc.body, "w1:p2", pfmengine.ID(tc.engine))
			if got := fmt.Sprint(err); got != tc.want {
				t.Fatalf("foreground error = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEngineForegroundEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, processes string
		want            bool
		diagnostic      string
	}{
		{name: "empty", processes: `[]`, diagnostic: "foreground evidence unavailable"},
		{name: "null", processes: `null`, diagnostic: "foreground evidence unavailable"},
		{name: "shell", processes: `[{"pid":7,"name":"zsh"}]`},
		{name: "engine", processes: `[{"pid":7,"argv0":"claude"}]`, want: true},
		// Herdr now and then answers a live Claude's row with an empty argv; its
		// name is the native binary's file name, Claude's version.
		{name: "version-named-without-argv", processes: `[{"pid":3300341,"name":"2.1.330","argv":[]}]`, want: true},
		{name: "partial", processes: `[{"pid":7,"name":"claude"},{"pid":0,"name":"zsh"}]`, want: true},
		{name: "unidentified-beside-shell", processes: `[{"pid":7,"name":"zsh"},{"pid":8}]`, diagnostic: "valid pid or identity"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"type":"pane_process_info","process_info":{"pane_id":"w6:p3","foreground_processes":` + tc.processes + `}}`
			got, err := foregroundOf(body, "w6:p3", pfmengine.Claude)
			if tc.diagnostic != "" {
				if err == nil || !strings.Contains(err.Error(), tc.diagnostic) {
					t.Fatalf("foreground=%t err=%v; want %s", got, err, tc.diagnostic)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("foreground=%t err=%v; want %t", got, err, tc.want)
			}
		})
	}
}

// TestProcessRunsEngineByItsOwnCommand: a row runs the engine when its argv0,
// else its argv, else its name is the engine's; a child Claude native runs
// under argv0 rg carries Claude's version as its name, so IsEngine still
// counts it as presence while RunsEngine never takes it for the engine.
func TestProcessRunsEngineByItsOwnCommand(t *testing.T) {
	for _, tc := range []struct {
		name          string
		process       Process
		engine        pfmengine.ID
		runs, present bool
	}{
		{name: "argv0", process: Process{PID: 7, Name: "2.1.330", Argv0: "claude"}, engine: pfmengine.Claude, runs: true, present: true},
		{name: "version-named-without-argv", process: Process{PID: 7, Name: "2.1.330"}, engine: pfmengine.Claude, runs: true, present: true},
		{name: "argv-without-argv0", process: Process{PID: 7, Name: "x", Argv: []string{"/usr/bin/codex", "resume"}}, engine: pfmengine.Codex, runs: true, present: true},
		{name: "bundled-rg", process: Process{PID: 8, Name: "2.1.330", Argv0: "rg", Argv: []string{"rg", "--files"}}, engine: pfmengine.Claude, present: true},
		{name: "shell", process: Process{PID: 9, Name: "zsh", Argv0: "zsh", Argv: []string{"zsh"}}, engine: pfmengine.Claude},
	} {
		runs := tc.process.RunsEngine(tc.engine)
		present := tc.process.IsEngine(tc.engine)
		if runs != tc.runs || present != tc.present {
			t.Fatalf("%s: RunsEngine=%t IsEngine=%t, want %t %t (%s)",
				tc.name, runs, present, tc.runs, tc.present, tc.process)
		}
	}
}
