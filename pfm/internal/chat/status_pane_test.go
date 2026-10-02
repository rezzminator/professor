package chat

import (
	"context"
	"errors"
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

const (
	openCodeIdlePane = "  Build · claude-sonnet-4-5\n  0 tokens\n┃\n\n" +
		"                                   tab agents  ctrl+p commands    • OpenCode 1.18.18\n"
	openCodeBusyPane = "  Build · claude-sonnet-4-5\n  0 tokens\n┃\n\n" +
		"   ⬝■■■■■■⬝  esc interrupt                  tab agents  ctrl+p commands    • OpenCode 1.18.18\n"
)

// A live chat Inspect cannot judge from a transcript — OpenCode has none at
// all, and a fresh Claude or Codex seat has one with no turn in it — used to
// be reported "working" unconditionally, i.e. an empty prompt described as a
// chat mid-answer. Its own screen is the evidence.
func TestPaneStateDecidesALiveChatInspectCannotJudge(t *testing.T) {
	tests := []struct {
		name    string
		engine  pfmengine.ID
		capture string
		want    string
	}{
		{"opencode idle", pfmengine.OpenCode, openCodeIdlePane, headless.StateIdle},
		{"opencode busy", pfmengine.OpenCode, openCodeBusyPane, headless.StateWorking},
		{"claude idle", pfmengine.Claude, "conversation\n❯ \n", headless.StateIdle},
		{"claude busy", pfmengine.Claude, "thinking\n· 4s · esc to interrupt\n", headless.StateWorking},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := paneState(test.engine, test.capture); got != test.want {
				t.Fatalf("paneState(%s) = %q, want %q", test.engine, got, test.want)
			}
		})
	}
}

func TestNeedsPaneStateOnlyForALiveChatWithNoTranscriptEvidence(t *testing.T) {
	tests := []struct {
		name string
		chat headless.Chat
		last string
		want bool
	}{
		{"live, no path", headless.Chat{Live: true}, "", true},
		{"live, path but no turn", headless.Chat{Live: true, Path: "/t.jsonl"}, "", true},
		{"live, path with a turn", headless.Chat{Live: true, Path: "/t.jsonl"}, "assistant: hi", false},
		{"dead, no path", headless.Chat{}, "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := needsPaneState(test.chat, headless.Status{Last: test.last})
			if got != test.want {
				t.Fatalf("needsPaneState = %v, want %v", got, test.want)
			}
		})
	}
}

// A capture that could not RUN is an error, never a state: "we failed to look"
// must not render as "the chat is idle".
func TestStatusReturnsACaptureFailureAsAnError(t *testing.T) {
	testjail.Fleet(t)
	_, err := statusFromPane(
		context.Background(),
		headless.Chat{Name: "seat", Live: true, Socket: "ox-1-2-3", Pane: "%0", Engine: pfmengine.OpenCode},
		headless.Status{State: headless.StateWorking},
		func(context.Context, string, string) (string, error) {
			return "", errors.New("tmux could not run")
		},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "tmux could not run") {
		t.Fatalf("statusFromPane error = %v, want the capture failure named", err)
	}
}

func TestStatusReadsALiveOpenCodeSeatFromItsPane(t *testing.T) {
	testjail.Fleet(t)
	chat := headless.Chat{
		Name: "P:OPENCODE", ID: "ses_live", Live: true,
		Socket: "ox-1-2-3", Pane: "%0", Engine: pfmengine.OpenCode,
	}
	for capture, want := range map[string]string{
		openCodeIdlePane: headless.StateIdle,
		openCodeBusyPane: headless.StateWorking,
	} {
		status, err := statusFromPane(
			context.Background(), chat, headless.Status{State: headless.StateWorking},
			func(_ context.Context, socketPath, target string) (string, error) {
				if !strings.HasSuffix(socketPath, "ox-1-2-3") || target != "%0" {
					return "", errors.New("captured the wrong pane: " + socketPath + " " + target)
				}
				return capture, nil
			},
			nil,
		)
		if err != nil {
			t.Fatalf("statusFromPane: %v", err)
		}
		if status.State != want {
			t.Fatalf("state = %q, want %q for capture %q", status.State, want, capture)
		}
	}
}

func TestPaneTargetPrefersThePaneThenTheSession(t *testing.T) {
	tests := []struct {
		chat headless.Chat
		want string
	}{
		{headless.Chat{Socket: "s", Session: "sess", Pane: "%2"}, "%2"},
		{headless.Chat{Socket: "s", Session: "sess"}, "sess"},
		{headless.Chat{Socket: "s"}, "s"},
	}
	for _, test := range tests {
		if got := PaneTarget(test.chat); got != test.want {
			t.Fatalf("PaneTarget(%+v) = %q, want %q", test.chat, got, test.want)
		}
	}
}

var _ = io.Discard

const (
	claudeTrustPane = "Accessing workspace\n\n ❯ 1. Yes, I trust this folder\n   2. No, exit\n"
	claudePermPane  = "Bash command\n  go test ./...\n Do you want to proceed?\n ❯ 1. Yes\n   2. No\n"
	claudeBusyPane  = "running\n· 4s · esc to interrupt\n"
)

// pendingToolChat writes a Claude transcript whose newest record is a tool
// call, last touched quiet ago, and returns the live seat reading it.
func pendingToolChat(t *testing.T, last string, quiet time.Duration) headless.Chat {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	body := `{"type":"user","message":{"role":"user","content":"go"}}` + "\n" + last + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-quiet)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	return headless.Chat{
		Name: "seat", Engine: pfmengine.Claude, Path: path, Live: true,
		Socket: "ox-1-2-3", Pane: "%0",
	}
}

const (
	claudeToolCall      = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{}}]}}`
	claudeAnswerMessage = `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`
)

// countingCapture serves one screen and counts the reads.
func countingCapture(screen string, calls *int) PaneCapture {
	return func(_ context.Context, socketPath, target string) (string, error) {
		*calls++
		if !strings.HasSuffix(socketPath, "ox-1-2-3") || target != "%0" {
			return "", errors.New("captured the wrong pane: " + socketPath + " " + target)
		}
		return screen, nil
	}
}

// A tool call the transcript has been silent on for longer than a tool
// normally takes is either still running or a dialog holding the seat for its
// human. Only the screen tells them apart: the engine's running-turn footer
// means the tool runs; any other screen is the human's to answer.
func TestInspectSeatReadsTheScreenOfASilentPendingToolCall(t *testing.T) {
	testjail.Fleet(t)
	for _, testCase := range []struct {
		name   string
		screen string
		want   string
	}{
		{"a dialog on screen", claudePermPane, headless.StateBlocked},
		{"a tool still running", claudeBusyPane, headless.StateWorking},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			chat := pendingToolChat(t, claudeToolCall, 90*time.Second)
			calls := 0
			status, err := InspectSeat(
				context.Background(),
				nil,
				chat,
				time.Now(),
				countingCapture(testCase.screen, &calls),
			)
			if err != nil {
				t.Fatalf("InspectSeat() error = %v", err)
			}
			if status.State != testCase.want || calls != 1 {
				t.Fatalf("state = %q after %d capture(s), want %q after 1", status.State, calls, testCase.want)
			}
			if status.State == headless.StateBlocked && (status.IdleSeconds != 0 || !status.Alive()) {
				t.Fatalf("blocked status = %#v, want alive with zero idle seconds", status)
			}
		})
	}
}

// A turn mid-stream writes continuously, so a pending tool call quiet for
// under blockedQuietSeconds never costs a capture; nor does a seat that
// answered, or one that is gone.
func TestInspectSeatCapturesOnlyAPendingToolCallQuietPastTheThreshold(t *testing.T) {
	testjail.Fleet(t)
	for _, testCase := range []struct {
		name  string
		last  string
		quiet time.Duration
		live  bool
		want  string
	}{
		{"quiet under the threshold", claudeToolCall, time.Second, true, headless.StateWorking},
		{"an idle seat", claudeAnswerMessage, 90 * time.Second, true, headless.StateIdle},
		{"a dead seat", claudeToolCall, 90 * time.Second, false, headless.StateDead},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			chat := pendingToolChat(t, testCase.last, testCase.quiet)
			chat.Live = testCase.live
			calls := 0
			status, err := InspectSeat(
				context.Background(),
				nil,
				chat,
				time.Now(),
				countingCapture(claudePermPane, &calls),
			)
			if err != nil {
				t.Fatalf("InspectSeat() error = %v", err)
			}
			if calls != 0 || status.State != testCase.want {
				t.Fatalf("state = %q after %d capture(s), want %q after none", status.State, calls, testCase.want)
			}
		})
	}
}

// "We failed to look" is an error, never a state: a pane that could not be
// read must not render a silent pending tool call as blocked or working.
func TestInspectSeatReturnsACaptureFailureOnASilentPendingToolCall(t *testing.T) {
	testjail.Fleet(t)
	chat := pendingToolChat(t, claudeToolCall, 90*time.Second)
	_, err := InspectSeat(context.Background(), nil, chat, time.Now(),
		func(context.Context, string, string) (string, error) { return "", errors.New("tmux could not run") })
	if err == nil || !strings.Contains(err.Error(), "tmux could not run") {
		t.Fatalf("InspectSeat error = %v, want the capture failure named", err)
	}
}

// A live seat with no transcript whose screen is Claude's folder-trust dialog
// is held for its human, not idle at an empty prompt.
func TestInspectSeatReadsATrustDialogOnATranscriptlessSeatAsBlocked(t *testing.T) {
	testjail.Fleet(t)
	chat := headless.Chat{
		Name: "seat", Engine: pfmengine.Claude, Live: true, Socket: "ox-1-2-3", Pane: "%0",
	}
	calls := 0
	status, err := InspectSeat(context.Background(), nil, chat, time.Now(), countingCapture(claudeTrustPane, &calls))
	if err != nil {
		t.Fatalf("InspectSeat() error = %v", err)
	}
	if status.State != headless.StateBlocked || calls != 1 {
		t.Fatalf("state = %q after %d capture(s), want blocked after 1", status.State, calls)
	}
}
