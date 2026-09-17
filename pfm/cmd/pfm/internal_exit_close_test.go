package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/kill"
)

// TestRunExitCloseGuards pins every gate that must stop runExitClose from
// touching a terminal: it acts ONLY on SessionEnd + reason
// "prompt_input_exit" ("clear" especially — that chat keeps running), only
// when $TMUX names a real fleet socket, and always returns 0.
func TestRunExitCloseGuards(t *testing.T) {
	jailExitClosePaths(t)
	original := exitCloseTerminals
	originalEnv := exitCloseEnv
	t.Cleanup(func() {
		exitCloseTerminals = original
		exitCloseEnv = originalEnv
	})

	for _, test := range []struct {
		name       string
		payload    string
		tmux       string
		wantCalled bool
		wantSocket string
	}{
		{
			name:       "prompt_input_exit on a fleet socket closes",
			payload:    `{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`,
			tmux:       "/tmp/tmux-501/cc-500-1-1,9999,0",
			wantCalled: true,
			wantSocket: "/tmp/tmux-501/cc-500-1-1",
		},
		{
			name:    "clear reason closes nothing — that chat keeps running",
			payload: `{"hook_event_name":"SessionEnd","reason":"clear"}`,
			tmux:    "/tmp/tmux-501/cc-500-1-1,9999,0",
		},
		{
			name:    "logout reason closes nothing",
			payload: `{"hook_event_name":"SessionEnd","reason":"logout"}`,
			tmux:    "/tmp/tmux-501/cc-500-1-1,9999,0",
		},
		{
			name:    "an unrecognized reason closes nothing",
			payload: `{"hook_event_name":"SessionEnd","reason":"other"}`,
			tmux:    "/tmp/tmux-501/cc-500-1-1,9999,0",
		},
		{
			name:    "a non-SessionEnd event closes nothing",
			payload: `{"hook_event_name":"SessionStart","reason":"prompt_input_exit"}`,
			tmux:    "/tmp/tmux-501/cc-500-1-1,9999,0",
		},
		{
			name:    "empty TMUX closes nothing — no viewport to close",
			payload: `{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`,
			tmux:    "",
		},
		{
			name:    "a non-fleet socket closes nothing — this is a human's own tmux",
			payload: `{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`,
			tmux:    "/tmp/tmux-501/default,9999,0",
		},
		{
			name:    "malformed payload fails open and closes nothing",
			payload: `{`,
			tmux:    "/tmp/tmux-501/cc-500-1-1,9999,0",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			called := false
			var gotSocket string
			exitCloseTerminals = func(
				_ context.Context, socketPath string, _ kill.ViewportDeps,
			) ([]int, []string, error) {
				called = true
				gotSocket = socketPath
				return nil, nil, nil
			}
			exitCloseEnv = func(key string) string {
				if key == "TMUX" {
					return test.tmux
				}
				if key == "TMUX_PANE" && test.tmux != "" {
					return "%0"
				}
				return ""
			}

			var stderr bytes.Buffer
			code := runExitClose(strings.NewReader(test.payload), &stderr)
			if code != 0 {
				t.Fatalf("exit=%d, want 0 (fail-open); stderr=%q", code, stderr.String())
			}
			if called != test.wantCalled {
				t.Fatalf("closer called=%v, want %v; stderr=%q", called, test.wantCalled, stderr.String())
			}
			if test.wantCalled && gotSocket != test.wantSocket {
				t.Fatalf("socket=%q, want %q", gotSocket, test.wantSocket)
			}
		})
	}
}

// TestRunExitCloseFailsOpenOnCloserError pins fail-open all the way through
// the closer itself: an error closing terminals must never cost the human
// their SessionEnd hook exit code.
func TestRunExitCloseFailsOpenOnCloserError(t *testing.T) {
	jailExitClosePaths(t)
	original := exitCloseTerminals
	originalEnv := exitCloseEnv
	t.Cleanup(func() {
		exitCloseTerminals = original
		exitCloseEnv = originalEnv
	})
	exitCloseTerminals = func(context.Context, string, kill.ViewportDeps) ([]int, []string, error) {
		return nil, nil, errors.New("boom")
	}
	exitCloseEnv = func(key string) string {
		if key == "TMUX" {
			return "/tmp/tmux-501/cc-500-1-1,9999,0"
		}
		if key == "TMUX_PANE" {
			return "%0"
		}
		return ""
	}

	var stderr bytes.Buffer
	code := runExitClose(strings.NewReader(
		`{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`,
	), &stderr)
	if code != 0 {
		t.Fatalf("exit=%d, want 0 (fail-open)", code)
	}
	if !strings.Contains(stderr.String(), "close terminals (fail-open): boom") {
		t.Fatalf("stderr=%q, missing the fail-open closer error", stderr.String())
	}
}

// TestRunExitCloseReportsSkippedAndClosed pins that a skipped viewport is
// always named on stderr — never silent — and that a real close is reported
// too, both alongside the always-0 exit code.
func TestRunExitCloseReportsSkippedAndClosed(t *testing.T) {
	jailExitClosePaths(t)
	original := exitCloseTerminals
	originalEnv := exitCloseEnv
	t.Cleanup(func() {
		exitCloseTerminals = original
		exitCloseEnv = originalEnv
	})
	exitCloseTerminals = func(context.Context, string, kill.ViewportDeps) ([]int, []string, error) {
		return []int{4242}, []string{"client 9: parent 10 is \"Code Helper\", not a terminal shell"}, nil
	}
	exitCloseEnv = func(key string) string {
		if key == "TMUX" {
			return "/tmp/tmux-501/cc-500-1-1,9999,0"
		}
		if key == "TMUX_PANE" {
			return "%0"
		}
		return ""
	}

	var stderr bytes.Buffer
	code := runExitClose(strings.NewReader(
		`{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`,
	), &stderr)
	if code != 0 {
		t.Fatalf("exit=%d, want 0", code)
	}
	if !strings.Contains(stderr.String(), `left open — client 9: parent 10 is "Code Helper", not a terminal shell`) {
		t.Fatalf("stderr=%q, missing the named skip reason", stderr.String())
	}
	if !strings.Contains(stderr.String(), "closed 1 terminal(s) [4242]") {
		t.Fatalf("stderr=%q, missing the closed report", stderr.String())
	}
}

// jailExitClosePaths gives the hook a sid dir to probe for a reload lock; no
// lock file there means "not a reload", which is the case every guard test
// above assumes.
func jailExitClosePaths(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PFM_HOME", home)
	t.Setenv("PFM_SID_DIR", filepath.Join(home, "sid"))
}
