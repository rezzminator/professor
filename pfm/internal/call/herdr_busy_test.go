package call

import (
	"context"
	"fmt"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// Exercise the producer wire, not a mock delivery verdict: input must be queued
// with Enter, and only force_now may interrupt the recipient's active turn.
func TestHerdrCallBusy(t *testing.T) {
	for _, id := range []pfmengine.ID{pfmengine.Claude, pfmengine.Codex, pfmengine.OpenCode} {
		for _, test := range []struct {
			name        string
			force       bool
			idleAfter   int
			wantEscapes int
			composer    string
			wantRefused bool
			style       bool
		}{
			{name: "queue"},
			{name: "queue-styled-placeholder", composer: "hint", style: true},
			{name: "force", force: true, idleAfter: 2, wantEscapes: 2},
			{name: "force-bounded", force: true, wantEscapes: 3},
			{name: "draft-preserved", composer: "private draft", wantRefused: true},
			{name: "force-draft-preserved", force: true, composer: "private draft", wantRefused: true},
			{name: "menu-preserved", composer: "1. Allow once\n  2. Deny", wantRefused: true},
		} {
			t.Run(string(id)+"/"+test.name, func(t *testing.T) {
				escapes, enters := 0, 0
				payload := ""
				socket := newNativeSocket(t, func(request nativeRequest) (any, string) {
					switch request.Method {
					case "agent.get":
						return nativeAgent("agent_info", "working"), ""
					case "pane.read":
						composer := test.composer
						if test.style && request.Params["format"] == "ansi" {
							composer = "\x1b[2mhint\x1b[0m"
						}
						if enters > 0 {
							composer = ""
						}
						screen := "› " + composer
						if id == pfmengine.Claude {
							screen = "────────\n❯ " + composer + "\n────────"
						}
						if test.idleAfter == 0 || escapes < test.idleAfter {
							if id == pfmengine.OpenCode {
								screen += "\nesc interrupt"
							} else {
								screen += "\nesc to interrupt"
							}
						}
						if enters > 0 {
							screen = payload + "\n" + screen
						}
						return nativePaneRead(request, screen), ""
					case "pane.send_input":
						if escapes != test.wantEscapes {
							t.Errorf(
								"message sent before interrupts completed: Escape=%d want %d",
								escapes,
								test.wantEscapes,
							)
						}
						payload, _ = request.Params["text"].(string)
					case "pane.send_keys":
						switch fmt.Sprint(request.Params["keys"]) {
						case "[Escape]":
							escapes++
						case "[Enter]":
							if payload == "" {
								t.Error("submitted before sending the message")
							}
							enters++
						default:
							t.Errorf("unexpected key request: %+v", request)
						}
					default:
						t.Errorf("busy delivery used unexpected API %s", request.Method)
					}
					return map[string]any{"type": "ok"}, ""
				})
				target := Target{Backend: "herdr", SocketPath: socket.path, Pane: "w1:p2", Engine: string(id)}
				engine := nativeEngine(t, target)
				engine.options.InterruptTries = 3
				result, err := engine.CallTarget(
					context.Background(),
					target,
					Request{Message: "queued request", ForceNow: test.force},
				)
				if err != nil {
					t.Fatal(err)
				}
				if test.wantRefused {
					if result.Code != CodeUndelivered || result.Typed || payload != "" || enters != 0 || escapes != 0 {
						t.Fatalf(
							"existing composer was not preserved: %+v payload=%q Enter=%d Escape=%d",
							result,
							payload,
							enters,
							escapes,
						)
					}
					return
				}
				if result.Code != 0 || result.Status != statusDelivered || !result.Typed || !result.Busy ||
					result.Interrupted != test.force || enters != 1 || escapes != test.wantEscapes ||
					!strings.HasPrefix(payload, "queued request") || !strings.Contains(payload, "caller-name") {
					t.Fatalf(
						"busy delivery=%+v payload=%q Enter=%d Escape=%d; want delivered, Escape=%d",
						result,
						payload,
						enters,
						escapes,
						test.wantEscapes,
					)
				}
			})
		}
	}
}
