package gather

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestProbeServerNeverReadsAFailureAsDead pins the one liveness answer: only
// tmux's own "no server" words are dead, a server past the bound is
// unresponsive, and every other failure is unknown with tmux's reason.
func TestProbeServerNeverReadsAFailureAsDead(t *testing.T) {
	cases := []struct {
		name      string
		script    string
		want      ServerState
		wantPanes []string
		wantErr   string
	}{
		{
			"panes",
			"printf '" + paneLine("%%1", "42", "0") + paneLine("%%2", "42", "1") + "'",
			ServerAlive,
			[]string{"%1", "%2"},
			"",
		},
		{
			// tmux 3.8 prints no pane_pid once a dead pane's descriptor closes.
			"exited",
			"printf '" + paneLine("%%3", "", "1") + "'",
			ServerAlive,
			[]string{"%3"},
			"",
		},
		{"unreadable", "printf 'garbage\\n'", ServerUnknown, nil, "returned 1 pane fields"},
		{"empty", "echo 'no current target' >&2; exit 1", ServerAlive, nil, ""},
		{"dead", "echo 'no server running on /tmp/x' >&2; exit 1", ServerDead, nil, ""},
		{"refused", "echo 'error connecting to /tmp/x (Connection refused)' >&2; exit 1", ServerDead, nil, ""},
		// The client connected, then its server ended before answering: tmux
		// 3.4 raced against kill-server said this 238 times in 300, and every
		// next probe said "no server running".
		{"lost", "echo 'server exited unexpectedly' >&2; exit 1", ServerDead, nil, ""},
		{"denied", "echo 'connect failed: permission denied' >&2; exit 1", ServerUnknown, nil, "permission denied"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "tmux")
			if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\n"+tc.script+"\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			state, panes, err := ProbeServer(context.Background(), binary, "/tmp/x")
			var ids []string
			for _, pane := range panes {
				ids = append(ids, pane.PaneID)
			}
			if state != tc.want || !slices.Equal(ids, tc.wantPanes) {
				t.Fatalf("ProbeServer() = %v %q %v, want %v %q", state, ids, err, tc.want, tc.wantPanes)
			}
			if tc.name == "panes" {
				pane := panes[1]
				if !pane.Dead || pane.PID != 42 || pane.TTY != "/dev/pts/7" || pane.CurrentCommand != "claude" ||
					pane.WindowIndex != 3 || pane.SessionName != "cc-1" || pane.Socket != "x" || panes[0].Dead {
					t.Fatalf("ProbeServer() panes = %+v, want every paneFormat field read", panes)
				}
			}
			if tc.name == "exited" && (!panes[0].Dead || panes[0].PID != 0) {
				t.Fatalf("ProbeServer() exited pane = %+v, want dead with PID 0", panes[0])
			}
			if tc.wantErr == "" && err != nil ||
				tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("ProbeServer() error = %v, want %q", err, tc.wantErr)
			}
		})
	}
	t.Run("hung", func(t *testing.T) {
		// exec, so the killed client is the process holding its pipes: the
		// probe returns at its bound, not after the door's pipe wait.
		binary := filepath.Join(t.TempDir(), "tmux")
		if err := testjail.WriteExecutable(binary, []byte("#!/bin/sh\nexec sleep 5\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		var state ServerState
		err := within(t, stoppedBound, map[string]func() error{"ProbeServer": func() error {
			var err error
			state, _, err = ProbeServer(stoppedProbe(context.Background()), binary, "/tmp/x")
			return err
		}})["ProbeServer"]
		want := "tmux server did not answer within 200ms: /tmp/x"
		if state != ServerUnresponsive || !errors.Is(err, ErrServerUnresponsive) || err.Error() != want {
			t.Fatalf("ProbeServer() over a hung server = %v %v, want unresponsive %q", state, err, want)
		}
	})
	t.Run("production bound", func(t *testing.T) {
		expired, cancel := context.WithDeadline(context.Background(), time.Unix(0, 0))
		defer cancel()
		err := Unresponsive(context.Background(), expired, "/tmp/x")
		if want := "tmux server did not answer within 2s: /tmp/x"; err == nil || err.Error() != want {
			t.Fatalf("Unresponsive() under a context carrying no bound = %v, want %q", err, want)
		}
	})
}

// paneLine is one paneFormat answer line, printf-escaped: session cc-1
// on /dev/pts/7 running claude in window 3.
func paneLine(paneID, pid, dead string) string {
	fields := []string{"cc-1", "@1", "win", "title", "/dev/pts/7", pid, paneID, "1", "/work", "claude", dead, "3"}
	return strings.Join(fields, "\\037") + "\\n"
}

// TestTmuxReadClassifiesAFailureAsEveryProbeDoes: a read of a socket with no
// server behind it is ErrServerGone, one past its bound ErrServerUnresponsive,
// and every other failure keeps tmux's reason and is neither.
func TestTmuxReadClassifiesAFailureAsEveryProbeDoes(t *testing.T) {
	live := context.Background()
	expired, cancel := context.WithDeadline(live, time.Unix(0, 0))
	defer cancel()
	read := []string{"show-options", "-qv", "-t", "cc-1", "@pfm_herdr"}
	const lead = "tmux show-options -qv -t cc-1 @pfm_herdr: "
	for _, tc := range []struct {
		name     string
		probeCtx context.Context
		stderr   string
		sentinel error
		want     string
	}{
		{
			"socket file gone", live, "error connecting to /t/cc-1 (No such file or directory)\n", ErrServerGone,
			lead + "no tmux server on socket: /t/cc-1",
		},
		{
			"no server running", live, "no server running on /t/cc-1\n", ErrServerGone,
			lead + "no tmux server on socket: /t/cc-1",
		},
		{
			"refused", live, "error connecting to /t/cc-1 (Connection refused)\n", ErrServerGone,
			lead + "no tmux server on socket: /t/cc-1",
		},
		{
			"permission denied", live, "error connecting to /t/cc-1 (Permission denied)\n", nil,
			lead + "<nil>: error connecting to /t/cc-1 (Permission denied)",
		},
		{
			"session missing", live, "can't find session: cc-1\n", nil,
			lead + "<nil>: can't find session: cc-1",
		},
		{
			"past its bound", expired, "", ErrServerUnresponsive,
			lead + "tmux server did not answer within 2s: /t/cc-1",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tmuxReadFailure(live, tc.probeCtx, &exec.ExitError{Stderr: []byte(tc.stderr)}, "/t/cc-1", read)
			gone, unresponsive := errors.Is(err, ErrServerGone), errors.Is(err, ErrServerUnresponsive)
			if err == nil || err.Error() != tc.want ||
				gone != (tc.sentinel == ErrServerGone) || unresponsive != (tc.sentinel == ErrServerUnresponsive) {
				t.Fatalf("tmuxReadFailure() = %v (gone %v, unresponsive %v), want %q wrapping %v",
					err, gone, unresponsive, tc.want, tc.sentinel)
			}
		})
	}
}

// TestTmuxReadGivesUpOnAFrozenServer pins TmuxRead's bound: a chat server that
// accepts its socket and never answers (stopped or wedged) fails the read
// within ProbeTimeout, as the fleet scan's own probe does, so one frozen server
// never hangs `pfm chat ls` or the MCP ls.
func TestTmuxReadGivesUpOnAFrozenServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	socketPath := filepath.Join(testjail.ShortRoot(t), "cc-1")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	held := make(chan net.Conn, 16)
	t.Cleanup(func() {
		_ = listener.Close()
		for len(held) > 0 {
			_ = (<-held).Close()
		}
	})
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			held <- conn // held open, never answered
		}
	}()
	done := make(chan error, 1)
	go func() {
		_, err := TmuxRead(context.Background(), socketPath, "show-options", "-qv", "-t", "cc-1", "@pfm_herdr")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), ProbeTimeout.String()) {
			t.Fatalf("read of a frozen server = %v, want an error naming the %s bound", err, ProbeTimeout)
		}
	case <-time.After(ProbeTimeout + 5*time.Second):
		t.Fatalf(
			"read of a frozen server still waiting after %s: its caller would hang",
			ProbeTimeout+5*time.Second,
		)
	}
}
