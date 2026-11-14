package chat

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/naming"
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

// Claude fires no hook on /rename: its statusline carries the new session_name,
// and ConvergeHerdrSessionName names the chat's native seat from it at once,
// with the hooks' own rules (EngineTitle) over pfm's clear name.
func TestConvergeHerdrSessionName(t *testing.T) {
	cleared := naming.ClearName{Inherited: "INTEGRATION", Name: "INTEGRATION #2"}
	confirmed := cleared
	confirmed.Confirmed = true
	for _, testCase := range []struct {
		name      string
		session   string
		title     string
		clear     *naming.ClearName
		refuse    bool
		want      string
		attempted bool
		decision  naming.ClearName
	}{
		{name: "fresh title", title: "Fresh name", want: "Fresh name", attempted: true},
		{
			name: "inherited title keeps the clear name", title: "INTEGRATION", clear: &cleared,
			want: "INTEGRATION #2", attempted: true, decision: cleared,
		},
		{
			name: "fresh title over a clear name confirms it", title: "Fresh name", clear: &cleared,
			want: "Fresh name", attempted: true, decision: confirmed,
		},
		{name: "reserved title", title: "seat:other", want: "INTEGRATION"},
		{
			name: "reserved title over a clear name", title: "seat:other", clear: &cleared,
			want: "INTEGRATION #2", attempted: true, decision: cleared,
		},
		{name: "the seat's own name", title: "INTEGRATION", want: "INTEGRATION"},
		{
			name: "no native seat", session: "0d0c3b55-1e1f-4a4b-8c8d-9e9fa0a1a2a3", title: "Fresh name",
			want: "INTEGRATION",
		},
		{name: "refused", title: "Fresh name", refuse: true, want: "INTEGRATION", attempted: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			testjail.Fleet(t)
			var mu sync.Mutex
			attempted := false
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				if value, code, ok := nativeChatProof(req, "w6:p3"); ok {
					return value, code
				}
				var label, name string
				_ = json.Unmarshal(req.Params["label"], &label)
				_ = json.Unmarshal(req.Params["name"], &name)
				switch req.Method {
				case "pane.rename":
					mu.Lock()
					attempted = true
					mu.Unlock()
					if testCase.refuse {
						return nil, "invalid_params"
					}
					return map[string]any{"pane": map[string]any{"pane_id": "w6:p3", "label": label}}, ""
				case "agent.rename":
					return map[string]any{"agent": map[string]any{"pane_id": "w6:p3", "name": name}}, ""
				case "pane.get":
					return testjail.HerdrPaneInfo("w6:p3"), ""
				case "tab.get", "tab.rename":
					return map[string]any{
						"type": "tab_info",
						"tab":  map[string]any{"tab_id": "w6:t1", "label": label, "pane_count": 1},
					}, ""
				default:
					return nil, "unexpected_method"
				}
			})
			runtime, state, seat := nativeChatSeat(t, socket, "live")
			seat = nativeChatHost(t, state, seat, true)
			ctx := context.Background()
			if testCase.clear != nil {
				if err := state.PutClearName(ctx, seat.SessionID, *testCase.clear, 1); err != nil {
					t.Fatal(err)
				}
			}
			session := cmp.Or(testCase.session, seat.SessionID)
			err := ConvergeHerdrSessionName(ctx, runtime.Paths, session, testCase.title)
			var refusal *herdr.Refusal
			if testCase.refuse != (errors.As(err, &refusal) && refusal.Code == "invalid_params") ||
				(!testCase.refuse && err != nil) {
				t.Fatalf(
					"ConvergeHerdrSessionName(%q) = %v, want a refusal only when Herdr refuses",
					testCase.title,
					err,
				)
			}
			got, _, err := state.Seat(ctx, seat.SeatID)
			mu.Lock()
			defer mu.Unlock()
			if err != nil || got.Name != testCase.want || attempted != testCase.attempted {
				t.Fatalf("seat name %q, pane.rename sent %v (err %v); want %q, sent %v",
					got.Name, attempted, err, testCase.want, testCase.attempted)
			}
			if testCase.clear != nil {
				decision, _, err := state.ClearName(ctx, seat.SessionID)
				if err != nil || decision != testCase.decision {
					t.Fatalf("clear name = %+v (err %v), want %+v", decision, err, testCase.decision)
				}
			}
		})
	}
}
