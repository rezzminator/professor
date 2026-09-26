package spawn

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// NewSession must refuse an unreachable engine binary BEFORE a server is
// created. A pane whose command cannot resolve dies at launch and takes the
// fresh server with it, and the visible failure becomes "no server running" —
// tmux named, the engine never mentioned. That is exactly how an MCP daemon
// running under systemd's default PATH reported a missing `claude` on a live
// box: the spawn failed and nothing said why.
func TestNewSessionRefusesAnUnreachableBinaryBeforeCreatingAServer(t *testing.T) {
	dir := t.TempDir()
	tmux := TmuxSpawner{TmuxDir: dir}
	err := tmux.NewSession(context.Background(), SessionSpec{
		Socket:  "preflight-missing",
		Session: "preflight-missing",
		Window:  "w",
		CWD:     dir,
		Run:     "pfm-test-missing-engine-xyz --headless",
		Binary:  "pfm-test-missing-engine-xyz",
		Width:   80,
		Height:  24,
	})
	if err == nil {
		t.Fatal("NewSession accepted a binary that resolves nowhere")
	}
	for _, want := range []string{"pfm-test-missing-engine-xyz", "PATH"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %v, want it to name %q", err, want)
		}
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("read tmux dir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("a refused spawn still created %d entr(ies) under the socket dir", len(entries))
	}
}

// A configured absolute binary that does not exist on disk is the same
// refusal: the config names a file, the file is gone, and the pane must not
// be the one to discover it.
func TestNewSessionRefusesAMissingAbsoluteBinary(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "engines", "claude")
	tmux := TmuxSpawner{TmuxDir: dir}
	err := tmux.NewSession(context.Background(), SessionSpec{
		Socket:  "preflight-absolute",
		Session: "preflight-absolute",
		Window:  "w",
		CWD:     dir,
		Run:     missing,
		Binary:  missing,
		Width:   80,
		Height:  24,
	})
	if err == nil {
		t.Fatal("NewSession accepted an absolute binary that is not on disk")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error = %v, want it to name %q", err, missing)
	}
}

// A resolvable binary passes the preflight and the server is created — the
// gate refuses the broken state without taxing the healthy one.
func TestNewSessionAcceptsAResolvableBinary(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux binary is not installed")
	}
	// /tmp, not t.TempDir(): the socket path must stay inside sun_path.
	root, err := os.MkdirTemp("/tmp", "pfmpre")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	tmux := TmuxSpawner{TmuxDir: root}
	spec := SessionSpec{
		Socket:  "preflight-ok",
		Session: "preflight-ok",
		Window:  "w",
		CWD:     root,
		Run:     "sh -c 'sleep 30'",
		Binary:  "sh",
		Width:   80,
		Height:  24,
	}
	if err := tmux.NewSession(context.Background(), spec); err != nil {
		t.Fatalf("NewSession refused a resolvable binary: %v", err)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", filepath.Join(root, spec.Socket), "kill-server")
		_ = kill.Run()
	})
}
