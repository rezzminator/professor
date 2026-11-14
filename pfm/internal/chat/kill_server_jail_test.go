//go:build jail

package chat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestEndNowEndsATmuxChatAndConfirmsItsSocketGone: pfm restart's worker ends a
// tmux chat in-process, and the call returns nil only once the socket no
// longer answers.
func TestEndNowEndsATmuxChatAndConfirmsItsSocketGone(t *testing.T) {
	testjail.TmuxServer(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Fatalf("tmux is required in the fence: %v", err)
	}
	root := testjail.ShortRoot(t)
	values := paths.Values{
		TmuxDir: filepath.Join(root, "t"),
		SIDDir:  filepath.Join(root, "s"),
		StateDB: filepath.Join(root, "pfm.db"),
	}
	for _, dir := range []string{values.TmuxDir, values.SIDDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	socket := "cc-endnow-1-1"
	socketPath := filepath.Join(values.TmuxDir, socket)
	start := exec.Command("tmux", "-S", socketPath, "-f", "/dev/null", "new-session", "-d", "-s", "bg", "sleep", "120")
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start probe server: %v: %s", err, output)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socketPath, "kill-server").Run() })
	chat := headless.Chat{ID: "sid-endnow", Socket: socket, Pane: "%0"}
	if err := EndNow(context.Background(), chat, values); err != nil {
		t.Fatalf("EndNow: %v", err)
	}
	if exec.Command("tmux", "-S", socketPath, "list-panes", "-a").Run() == nil {
		t.Fatal("the tmux server still answers after EndNow returned nil")
	}
}
