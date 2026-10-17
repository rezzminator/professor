package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
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
