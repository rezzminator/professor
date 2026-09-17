package hookentry

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/kill"
)

func TestRunExitCloseGuards(t *testing.T) {
	jailExitClosePaths(t)
	original, originalEnv := exitCloseTerminals, exitCloseEnv
	t.Cleanup(func() { exitCloseTerminals, exitCloseEnv = original, originalEnv })
	for _, test := range []struct {
		name, payload, tmux, wantSocket string
		wantCalled                      bool
	}{
		{name: "prompt exit on fleet socket closes", payload: `{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`, tmux: "/tmp/tmux-501/cc-500-1-1,9999,0", wantCalled: true, wantSocket: "/tmp/tmux-501/cc-500-1-1"},
		{name: "clear closes nothing", payload: `{"hook_event_name":"SessionEnd","reason":"clear"}`, tmux: "/tmp/tmux-501/cc-500-1-1,9999,0"},
		{name: "logout closes nothing", payload: `{"hook_event_name":"SessionEnd","reason":"logout"}`, tmux: "/tmp/tmux-501/cc-500-1-1,9999,0"},
		{name: "other reason closes nothing", payload: `{"hook_event_name":"SessionEnd","reason":"other"}`, tmux: "/tmp/tmux-501/cc-500-1-1,9999,0"},
		{name: "non SessionEnd closes nothing", payload: `{"hook_event_name":"SessionStart","reason":"prompt_input_exit"}`, tmux: "/tmp/tmux-501/cc-500-1-1,9999,0"},
		{name: "empty TMUX closes nothing", payload: `{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`},
		{name: "non fleet socket closes nothing", payload: `{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`, tmux: "/tmp/tmux-501/default,9999,0"},
		{name: "malformed fails open", payload: `{`, tmux: "/tmp/tmux-501/cc-500-1-1,9999,0"},
	} {
		t.Run(test.name, func(t *testing.T) {
			called, gotSocket := false, ""
			exitCloseTerminals = func(_ context.Context, socketPath string, _ kill.ViewportDeps) ([]int, []string, error) {
				called, gotSocket = true, socketPath
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
			if code := ExitClose(strings.NewReader(test.payload), &stderr); code != 0 {
				t.Fatalf("exit=%d stderr=%q", code, stderr.String())
			}
			if called != test.wantCalled || (called && gotSocket != test.wantSocket) {
				t.Fatalf("called=%v socket=%q, want %v %q", called, gotSocket, test.wantCalled, test.wantSocket)
			}
		})
	}
}

func TestRunExitCloseFailsOpenOnCloserError(t *testing.T) {
	jailExitClosePaths(t)
	original, originalEnv := exitCloseTerminals, exitCloseEnv
	t.Cleanup(func() { exitCloseTerminals, exitCloseEnv = original, originalEnv })
	exitCloseTerminals = func(context.Context, string, kill.ViewportDeps) ([]int, []string, error) {
		return nil, nil, errors.New("boom")
	}
	exitCloseEnv = fleetExitCloseEnv
	var stderr bytes.Buffer
	code := ExitClose(strings.NewReader(`{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`), &stderr)
	if code != 0 || !strings.Contains(stderr.String(), "close terminals (fail-open): boom") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

func TestRunExitCloseReportsSkippedAndClosed(t *testing.T) {
	jailExitClosePaths(t)
	original, originalEnv := exitCloseTerminals, exitCloseEnv
	t.Cleanup(func() { exitCloseTerminals, exitCloseEnv = original, originalEnv })
	exitCloseTerminals = func(context.Context, string, kill.ViewportDeps) ([]int, []string, error) {
		return []int{4242}, []string{"client 9: parent 10 is \"Code Helper\", not a terminal shell"}, nil
	}
	exitCloseEnv = fleetExitCloseEnv
	var stderr bytes.Buffer
	code := ExitClose(strings.NewReader(`{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`), &stderr)
	if code != 0 ||
		!strings.Contains(stderr.String(), `left open — client 9: parent 10 is "Code Helper", not a terminal shell`) ||
		!strings.Contains(stderr.String(), "closed 1 terminal(s) [4242]") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

func fleetExitCloseEnv(key string) string {
	if key == "TMUX" {
		return "/tmp/tmux-501/cc-500-1-1,9999,0"
	}
	if key == "TMUX_PANE" {
		return "%0"
	}
	return ""
}

func jailExitClosePaths(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PFM_HOME", home)
	t.Setenv("PFM_SID_DIR", filepath.Join(home, "sid"))
}
