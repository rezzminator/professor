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

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
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
		Socket: socket, Session: socket, Window: "Codex", CWD: root, Width: 180, Height: 45, Run: "tail -f /dev/null",
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
	command := "PFM_LAUNCH_MARK='" + marker + "' sh -c 'touch \"$PFM_LAUNCH_MARK\"; exec tail -f /dev/null' pfm-launch '" +
		strings.Repeat(
			"x",
			64<<10,
		) + "'"
	respawner := CommandTmux{LaunchDir: tmuxDir}
	if err := respawner.Respawn(ctx, socketPath, query("#{pane_id}"), root, command); err != nil {
		t.Fatalf("respawn with a %d-byte command: %v", len(command), err)
	}
	tail, err := exec.LookPath("tail")
	if err != nil {
		t.Fatalf("resolve tail: %v", err)
	}
	want, err := filepath.EvalSymlinks(tail)
	if err != nil {
		t.Fatalf("resolve tail symlinks: %v", err)
	}
	testwait.Until(
		t,
		"the respawned pane to run the target from a consumed script (exe "+want+")",
		func() (bool, string) {
			entries, err := os.ReadDir(tmuxDir)
			if err != nil {
				t.Errorf("read socket directory: %v", err)
				return true, err.Error()
			}
			pid := query("#{pane_pid}")
			exe, exeErr := paneExecutable(pid)
			_, markErr := os.Stat(marker)
			detail := "entries=" + strconv.Itoa(len(entries)) + " pane " + pid + " exe=" + exe
			return markErr == nil && exeErr == nil && exe == want && len(entries) == 1, detail
		},
	)
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
