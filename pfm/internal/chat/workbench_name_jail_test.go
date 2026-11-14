//go:build jail

package chat

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// TestWorkbenchNameReservesLiveAndKilledRosterNames reads the roster off a real
// tmux server: the live row is a socket the scan connects to and a pane it
// lists, so its subject is that server.
func TestWorkbenchNameReservesLiveAndKilledRosterNames(t *testing.T) {
	root := testjail.Fleet(t)
	dir := chatWorkbenchFixture(t, root)
	const liveID = "a1111111-1111-4111-8111-111111111111"
	const killedID = "b2222222-2222-4222-8222-222222222222"
	seedClaudeChat(t, root, liveID, `{"type":"custom-title","customTitle":"_SCRIBE:1"}`)
	seedClaudeChat(t, root, killedID, `{"type":"custom-title","customTitle":"_SCRIBE:2"}`)
	tmuxDir := filepath.Join(root, "tmux-"+fmt.Sprint(os.Getuid()))
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvTmuxDir, tmuxDir)
	t.Setenv("TMUX_TMPDIR", root)
	testjail.TmuxServer(t)
	ctx := context.Background()
	const socket = "cc-new-workbench-test"
	client := pfmtmux.Socket{Dir: tmuxDir}
	if output, err := client.Command(ctx, socket, "-f", "/dev/null", "new-session", "-d", "-s", socket, "sleep 120").
		CombinedOutput(); err != nil {
		t.Fatalf("start jailed seat: %v: %s", err, output)
	}
	t.Cleanup(func() {
		if err := client.KillServer(ctx, socket); err != nil {
			t.Errorf("clean jailed seat: %v", err)
		}
	})
	if err := atomicfile.Write(
		filepath.Join(root, "sid", socket+".%0"),
		[]byte(filepath.Join(root, "claude", "project", liveID+".jsonl")+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Kill(ctx, store.Killed{ID: killedID, KilledAt: 1}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	rows, err := Rows(ctx, io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	var live, killed bool
	for _, row := range rows {
		if row.Name == "_SCRIBE:1" && row.Kind.IsAddressable() && !row.Killed {
			live = true
		}
		if row.Name == "_SCRIBE:2" && row.Killed {
			killed = true
		}
	}
	if !live || !killed {
		t.Fatalf("jail roster must include live and killed rows: %#v", rows)
	}
	name, found, err := WorkbenchName(ctx, dir, io.Discard, nil)
	if err != nil || !found || name != "_SCRIBE:3" {
		t.Fatalf("WorkbenchName = %q, %t, %v", name, found, err)
	}
}
