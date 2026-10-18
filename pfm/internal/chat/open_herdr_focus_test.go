package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestOpenNativeFocusAcknowledgement(t *testing.T) {
	// Real agent.focus result recorded by the pinned producer, not a pane.get reply.
	const producer = `{"type":"agent_info","agent":{"terminal_id":"term_65e0c6fed53742","agent":"claude","agent_status":"idle","workspace_id":"w1","tab_id":"w1:t2","pane_id":"w1:p2","focused":true,"state_change_seq":5,"completion_seq":5,"cwd":"/work/express","foreground_cwd":"/work/express","revision":0}}`
	for _, tc := range []struct {
		name, body, refusal, diagnostic string
	}{
		{name: "producer", body: producer},
		{name: "legacy", body: `{"type":"pane_focused","pane_id":"w1:p2"}`},
		{name: "missing-agent", body: `{"type":"agent_info"}`, diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "null-agent", body: `{"type":"agent_info","agent":null}`, diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "missing-pane", body: `{"type":"agent_info","agent":{"focused":true}}`, diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "missing-focused", body: `{"type":"agent_info","agent":{"pane_id":"w1:p2"}}`, diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "unfocused", body: strings.Replace(producer, `"focused":true`, `"focused":false`, 1), diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "mismatched-pane", body: strings.Replace(producer, `"pane_id":"w1:p2"`, `"pane_id":"w1:p9"`, 1), diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "wrong-type", body: strings.Replace(producer, `"type":"agent_info"`, `"type":"pane_info"`, 1), diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "malformed-payload", body: `"malformed"`, diagnostic: "decode Herdr focus"},
		{name: "malformed-focused", body: strings.Replace(producer, `"focused":true`, `"focused":"true"`, 1), diagnostic: "decode Herdr focus"},
		{name: "top-level-spoof", body: `{"type":"agent_info","pane_id":"w1:p2","focused":true}`, diagnostic: "unexpected Herdr focus acknowledgement"},
		{name: "refused", refusal: "focus_refused", diagnostic: "focus_refused"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testjail.Fleet(t)
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				switch req.Method {
				case "pane.get":
					return map[string]any{"pane": map[string]any{"pane_id": "w1:p2"}}, ""
				case "pane.process_info":
					return map[string]any{"type": "pane_process_info", "process_info": map[string]any{
						"pane_id": "w1:p2", "foreground_processes": []any{
							map[string]any{"pid": 42, "name": "claude", "argv0": "claude"},
						},
					}}, ""
				case "agent.focus":
					if string(req.Params["target"]) != `"w1:p2"` {
						t.Errorf("focus target=%s", req.Params["target"])
					}
					return json.RawMessage(tc.body), tc.refusal
				default:
					return nil, "unexpected_method"
				}
			})
			runtime, db, seat := nativeChatSeat(t, socket, "live")
			seat.HerdrPane = "w1:p2"
			if err := db.PutSeat(context.Background(), seat); err != nil {
				t.Fatal(err)
			}
			t.Setenv("HERDR_ENV", "1")
			t.Setenv("HERDR_SOCKET_PATH", socket)
			t.Setenv("HERDR_PANE_ID", "w1:p9")
			var stdout, stderr bytes.Buffer
			code := OpenID(context.Background(), seat.SessionID, &stdout, &stderr, &runtime)
			if tc.diagnostic == "" {
				if code != 0 || stderr.Len() != 0 {
					t.Fatalf("valid focus acknowledgement: code=%d stderr=%s", code, stderr.String())
				}
			} else if code != 1 || !strings.Contains(stderr.String(), tc.diagnostic) {
				t.Fatalf(
					"invalid focus acknowledgement: code=%d stderr=%s want=%q",
					code,
					stderr.String(),
					tc.diagnostic,
				)
			}
		})
	}
}
