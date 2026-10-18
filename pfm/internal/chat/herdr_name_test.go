package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestRenameHerdrUsesPaneLabelUniqueAgentAndDurableName(t *testing.T) {
	testjail.Fleet(t)
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(testjail.ShortRoot(t), "herdr.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	requests := make(chan string, 4)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			func() {
				defer func() { _ = conn.Close() }()
				var req struct {
					ID     string `json:"id"`
					Method string `json:"method"`
					Params struct {
						Name   string `json:"name"`
						Label  string `json:"label"`
						Pane   string `json:"pane_id"`
						Target string `json:"target"`
					} `json:"params"`
				}
				if json.NewDecoder(conn).Decode(&req) != nil {
					return
				}
				requests <- req.Method + ":" + req.Params.Name + req.Params.Label
				if req.Method == "agent.rename" && req.Params.Name == "integration" {
					_, _ = fmt.Fprintf(
						conn,
						`{"id":%q,"ok":false,"error":{"code":"agent_name_taken","message":"already used"}}`+"\n",
						req.ID,
					)
					return
				}
				result := `{"pane":{"pane_id":"w6:p3","label":"INTEGRATION"}}`
				if req.Method == "agent.rename" {
					result = `{"agent":{"pane_id":"w6:p3","name":"integration-2"}}`
				}
				_, _ = fmt.Fprintf(conn, `{"id":%q,"ok":true,"result":%s}`+"\n", req.ID, result)
			}()
		}
	}()
	state := fleetdb.OpenSharedState(context.Background(), values)
	defer func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := state.PutSeat(
		context.Background(),
		fleetdb.Seat{
			SeatID:         "seat1",
			SessionID:      "sid1",
			Engine:         "cc",
			Backend:        "herdr",
			HerdrSocket:    socket,
			HerdrPane:      "w6:p3",
			HerdrWorkspace: "w6",
			Name:           "OLD",
			State:          "live",
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := renameHerdr(
		context.Background(),
		headless.Chat{Backend: "herdr", SeatID: "seat1", ID: "sid1", Engine: "cc", Socket: socket, Pane: "w6:p3"},
		"INTEGRATION",
		values,
	); err != nil {
		t.Fatal(err)
	}
	seat, found, err := state.Seat(context.Background(), "seat1")
	if err != nil || !found || seat.Name != "INTEGRATION" {
		t.Fatalf("seat=%+v found=%v err=%v", seat, found, err)
	}
	got := []string{<-requests, <-requests, <-requests}
	want := []string{"pane.rename:INTEGRATION", "agent.rename:integration", "agent.rename:integration-2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("requests=%v want=%v", got, want)
	}
}

func TestRenameHerdrRequiresExistingAgent(t *testing.T) {
	testjail.Fleet(t)
	socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
		if req.Method == "pane.rename" {
			return map[string]any{"pane": map[string]any{"pane_id": "w6:p3", "label": "INTEGRATION"}}, ""
		}
		return nil, "agent_not_found"
	})
	runtime, _, seat := nativeChatSeat(t, socket, "lost")
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
