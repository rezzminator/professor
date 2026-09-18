package action

import (
	"context"
	"io"
	"testing"

	"hostops/pfm/internal/compose"
	"hostops/pfm/internal/obs"
)

func TestOpenDetachedResumableSpawnsThroughTheSpawnDoor(t *testing.T) {
	jailAction(t)
	tmux := &fakeActionTmux{alive: map[string]bool{}}
	executor, err := New(Dependencies{
		Tmux:      tmux,
		Processes: &fakeProcesses{},
		Gate:      fixedGate(false),
		Runner:    &captureRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	id := "66666666-6666-4666-8666-666666666666"
	request := Request{
		Row: compose.Row{
			Kind: compose.ResumeClaude,
			ID:   id,
			CWD:  "/work/resume",
			Name: "resumable chat",
		},
		PrimaryAccount: 1,
		Home:           "/home/test",
		FreshSocket:    "cc-905-1-1",
		Config:         testMachineConfig("/home/test"),
	}
	result, err := executor.OpenDetached(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(tmux.created) != 1 {
		t.Fatalf("spawn door called %d times, want 1: %#v", len(tmux.created), tmux.created)
	}
	if tmux.created[0].Socket != "cc-905-1-1" {
		t.Fatalf("spawned server socket = %q, want cc-905-1-1", tmux.created[0].Socket)
	}
	if result != (OpenResult{Name: "resumable chat", Socket: "cc-905-1-1", State: "opened"}) {
		t.Fatalf("result = %#v", result)
	}
}

func TestOpenDetachedLiveSpawnsNothing(t *testing.T) {
	jailAction(t)
	tmux := &fakeActionTmux{alive: map[string]bool{"cc-100-1-1": true}}
	executor, err := New(Dependencies{
		Tmux:      tmux,
		Processes: &fakeProcesses{},
		Gate:      fixedGate(false),
		Runner:    &captureRunner{},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		Row: compose.Row{
			Kind:        compose.LiveClaude,
			ID:          "77777777-7777-4777-8777-777777777777",
			Socket:      "cc-100-1-1",
			SessionName: "live-session",
			Name:        "live chat",
		},
		PrimaryAccount: 1,
		Home:           "/home/test",
		FreshSocket:    "cc-900-1-1",
		Config:         testMachineConfig("/home/test"),
	}
	result, err := executor.OpenDetached(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if len(tmux.created) != 0 {
		t.Fatalf("spawn door called for an already-live chat: %#v", tmux.created)
	}
	if result != (OpenResult{Name: "live chat", Socket: "cc-100-1-1", State: "live"}) {
		t.Fatalf("result = %#v", result)
	}
}

// TestOpenDetachedRecordsItsTrail: the detached door is a coordinator like
// Executor.Open, and it owes the same state trail — requested → opened →
// done. Without it a chat opened over MCP leaves no trace in the activity
// log, and the only chats with a recorded birth are the ones opened from a
// terminal.
func TestOpenDetachedRecordsItsTrail(t *testing.T) {
	jailAction(t)
	ctx, recorder := obs.Test(t)
	executor, err := New(Dependencies{
		Tmux:      &fakeActionTmux{alive: map[string]bool{}},
		Processes: &fakeProcesses{},
		Gate:      fixedGate(false),
		Runner:    &captureRunner{},
		Stderr:    io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.OpenDetached(ctx, Request{
		Row: compose.Row{
			Kind: compose.ResumeClaude,
			ID:   "55555555-5555-4555-8555-555555555555",
			CWD:  "/work/resume",
			Name: "trailed chat",
		},
		PrimaryAccount: 1,
		Home:           "/home/test",
		FreshSocket:    "cc-906-1-1",
		Config:         testMachineConfig("/home/test"),
	}); err != nil {
		t.Fatal(err)
	}
	var opened bool
	for _, record := range recorder.Records() {
		if record.Message != "state.transition" {
			continue
		}
		if kind, _ := record.Field("kind"); kind != "action" {
			continue
		}
		if next, _ := record.Field("next"); next == "opened" {
			opened = true
		}
	}
	if !opened {
		t.Fatalf("OpenDetached() reached no opened state: %s", recorder.Raw())
	}
}

func TestOpenDetachedNilExecutor(t *testing.T) {
	var executor *Executor
	if _, err := executor.OpenDetached(context.Background(), Request{}); err == nil {
		t.Fatal("want an error for a nil executor, got nil")
	}
}
