package chat

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

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
