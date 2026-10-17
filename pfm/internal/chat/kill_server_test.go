package chat

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/headless"
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
		StateDB: filepath.Join(root, "pfm.db"),
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

func TestKillServerRefusesNativeSocket(t *testing.T) {
	root := t.TempDir()
	socket := filepath.Join(root, "herdr.sock")
	if err := os.WriteFile(socket, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := KillServer(
		context.Background(),
		paths.Values{TmuxDir: root, StateDB: filepath.Join(root, "state.db")},
		socket,
	)
	body, readErr := os.ReadFile(socket)
	if err == nil || !strings.Contains(err.Error(), "phase 2") || readErr != nil || string(body) != "preserve" {
		t.Fatalf("native end err=%v body=%q read=%v", err, body, readErr)
	}
}

func TestEndResolvedRefusesNative(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := EndResolved(
		context.Background(),
		headless.Chat{
			Backend: "herdr",
			ID:      "native",
			Live:    true,
			Socket:  filepath.Join(t.TempDir(), "herdr.sock"),
			Pane:    "w6:p3",
		},
		&stdout,
		&stderr,
		nil,
	)
	if code != 2 || !strings.Contains(stderr.String(), "phase 2") || stdout.Len() != 0 {
		t.Fatalf("native end code=%d out=%q err=%q", code, stdout.String(), stderr.String())
	}
}
