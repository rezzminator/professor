package chat

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestStatusReportsADeadChatAsAStatusNotAnError pins the verb's contract: a
// resumable chat with no server behind it comes back dead, with its facts,
// and no error — the caller reads Alive(), never an error, for liveness.
func TestStatusReportsADeadChatAsAStatusNotAnError(t *testing.T) {
	root := testjail.Fleet(t)
	const id = "c3333333-3333-4333-8333-333333333333"
	seedClaudeChat(t, root, id, assistantSaid("tests are green"))
	status, err := Status(context.Background(), nil, StatusRequest{Target: id}, io.Discard)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.State != headless.StateDead || status.Alive() || status.SessionID != id {
		t.Fatalf("Status() = %+v, want %s dead", status, id)
	}
	if !strings.Contains(status.Last, "tests are green") {
		t.Fatalf("Status().Last = %q, want the newest record", status.Last)
	}
	if status.Summary != "" || status.Ask != "" {
		t.Fatalf("Status() ran a runner nobody asked for: %+v", status)
	}
}

// An answered transcript is not proof that its live Herdr endpoint is reachable.
// Both the status inspector and the watch's incremental inspector must probe it.
func TestHerdrAnsweredStatusRequiresReachableEndpoint(t *testing.T) {
	for _, state := range []struct {
		name, record, want string
	}{
		{"idle", assistantSaid("ready"), headless.StateIdle},
		{"error", `{"type":"assistant","isApiErrorMessage":true,"error":"server_error","message":{"role":"assistant","content":[{"type":"text","text":"API Error: overloaded"}]}}`, headless.StateError},
	} {
		for _, watch := range []bool{false, true} {
			name := state.name + "/status"
			if watch {
				name = state.name + "/watch"
			}
			t.Run(name, func(t *testing.T) {
				root := testjail.Fleet(t)
				const id = "c3333333-3333-4333-8333-333333333333"
				seedClaudeChat(t, root, id, state.record)
				target := headless.Chat{
					ID: id, Name: "answered", Backend: "herdr", Engine: pfmengine.Claude,
					Live: true, Pane: "w6:p3", Path: filepath.Join(root, "claude", "project", id+".jsonl"),
					Socket: herdrStatusSocket(t, `{"agent":{"pane_id":"w6:p3","agent_status":"idle"}}`),
				}
				inspect := NewSeatInspector(nil).Inspect
				if !watch {
					inspect = func(ctx context.Context, chat headless.Chat, now time.Time) (headless.Status, error) {
						return InspectSeat(ctx, nil, chat, now, nil)
					}
				}
				now := time.Now().Add(10 * time.Second)
				before, err := inspect(context.Background(), target, now)
				if err != nil || before.State != state.want || before.IdleSeconds < 10 {
					t.Fatalf(
						"reachable status=%+v err=%v, want transcript state %s and idle duration",
						before,
						err,
						state.want,
					)
				}
				parked := target.Socket + ".parked"
				if err := os.Rename(target.Socket, parked); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Rename(parked, target.Socket); err != nil {
						t.Error(err)
					}
				})
				got, err := inspect(context.Background(), target, now)
				if err == nil || !strings.Contains(err.Error(), target.Socket) {
					t.Fatalf(
						"unreachable endpoint %s returned status=%+v err=%v, want named endpoint error",
						target.Socket,
						got,
						err,
					)
				}
			})
		}
	}
}

func TestHerdrStatusRequiresItsPaneAndNamesEndpoint(t *testing.T) {
	for _, answered := range []bool{false, true} {
		t.Run(fmt.Sprint(answered), func(t *testing.T) {
			root := testjail.Fleet(t)
			const id = "c3333333-3333-4333-8333-333333333333"
			target := headless.Chat{
				ID: id, Name: "missing", Backend: "herdr", Engine: pfmengine.Claude,
				Live: true, Pane: "w6:p3",
				Socket: herdrStatusSocket(t, `{"agent":{"pane_id":"w6:p9","agent_status":"idle"}}`),
			}
			if answered {
				seedClaudeChat(t, root, id, assistantSaid("ready"))
				target.Path = filepath.Join(root, "claude", "project", id+".jsonl")
			}
			for _, inspect := range []func(context.Context, headless.Chat, time.Time) (headless.Status, error){
				NewSeatInspector(nil).Inspect,
				func(ctx context.Context, chat headless.Chat, now time.Time) (headless.Status, error) {
					return InspectSeat(ctx, nil, chat, now, nil)
				},
			} {
				status, err := inspect(context.Background(), target, time.Now().Add(time.Minute))
				if err == nil || !strings.Contains(err.Error(), target.Socket) ||
					!strings.Contains(err.Error(), target.Pane) {
					t.Fatalf("missing pane status=%+v err=%v; want named endpoint and pane", status, err)
				}
			}
		})
	}
}

func TestInspectSeatReportsDurableLossUntilNextPrompt(t *testing.T) {
	testjail.Fleet(t)
	runtime, db, seat := nativeChatSeat(t, "/fixture/unreachable.sock", "lost")
	for _, note := range []string{"your last message was not answered — re-send it", ""} {
		before := seat
		seat.LostNote = note
		if err := db.UpdateSeat(context.Background(), before, seat); err != nil {
			t.Fatal(err)
		}
		status, err := InspectSeat(
			context.Background(),
			&runtime,
			headless.Chat{
				ID:      seat.SessionID,
				SeatID:  seat.SeatID,
				Backend: "herdr",
				Engine:  pfmengine.Claude,
				Name:    seat.Name,
			},
			time.Now(),
			nil,
		)
		if err != nil || status.LostNote != note {
			t.Fatalf("status=%+v err=%v want lost_note=%q", status, err, note)
		}
	}
}
