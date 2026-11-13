package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestStatusReportsADeadChatAsAStatusNotAnError pins the verb's contract: a
// resumable chat with no server behind it comes back dead, with its facts,
// and no error — the caller reads Alive(), never an error, for liveness.
func TestStatusReportsADeadChatAsAStatusNotAnError(t *testing.T) {
	root := memoryFleet(t)
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
				root := memoryFleet(t)
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
			root := memoryFleet(t)
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
	// An unreadable seat store costs only the note, which says it could not look.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	runtime.Paths.StateDB = filepath.Join(blocker, "state.db")
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
	if err != nil || !strings.HasPrefix(status.LostNote, "loss note unknown: ") ||
		!strings.Contains(status.LostNote, blocker) || status.Name != seat.Name {
		t.Fatalf(
			"status=%+v err=%v; want the status with lost_note naming the unreadable store %s",
			status,
			err,
			blocker,
		)
	}
}

func TestHerdrStatusReconcilesTranscript(t *testing.T) {
	for _, record := range []string{assistantSaid("ready"), `{"type":"assistant","isApiErrorMessage":true,"error":"server_error","message":{"role":"assistant","content":[{"type":"text","text":"API Error: overloaded"}]}}`, ""} {
		for _, native := range []string{"idle", "done", "working", "blocked", "unknown"} {
			for _, cached := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%t/%d", native, cached, len(record)), func(t *testing.T) {
					root := memoryFleet(t)
					const id = "c3333333-3333-4333-8333-333333333333"
					target := headless.Chat{
						ID:      id,
						Name:    "reconcile",
						Backend: "herdr",
						Engine:  pfmengine.Claude,
						Live:    true,
						Pane:    "w6:p3",
						Socket: herdrStatusSocket(
							t,
							fmt.Sprintf(`{"agent":{"pane_id":"w6:p3","agent_status":%q}}`, native),
						),
					}
					if record != "" {
						seedClaudeChat(t, root, id, record)
						target.Path = filepath.Join(root, "claude", "project", id+".jsonl")
					}
					now := time.Now().Add(15 * time.Second)
					before, err := headless.InspectWith(context.Background(), target, now, nil)
					if err != nil {
						t.Fatal(err)
					}
					inspect := NewSeatInspector(nil).Inspect
					if !cached {
						inspect = func(ctx context.Context, c headless.Chat, n time.Time) (headless.Status, error) {
							return InspectSeat(ctx, nil, c, n, nil)
						}
					}
					got, err := inspect(context.Background(), target, now)
					want := before.State
					switch native {
					case "working", "blocked":
						want = native
					case "idle", "done":
						if want != headless.StateIdle && want != headless.StateError {
							want = headless.StateIdle
						}
					}
					if err != nil || got.State != want {
						t.Fatalf("native=%s transcript=%+v got=%+v err=%v want=%s", native, before, got, err, want)
					}
					if native == "working" || native == "blocked" {
						if got.IdleSeconds != 0 || got.Error != "" {
							t.Fatalf("activity retained idle/error evidence: %+v", got)
						}
					}
					if native == "unknown" {
						data, err := json.Marshal(got)
						if err != nil {
							t.Fatal(err)
						}
						if !strings.Contains(string(data), `"state_source":"transcript (Herdr unknown)"`) ||
							!strings.Contains(got.Line(), "Herdr unknown") {
							t.Fatalf("fallback not visible: %s line=%s", data, got.Line())
						}
					}
				})
			}
		}
	}
}

// Herdr answering agent.get with agent_not_found is an answer, not a failed
// read — `pfm chat ls` and `pfm chat status` must classify the seat from the
// pane's foreground: engine gone is dead, engine still there keeps the
// transcript's verdict; only an unreadable foreground stays an error.
func TestHerdrStatusAgentNotFoundIsAnAnswer(t *testing.T) {
	for _, testCase := range []struct {
		name, foreground, want, source string
		fails                          bool
	}{
		{name: "engine exited", foreground: "bash", want: headless.StateDead},
		{name: "engine untracked", foreground: "claude", want: headless.StateIdle, source: herdrAgentUntracked},
		{name: "foreground unreadable", fails: true},
	} {
		for _, watch := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/watch=%t", testCase.name, watch), func(t *testing.T) {
				root := memoryFleet(t)
				t.Setenv("PFM_STATE_DB", filepath.Join(t.TempDir(), "state.db"))
				t.Setenv("PFM_PROC_ROOT", t.TempDir())
				const id = "c3333333-3333-4333-8333-333333333333"
				seedClaudeChat(t, root, id, assistantSaid("ready"))
				socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
					switch req.Method {
					case "agent.get":
						return nil, herdr.AgentNotFound
					case "pane.process_info":
						if testCase.fails {
							return nil, "process_read_failed"
						}
						return map[string]any{"type": "pane_process_info", "process_info": map[string]any{
							"pane_id":   "w6:p3",
							"shell_pid": 41,
							"foreground_processes": []any{
								map[string]any{"pid": os.Getpid(), "name": testCase.foreground},
							},
						}}, ""
					}
					if proof, code, ok := nativeChatProof(req, "w6:p3"); ok {
						return proof, code
					}
					return nil, "unexpected_method"
				})
				_, db, seat := nativeChatSeat(t, socket, "live")
				_ = nativeChatHost(t, db, seat, true)
				target := headless.Chat{
					ID: id, Name: "gone", Backend: "herdr", Engine: pfmengine.Claude,
					Live: true, Pane: "w6:p3", Socket: socket,
					Path: filepath.Join(root, "claude", "project", id+".jsonl"),
				}
				inspect := NewSeatInspector(nil).Inspect
				if !watch {
					inspect = func(ctx context.Context, chat headless.Chat, now time.Time) (headless.Status, error) {
						return InspectSeat(ctx, nil, chat, now, nil)
					}
				}
				got, err := inspect(context.Background(), target, time.Now().Add(time.Minute))
				if testCase.fails {
					if err == nil || !strings.Contains(err.Error(), socket) || !strings.Contains(err.Error(), "w6:p3") {
						t.Fatalf("unreadable foreground status=%+v err=%v; want a named endpoint error", got, err)
					}
					return
				}
				if err != nil || got.State != testCase.want || got.StateSource != testCase.source {
					t.Fatalf("agent_not_found with foreground %s: status=%+v err=%v; want state %s source %q",
						testCase.foreground, got, err, testCase.want, testCase.source)
				}
			})
		}
	}
}
