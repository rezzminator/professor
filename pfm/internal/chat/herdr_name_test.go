package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestRenameHerdrUsesPaneLabelUniqueAgentAndDurableName(t *testing.T) {
	for _, mode := range []string{"same", "moved", "recycled", "unlearned"} {
		t.Run(mode, func(t *testing.T) {
			testjail.Fleet(t)
			pane := "w6:p3"
			if mode == "moved" {
				pane = "w9:p2"
			}
			var mutations []string
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				if req.Method == "pane.list" && mode == "recycled" {
					return map[string]any{
						"type":  "pane_list",
						"panes": []any{testjail.HerdrPaneInfo("w6:p9")["pane"]},
					}, ""
				}
				if value, code, ok := nativeChatProof(req, pane); ok {
					return value, code
				}
				switch req.Method {
				case "pane.rename":
					mutations = append(mutations, req.Method)
					if string(req.Params["pane_id"]) != fmt.Sprintf("%q", pane) {
						t.Errorf("noncanonical pane rename %s", req.Params["pane_id"])
					}
					return map[string]any{"pane": map[string]any{"pane_id": pane, "label": "INTEGRATION"}}, ""
				case "agent.rename":
					mutations = append(mutations, req.Method)
					if string(req.Params["target"]) != fmt.Sprintf("%q", pane) {
						t.Errorf("noncanonical agent target %s", req.Params["target"])
					}
					if string(req.Params["name"]) == `"integration"` {
						return nil, "agent_name_taken"
					}
					return map[string]any{"agent": map[string]any{"pane_id": pane, "name": "integration-2"}}, ""
				case "pane.get":
					return testjail.HerdrPaneInfo(pane), ""
				case "tab.get":
					return map[string]any{
						"type": "tab_info",
						"tab": map[string]any{
							"tab_id":     strings.Split(pane, ":")[0] + ":t1",
							"label":      "INTEGRATION",
							"pane_count": 1,
						},
					}, ""
				default:
					return nil, "unexpected_method"
				}
			})
			runtime, db, seat := nativeChatSeat(t, socket, "live")
			if mode == "unlearned" {
				seat.SessionID = ""
			}
			seat = nativeChatHost(t, db, seat, true)
			err := renameHerdr(
				context.Background(),
				headless.Chat{SeatID: seat.SeatID, ID: seat.SessionID, Socket: socket, Pane: seat.HerdrPane},
				"INTEGRATION",
				runtime.Paths,
			)
			if mode == "recycled" {
				if err == nil ||
					!strings.Contains(err.Error(), "ownership unproved: terminal not present in pane.list") ||
					len(mutations) != 0 {
					t.Fatalf("recycled occupant mutated: err=%v calls=%v", err, mutations)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"pane.rename", "agent.rename", "agent.rename"}
			if !reflect.DeepEqual(mutations, want) {
				t.Fatalf("mutations=%v", mutations)
			}
			current, found, err := db.Seat(context.Background(), seat.SeatID)
			if err != nil || !found || current.Name != "INTEGRATION" || current.HerdrPane != pane {
				t.Fatalf("seat=%+v found=%v err=%v", current, found, err)
			}
		})
	}
}

func TestRenameHerdrRequiresExistingAgent(t *testing.T) {
	testjail.Fleet(t)
	socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
		if value, code, ok := nativeChatProof(req, "w6:p3"); ok {
			return value, code
		}
		if req.Method == "pane.rename" {
			return map[string]any{"pane": map[string]any{"pane_id": "w6:p3", "label": "INTEGRATION"}}, ""
		}
		return nil, "agent_not_found"
	})
	runtime, db, seat := nativeChatSeat(t, socket, "lost")
	seat = nativeChatHost(t, db, seat, true)
	err := renameHerdr(
		context.Background(),
		headless.Chat{SeatID: seat.SeatID, Socket: socket, Pane: seat.HerdrPane},
		"INTEGRATION",
		runtime.Paths,
	)
	var refusal *herdr.Refusal
	if !errors.As(err, &refusal) || refusal.Code != "agent_not_found" {
		t.Fatalf("ordinary rename error=%v", err)
	}
}

// A rename of a native seat labels the tab its pane sits in alone, under the
// same rule the tmux path's name sync labels by; a shared tab or one already
// carrying the name is left alone, and a tab that cannot be read or relabelled
// after the pane and agent took the name is returned, never swallowed.
func TestRenameHerdrLabelsTheSeatsSoleTab(t *testing.T) {
	for _, test := range []struct {
		name, label, refuse, wantErr string
		panes                        int
		wantRename                   bool
	}{
		{name: "a sole-pane tab takes the name", label: "1", panes: 1, wantRename: true},
		{name: "a tab already named is left alone", label: "INTEGRATION", panes: 1},
		{name: "a shared tab keeps its label", label: "1", panes: 2},
		{name: "an unreadable pane is returned", label: "1", panes: 1, refuse: "pane.get", wantErr: "label Herdr tab"},
		{
			name: "an unreadable tab is returned", label: "1", panes: 1, refuse: "tab.get",
			wantErr: "tab w6:t2 of pane w6:p3",
		},
		{
			name: "a refused relabel is returned", label: "1", panes: 1, refuse: "tab.rename",
			wantErr: "tab w6:t2 of pane w6:p3", wantRename: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			testjail.Fleet(t)
			renamed := make(chan string, 4)
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				if value, code, ok := nativeChatProof(req, "w6:p3"); ok {
					return value, code
				}
				if req.Method == "tab.rename" {
					var label string
					_ = json.Unmarshal(req.Params["label"], &label)
					renamed <- string(req.Params["tab_id"]) + ":" + label
				}
				if req.Method == test.refuse {
					return nil, "invalid_params"
				}
				switch req.Method {
				case "pane.rename":
					return map[string]any{"pane": map[string]any{"pane_id": "w6:p3", "label": "INTEGRATION"}}, ""
				case "agent.rename":
					return map[string]any{"agent": map[string]any{"pane_id": "w6:p3", "name": "integration"}}, ""
				case "pane.get":
					return map[string]any{
						"type": "pane_info",
						"pane": map[string]any{"pane_id": "w6:p3", "tab_id": "w6:t2", "workspace_id": "w6"},
					}, ""
				case "tab.get":
					return map[string]any{
						"type": "tab_info",
						"tab":  map[string]any{"tab_id": "w6:t2", "label": test.label, "pane_count": test.panes},
					}, ""
				case "tab.rename":
					return map[string]any{"type": "tab_info", "tab": map[string]any{"tab_id": "w6:t2"}}, ""
				default:
					return nil, "unexpected_method"
				}
			})
			runtime, db, seat := nativeChatSeat(t, socket, "live")
			seat = nativeChatHost(t, db, seat, true)
			old := seat
			old.Name = "OLD"
			if err := db.UpdateSeat(context.Background(), seat, old); err != nil {
				t.Fatal(err)
			}
			err := renameHerdr(
				context.Background(),
				headless.Chat{SeatID: seat.SeatID, Socket: socket, Pane: seat.HerdrPane},
				"INTEGRATION",
				runtime.Paths,
			)
			if test.wantErr == "" && err != nil ||
				test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("err = %v, want %q", err, test.wantErr)
			}
			var got []string
			for len(renamed) > 0 {
				got = append(got, <-renamed)
			}
			want := []string(nil)
			if test.wantRename {
				want = []string{`"w6:t2":INTEGRATION`}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("tab renames = %v, want %v", got, want)
			}
			stored, found, readErr := db.Seat(context.Background(), seat.SeatID)
			if readErr != nil || !found || stored.Name != "INTEGRATION" {
				t.Fatalf("seat=%+v found=%v err=%v, want the name kept once pane and agent carry it",
					stored, found, readErr)
			}
		})
	}
}
