package herdr

import (
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
	for _, tc := range []struct{ name, body, engine, reason string }{
		{"engine", `{"type":"pane_process_info","process_info":{"pane_id":"w1:p2","foreground_processes":[]}}`, "invalid", "engine"},
		{"json", `{"type":`, "cc", "decode"},
		{"type", `{"type":"future","process_info":{"pane_id":"w1:p2"}}`, "cc", `result type "future"`},
		{"identity", `{"type":"pane_process_info","process_info":{"pane_id":"w1:p9","foreground_processes":[]}}`, "cc", "malformed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := foregroundOf(tc.body, "w1:p2", pfmengine.ID(tc.engine))
			if err == nil || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("foreground error=%v; want %s", err, tc.reason)
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
		{name: "partial", processes: `[{"pid":7,"name":"claude"},{"pid":0,"name":"zsh"}]`, diagnostic: "valid pid or identity"},
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
