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
	"github.com/rezzminator/professor/pfm/internal/professor"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

func chatWorkbenchFixture(t *testing.T, root string) string {
	t.Helper()
	project := filepath.Join(root, "acme")
	dir := filepath.Join(project, "docs", "scribe")
	for path, body := range map[string]string{
		professor.BaselinePath(project):               "{}",
		paths.WorkbenchManifest(dir):                  `{"prompt":"scribe.md","title":"Scribe","name":"_SCRIBE","effort":"xhigh"}`,
		filepath.Join(dir, ".professor", "scribe.md"): "You are scribe.",
	} {
		if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

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

func TestWorkbenchNameOutside(t *testing.T) {
	root := testjail.Fleet(t)
	dir := chatWorkbenchFixture(t, root)
	if name, found, err := WorkbenchName(
		context.Background(),
		dir,
		io.Discard,
		nil,
	); err != nil || !found ||
		name != "_SCRIBE:1" {
		t.Fatalf("inside control = %q, %t, %v", name, found, err)
	}
	name, found, err := WorkbenchName(context.Background(), filepath.Join(root, "acme", "src"), io.Discard, nil)
	if err != nil || found || name != "" {
		t.Fatalf("outside = %q, %t, %v", name, found, err)
	}
}

func TestWorkbenchNameReportsRosterFailure(t *testing.T) {
	root := testjail.Fleet(t)
	dir := chatWorkbenchFixture(t, root)
	blocker := filepath.Join(root, "blocker")
	if err := atomicfile.Write(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvCacheDB, filepath.Join(blocker, "index.db"))
	_, rosterErr := Rows(context.Background(), io.Discard, nil)
	name, found, err := WorkbenchName(context.Background(), dir, io.Discard, nil)
	if err == nil || rosterErr == nil || err.Error() != rosterErr.Error() || found || name != "" {
		t.Fatalf("failed roster = %q, %t, %v; want %v", name, found, err, rosterErr)
	}
}

func TestWorkbenchNameReportsInvalidManifest(t *testing.T) {
	root := testjail.Fleet(t)
	dir := chatWorkbenchFixture(t, root)
	if err := atomicfile.Write(paths.WorkbenchManifest(dir), []byte(`{"prompt":""}`), 0o600); err != nil {
		t.Fatal(err)
	}
	name, found, err := WorkbenchName(context.Background(), dir, io.Discard, nil)
	want := paths.WorkbenchManifest(dir) + `: "prompt" is required`
	if err == nil || err.Error() != want || name != "" || found {
		t.Fatalf("invalid name = %q, %t, %v; want %q", name, found, err, want)
	}
}
