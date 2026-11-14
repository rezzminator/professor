package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// TestNameSyncApplyReannouncesLiveChats pins the re-announce pass of
// `pfm name-sync --apply`: each live chat server is re-announced through the
// seam by its socket path under the tmux directory; a server that reached
// panes is named with them, one that reached none prints nothing, and a
// failure is named on stderr and counted unverified without stopping the pass.
func TestNameSyncApplyReannouncesLiveChats(t *testing.T) {
	tmuxDir := "/tmuxdir"
	asked := []string{}
	reannounce := func(_ context.Context, socket string) ([]string, error) {
		asked = append(asked, socket)
		switch filepath.Base(socket) {
		case "cc-1-2-3":
			return []string{"w6:p2", "wA:p1"}, nil
		case "cc-4-5-6":
			return nil, nil
		case "cc-7-8-9":
			return nil, errors.New("herdr re-announce " + socket + ": list the sessions: no server running")
		default:
			return []string{"w2:p1"}, errors.New("herdr re-announce " + socket + " session s: the pane is gone")
		}
	}
	var stdout, stderr bytes.Buffer
	unverified := reannounceChatServers(
		context.Background(), reannounce, tmuxDir,
		[]string{"cc-1-2-3", "cc-4-5-6", "cc-7-8-9", "cc-0-0-1"}, &stdout, &stderr,
	)
	wantAsked := []string{"/tmuxdir/cc-1-2-3", "/tmuxdir/cc-4-5-6", "/tmuxdir/cc-7-8-9", "/tmuxdir/cc-0-0-1"}
	if strings.Join(asked, "|") != strings.Join(wantAsked, "|") {
		t.Fatalf("re-announced sockets = %q, want %q", asked, wantAsked)
	}
	wantOut := "herdr re-announce cc-1-2-3: 2 client(s) w6:p2, wA:p1\n" +
		"herdr re-announce cc-0-0-1: 1 client(s) w2:p1\n"
	if stdout.String() != wantOut {
		t.Errorf("stdout = %q, want %q (nothing for a server that reached no pane)", stdout.String(), wantOut)
	}
	wantErr := "pfm name-sync: herdr re-announce /tmuxdir/cc-7-8-9: list the sessions: no server running\n" +
		"pfm name-sync: herdr re-announce /tmuxdir/cc-0-0-1 session s: the pane is gone\n"
	if stderr.String() != wantErr {
		t.Errorf("stderr = %q, want %q", stderr.String(), wantErr)
	}
	if unverified != 2 {
		t.Errorf("unverified = %d, want 2 (one per failed server)", unverified)
	}
}
