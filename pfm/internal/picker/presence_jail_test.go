//go:build jail

package picker

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestParkedPickerPublishesAChatThatLaunchesWithoutAKeystroke pins root cause
// B for a launch: a picker that parked while nobody touched it did no pass
// until a key arrived, so a chat that started under it never reached the rows,
// the header counts or the cosmos. The parked poll now sees the new tmux socket
// without forking anything and runs a full pass for it.
func TestParkedPickerPublishesAChatThatLaunchesWithoutAKeystroke(t *testing.T) {
	testjail.TmuxServer(t)
	root, _ := presenceJail(t)
	updates := parkedPresenceStream(t)
	startCodexStatusPane(t, root, "cx-1800000021-1-1", `  NEW_CHAT · /work/example · Full Access\n`)
	awaitFullPass(t, updates, 5*time.Second, "a new chat's socket appeared")
}

// TestParkedPickerPublishesAChatThatDiesWithoutAKeystroke is the same law for a
// death: a chat whose server is killed (or whose host rebooted) must leave the
// rows, not stay on them as a live star until somebody presses a key.
func TestParkedPickerPublishesAChatThatDiesWithoutAKeystroke(t *testing.T) {
	testjail.TmuxServer(t)
	root, tmuxBinary := presenceJail(t)
	// The chat exists before the picker opens, so the picker parks WITH it.
	const socket = "cx-1800000022-1-1"
	startCodexStatusPane(t, root, socket, `  DOOMED · /work/example · Full Access\n`)
	updates := parkedPresenceStream(t)

	kill := exec.Command(tmuxBinary, "-L", socket, "kill-server")
	kill.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root)
	if output, err := kill.CombinedOutput(); err != nil {
		t.Fatalf("kill chat server: %v: %s", err, output)
	}
	awaitFullPass(t, updates, 5*time.Second, "a chat's server died")
}

func startCodexStatusPane(t *testing.T, tmuxTmpDir, socket, statusLine string) {
	t.Helper()
	if err := os.MkdirAll(tmuxTmpDir, 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(
		"tmux",
		"-f",
		"/dev/null",
		"-L",
		socket,
		"new-session",
		"-d",
		"-s",
		socket,
		"-n",
		"codex",
		"printf '"+statusLine+"'; sleep 120",
	)
	command.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+tmuxTmpDir)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start jailed codex pane %q: %v: %s", socket, err, output)
	}
	t.Cleanup(func() {
		killCommand := exec.Command("tmux", "-L", socket, "kill-server")
		killCommand.Env = command.Env
		_ = killCommand.Run()
	})
	want := strings.TrimSpace(strings.TrimSuffix(statusLine, `\n`))
	waitForTmuxPaneText(t, socket, command.Env, want, 5*time.Second)
}

func waitForTmuxPaneText(t *testing.T, socket string, environment []string, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		capture := exec.Command("tmux", "-L", socket, "capture-pane", "-p")
		capture.Env = environment
		output, err := capture.CombinedOutput()
		last = string(output)
		if err == nil && strings.Contains(last, want) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("tmux pane did not paint %q within %s; last capture=%q", want, timeout, last)
}
