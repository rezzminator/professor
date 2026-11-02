package chat

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestConvergeHerdrNamesFromEngineRename(t *testing.T) {
	for _, refuse := range []bool{false, true} {
		t.Run(map[bool]string{false: "renamed", true: "refused"}[refuse], func(t *testing.T) {
			testjail.Fleet(t)
			mutations := map[string]bool{}
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				if value, code, ok := nativeChatProof(req, "w6:p3"); ok {
					return value, code
				}
				switch req.Method {
				case "pane.rename":
					mutations[req.Method] = true
					if refuse {
						return nil, "invalid_params"
					}
					return map[string]any{"pane": map[string]any{"pane_id": "w6:p3", "label": "Engine rename"}}, ""
				case "agent.rename":
					mutations[req.Method] = true
					return map[string]any{"agent": map[string]any{"pane_id": "w6:p3", "name": "engine-rename"}}, ""
				case "pane.get":
					return testjail.HerdrPaneInfo("w6:p3"), ""
				case "tab.get":
					return map[string]any{
						"type": "tab_info",
						"tab":  map[string]any{"tab_id": "w6:t1", "label": "Engine rename", "pane_count": 1},
					}, ""
				default:
					return nil, "unexpected_method"
				}
			})
			runtime, state, seat := nativeChatSeat(t, socket, "live")
			seat = nativeChatHost(t, state, seat, true)
			db, err := store.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			if err := db.UpsertTranscript(
				context.Background(),
				store.Transcript{UUID: seat.SessionID, CustomTitle: "Engine rename"},
			); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			ok := ConvergeHerdrNames(context.Background(), db, runtime.Paths, &stderr)
			if ok == refuse || (stderr.Len() != 0) != refuse {
				t.Fatalf("ok=%v stderr=%s refuse=%v", ok, &stderr, refuse)
			}
			got, _, err := state.Seat(context.Background(), seat.SeatID)
			if err != nil || (!refuse && got.Name != "Engine rename") || (!refuse && !mutations["agent.rename"]) {
				t.Fatalf("seat=%+v err=%v calls=%v", got, err, mutations)
			}
		})
	}
}

// F9: Codex's session_index.jsonl holds its automatic thread titles beside a
// user's /rename, with nothing to tell them apart. A seat launched with --name
// keeps that name when a Codex title lands in the ledger (compose/herdr.go's
// rule: a Codex indexed name never outranks a seat name); an unnamed seat
// takes the ledger's name.
func TestConvergeHerdrNamesFromCodexTitle(t *testing.T) {
	for _, testCase := range []struct{ launched, want string }{
		{launched: "if-build v5", want: "if-build v5"},
		{launched: "", want: "ENGINE_FIX"},
	} {
		t.Run(cmp.Or(testCase.launched, "unnamed"), func(t *testing.T) {
			testjail.Fleet(t)
			mutations := map[string]bool{}
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				if value, code, ok := nativeChatProof(req, "w6:p3"); ok {
					return value, code
				}
				switch req.Method {
				case "pane.rename":
					mutations[req.Method] = true
					return map[string]any{"pane": map[string]any{"pane_id": "w6:p3", "label": "ENGINE_FIX"}}, ""
				case "agent.rename":
					mutations[req.Method] = true
					var slug string
					if err := json.Unmarshal(req.Params["name"], &slug); err != nil {
						return nil, "invalid_params"
					}
					return map[string]any{"agent": map[string]any{"pane_id": "w6:p3", "name": slug}}, ""
				case "pane.get":
					return testjail.HerdrPaneInfo("w6:p3"), ""
				case "tab.get":
					return map[string]any{
						"type": "tab_info",
						"tab":  map[string]any{"tab_id": "w6:t1", "label": "ENGINE_FIX", "pane_count": 1},
					}, ""
				default:
					return nil, "unexpected_method"
				}
			})
			runtime, state, seat := nativeChatSeat(t, socket, "live")
			seat.Engine, seat.Name = string(pfmengine.Codex), testCase.launched
			seat = nativeChatHost(t, state, seat, true)
			db, err := store.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			if err := db.UpsertCxName(
				context.Background(),
				store.CxName{ID: seat.SessionID, ThreadName: "ENGINE_FIX", Source: store.CxNameSourceSessionIndex},
			); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			if !ConvergeHerdrNames(context.Background(), db, runtime.Paths, &stderr) {
				t.Fatalf("converge failed: %s", &stderr)
			}
			got, _, err := state.Seat(context.Background(), seat.SeatID)
			if err != nil || got.Name != testCase.want || mutations["pane.rename"] != (testCase.launched == "") {
				t.Fatalf(
					"seat=%+v err=%v calls=%v stderr=%s; want name %q",
					got,
					err,
					mutations,
					&stderr,
					testCase.want,
				)
			}
		})
	}
}
