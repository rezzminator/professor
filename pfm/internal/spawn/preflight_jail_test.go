//go:build jail

package spawn

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A resolvable binary passes the preflight and the server is created — the
// gate refuses the broken state without taxing the healthy one.
func TestNewSessionAcceptsAResolvableBinary(t *testing.T) {
	testjail.TmuxServer(t)
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
