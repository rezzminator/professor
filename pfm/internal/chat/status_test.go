package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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

func TestHerdrStatusReconcilesTranscript(t *testing.T) {
	for _, record := range []string{assistantSaid("ready"), `{"type":"assistant","isApiErrorMessage":true,"error":"server_error","message":{"role":"assistant","content":[{"type":"text","text":"API Error: overloaded"}]}}`, ""} {
		for _, native := range []string{"idle", "done", "working", "blocked", "unknown"} {
			for _, cached := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%t/%d", native, cached, len(record)), func(t *testing.T) {
					root := testjail.Fleet(t)
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

func TestHerdrWatchAccumulatesNativeIdle(t *testing.T) {
	testjail.Fleet(t)
	var reply atomic.Value
	set := func(state string, seq int, terminal, session string) {
		reply.Store(
			fmt.Sprintf(
				`{"agent":{"pane_id":"w6:p3","terminal_id":%q,"agent_status":%q,"state_change_seq":%d,"agent_session":{"source":"hook","agent":"opencode","kind":"session_id","value":%q}}}`,
				terminal,
				state,
				seq,
				session,
			),
		)
	}
	set("idle", 1, "term-1", "session-1")
	target := headless.Chat{
		ID:      "native-idle",
		Name:    "native",
		Engine:  pfmengine.OpenCode,
		Backend: "herdr",
		Live:    true,
		Pane:    "w6:p3",
		Socket: herdrStatusSocketReplies(
			t,
			func() string { return reply.Load().(string) },
			"w6:p3",
			string(pfmengine.OpenCode),
		),
	}
	inspector := NewSeatInspector(nil)
	now := time.Now()
	check := func(at time.Time, want int64) {
		t.Helper()
		got, err := inspector.Inspect(context.Background(), target, at)
		if err != nil || got.IdleSeconds != want {
			t.Fatalf("status=%+v err=%v want idle=%d", got, err, want)
		}
		if got.State == headless.StateIdle && got.RestID == "" {
			t.Fatal("native rest identity did not reach watch status")
		}
	}
	check(now, 0)
	inspector.Sweep()
	check(now.Add(11*time.Second), 11)
	for i, reset := range []struct {
		state, terminal, session string
		seq                      int
	}{
		{"working", "term-1", "session-1", 2},
		{"blocked", "term-1", "session-1", 4},
		{"unknown", "term-1", "session-1", 6},
		{"idle", "term-2", "session-1", 8},
		{"idle", "term-2", "session-2", 10},
		{"idle", "term-2", "session-2", 12},
	} {
		at := now.Add(time.Duration(20+i*30) * time.Second)
		set(reset.state, reset.seq, reset.terminal, reset.session)
		check(at, 0)
		set("idle", reset.seq+1, reset.terminal, reset.session)
		check(at.Add(time.Second), 0)
		check(at.Add(12*time.Second), 11)
	}
	inspector.Sweep()
	inspector.Sweep()
	check(now.Add(300*time.Second), 0)
	check(now.Add(299*time.Second), 0)
}

func TestHerdrWatchFreshTranscriptCapsNativeIdle(t *testing.T) {
	inspector := NewSeatInspector(nil)
	target := headless.Chat{
		ID:      "native",
		Name:    "native",
		Backend: "herdr",
		Live:    true,
		Socket:  "fixture",
		Pane:    "w1:p1",
	}
	native := nativeStatusObservation{Pane: target.Pane, State: "idle", StateChangeSeq: 4, Terminal: "one"}
	now := time.Now()
	status := headless.Status{State: headless.StateIdle}
	inspector.observeNativeIdle(target, now, native, &status, nil)
	status.IdleSeconds = 20
	inspector.observeNativeIdle(target, now.Add(20*time.Second), native, &status, nil)
	if status.IdleSeconds != 20 {
		t.Fatalf("native idle did not advance: %+v", status)
	}
	// A newer completed transcript turn is shorter rest evidence, never permission
	// to keep the elapsed time of the previous turn.
	target.Path = "transcript.jsonl"
	socket := herdrStatusSocket(
		t,
		`{"agent":{"pane_id":"w1:p1","agent_status":"idle","terminal_id":"one","state_change_seq":4}}`,
		"w1:p1",
	)
	target.Socket = socket
	nativeStatus := headless.Status{State: headless.StateIdle, IdleSeconds: 3}
	var observed nativeStatusObservation
	got, err := statusFromHerdr(context.Background(), target, nativeStatus, &observed)
	if err != nil {
		t.Fatal(err)
	}
	// Seed the identical identity from this request, then compare fresh evidence.
	inspector = NewSeatInspector(nil)
	older := headless.Status{State: headless.StateIdle, IdleSeconds: 20}
	inspector.observeNativeIdle(target, now, observed, &older, nil)
	inspector.observeNativeIdle(target, now, observed, &got, nil)
	if got.IdleSeconds != 3 {
		t.Fatalf("new transcript rest ignored: %+v", got)
	}
	// A failed read and a new endpoint both restart observation instead of restoring
	// the age of an old answered transcript.
	inspector.observeNativeIdle(target, now.Add(time.Second), native, &got, fmt.Errorf("fixture read failed"))
	got.IdleSeconds = 100
	inspector.observeNativeIdle(target, now.Add(2*time.Second), observed, &got, nil)
	if got.IdleSeconds != 0 {
		t.Fatalf("failed read did not reset native idle: %+v", got)
	}
	target.Socket += ".moved"
	got.IdleSeconds = 100
	inspector.observeNativeIdle(target, now.Add(3*time.Second), observed, &got, nil)
	if got.IdleSeconds != 0 {
		t.Fatalf("endpoint replacement did not reset idle: %+v", got)
	}
}
