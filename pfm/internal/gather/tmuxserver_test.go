package gather

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

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
		state, _, err := ProbeServer(context.Background(), testjail.HungTmux(t, 1), "/tmp/x")
		if state != ServerUnresponsive || !errors.Is(err, ErrServerUnresponsive) {
			t.Fatalf("ProbeServer() over a hung server = %v %v, want unresponsive", state, err)
		}
	})
}

// paneLine is one paneFormat answer line, printf-escaped: session cc-1
// on /dev/pts/7 running claude in window 3.
func paneLine(paneID, pid, dead string) string {
	fields := []string{"cc-1", "@1", "win", "title", "/dev/pts/7", pid, paneID, "1", "/work", "claude", dead, "3"}
	return strings.Join(fields, "\\037") + "\\n"
}
