package chat

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestKillServerRecordsAChatStateTransition: KillServer walks the state door
// (spec § Middleware, `state`) — live to ended, comp=state, kind=chat —
// never the socket name it closed.
func TestKillServerRecordsAChatStateTransition(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := t.TempDir()
	tmuxDir := filepath.Join(root, "tmux")
	sidDir := filepath.Join(root, "sid")
	for _, dir := range []string{tmuxDir, sidDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	socket := "cc-obs-test-1-1"
	socketPath := filepath.Join(tmuxDir, socket)
	start := exec.Command(
		"tmux", "-S", socketPath, "-f", "/dev/null",
		"new-session", "-d", "-s", "bg", "sleep", "120",
	)
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start probe server: %v: %s", err, output)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", socketPath, "kill-server").Run()
	})

	resolved := paths.Values{
		TmuxDir: tmuxDir,
		SIDDir:  sidDir,
		FleetDB: filepath.Join(root, "fleet.db"),
	}
	ctx, recorder := obs.Test(t)
	if err := KillServer(ctx, resolved, socket); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, record := range recorder.Records() {
		if record.Message != "state.transition" {
			continue
		}
		if kind, _ := record.Field("kind"); kind != "chat" {
			continue
		}
		if next, _ := record.Field("next"); next == "ended" {
			found = true
		}
	}
	if !found {
		t.Fatalf("KillServer() wrote no chat->ended transition: %s", recorder.Raw())
	}
}
