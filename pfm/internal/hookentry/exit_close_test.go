package hookentry

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/kill"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

func TestRunExitCloseGuards(t *testing.T) {
	jailExitClosePaths(t)
	original, originalEnv := exitCloseTerminals, exitCloseIdentify
	t.Cleanup(func() { exitCloseTerminals, exitCloseIdentify = original, originalEnv })
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
			exitCloseIdentify = func(context.Context) (resolve.Identity, error) {
				if test.tmux == "" {
					return resolve.Identity{}, resolve.ErrNotInChat
				}
				socket, _, _ := strings.Cut(test.tmux, ",")
				return resolve.Identity{Backend: "tmux", SocketPath: socket, Pane: "%0"}, nil
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
	original, originalEnv := exitCloseTerminals, exitCloseIdentify
	t.Cleanup(func() { exitCloseTerminals, exitCloseIdentify = original, originalEnv })
	exitCloseTerminals = func(context.Context, string, kill.ViewportDeps) ([]int, []string, error) {
		return nil, nil, errors.New("boom")
	}
	exitCloseIdentify = fleetExitCloseIdentity
	var stderr bytes.Buffer
	code := ExitClose(strings.NewReader(`{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`), &stderr)
	if code != 0 || !strings.Contains(stderr.String(), "close terminals (fail-open): boom") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

func TestRunExitCloseReportsSkippedAndClosed(t *testing.T) {
	jailExitClosePaths(t)
	original, originalEnv := exitCloseTerminals, exitCloseIdentify
	t.Cleanup(func() { exitCloseTerminals, exitCloseIdentify = original, originalEnv })
	exitCloseTerminals = func(context.Context, string, kill.ViewportDeps) ([]int, []string, error) {
		return []int{4242}, []string{"client 9: parent 10 is \"Code Helper\", not a terminal shell"}, nil
	}
	exitCloseIdentify = fleetExitCloseIdentity
	var stderr bytes.Buffer
	code := ExitClose(strings.NewReader(`{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`), &stderr)
	if code != 0 ||
		!strings.Contains(stderr.String(), `left open — client 9: parent 10 is "Code Helper", not a terminal shell`) ||
		!strings.Contains(stderr.String(), "closed 1 terminal(s) [4242]") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

func fleetExitCloseIdentity(context.Context) (resolve.Identity, error) {
	return resolve.Identity{Backend: "tmux", SocketPath: "/tmp/tmux-501/cc-500-1-1", Pane: "%0"}, nil
}

func jailExitClosePaths(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("PFM_HOME", home)
	t.Setenv("PFM_SID_DIR", filepath.Join(home, "sid"))
}

func TestExitCloseDoesNotTrustAnUnprovedTmuxEnvironment(t *testing.T) {
	jailExitClosePaths(t)
	original, originalEnv := exitCloseTerminals, exitCloseIdentify
	t.Cleanup(func() { exitCloseTerminals, exitCloseIdentify = original, originalEnv })
	exitCloseIdentify = func(context.Context) (resolve.Identity, error) {
		return resolve.Identity{}, errors.New("read tmux caller failed")
	}
	exitCloseTerminals = func(context.Context, string, kill.ViewportDeps) ([]int, []string, error) {
		t.Error("closed a viewport without proving caller identity")
		return nil, nil, nil
	}
	var stderr bytes.Buffer
	if code := ExitClose(
		strings.NewReader(`{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`),
		&stderr,
	); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(stderr.String(), "left open —") {
		t.Fatalf("missing identity refusal: %s", stderr.String())
	}
}

func TestExitCloseLeavesHerdrViewportOpen(t *testing.T) {
	original := exitCloseIdentify
	t.Cleanup(func() { exitCloseIdentify = original })
	exitCloseIdentify = func(context.Context) (resolve.Identity, error) {
		return resolve.Identity{Backend: "herdr", SocketPath: "/h/herdr.sock", Pane: "w1:p1"}, nil
	}
	var stderr bytes.Buffer
	if code := ExitClose(
		strings.NewReader(`{"hook_event_name":"SessionEnd","reason":"prompt_input_exit"}`),
		&stderr,
	); code != 0 ||
		stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%s", code, &stderr)
	}
}
