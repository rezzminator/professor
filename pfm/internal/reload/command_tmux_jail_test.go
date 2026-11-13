//go:build jail

package reload

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestReloadRespawnLaunchesACommandLongerThanATmuxMessage: respawn-pane
// carries its command inside one tmux client message, capped near 16 KiB, so
// a Codex seat's reload — its launch line holds the composed fleet prompt —
// could not come back. The long command launches through a one-shot script
// that execs the target and removes itself.
func TestReloadRespawnLaunchesACommandLongerThanATmuxMessage(t *testing.T) {
	testjail.TmuxServer(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := testjail.ShortRoot(t)
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	t.Setenv(paths.EnvTmuxConf, "/dev/null")
	const socket = "cx-1800000013-1-1"
	ctx := context.Background()
	if err := (spawn.TmuxSpawner{TmuxDir: tmuxDir}).NewSession(ctx, spawn.SessionSpec{
		Socket: socket, Session: socket, Window: "Codex", CWD: root, Width: 180, Height: 45, Run: "sleep 120",
	}); err != nil {
		t.Fatalf("create chat server: %v", err)
	}
	socketPath := filepath.Join(tmuxDir, socket)
	query := func(format string) string {
		command := exec.Command("tmux", "-S", socketPath, "display-message", "-p", "-t", socket+":", format)
		command.Env = append(os.Environ(), "TMUX=")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux display-message %s: %v: %s", format, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", socketPath, "kill-server")
		kill.Env = append(os.Environ(), "TMUX=")
		if output, err := kill.CombinedOutput(); err != nil {
			t.Logf("kill test server: %v: %s", err, output)
		}
	})
	marker := filepath.Join(root, "respawned")
	command := "PFM_LAUNCH_MARK='" + marker + "' sh -c 'touch \"$PFM_LAUNCH_MARK\"; exec sleep 120' pfm-launch '" +
		strings.Repeat("x", 64<<10) + "'"
	respawner := CommandTmux{LaunchDir: tmuxDir}
	if err := respawner.Respawn(ctx, socketPath, query("#{pane_id}"), root, command); err != nil {
		t.Fatalf("respawn with a %d-byte command: %v", len(command), err)
	}
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatalf("resolve sleep: %v", err)
	}
	want, err := filepath.EvalSymlinks(sleep)
	if err != nil {
		t.Fatalf("resolve sleep symlinks: %v", err)
	}
	var detail string
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		entries, err := os.ReadDir(tmuxDir)
		if err != nil {
			t.Fatalf("read socket directory: %v", err)
		}
		pid := query("#{pane_pid}")
		exe, exeErr := paneExecutable(pid)
		_, markErr := os.Stat(marker)
		detail = "entries=" + strconv.Itoa(len(entries)) + " pane " + pid + " exe=" + exe
		if markErr == nil && exeErr == nil && exe == want && len(entries) == 1 {
			return
		}
	}
	t.Fatalf("respawned pane never ran the target from a consumed script (want exe %s): %s", want, detail)
}

// paneExecutable resolves a pane process's executable through the platform's
// process reader: /proc on Linux, kern.procargs2 on macOS, which has no /proc.
func paneExecutable(pid string) (string, error) {
	number, err := strconv.Atoi(pid)
	if err != nil {
		return "", err
	}
	reader, ok := gather.NewProcFS("").(gather.ProcExecutable)
	if !ok {
		return "", errors.New("this platform's process reader names no executable")
	}
	exe, err := reader.Executable(number)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}
