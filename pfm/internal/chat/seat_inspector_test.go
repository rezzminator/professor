package chat

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

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
