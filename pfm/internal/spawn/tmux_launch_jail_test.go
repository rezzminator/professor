package spawn

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// tmux caps one client message at about 16 KiB, and new-session carries its
// pane command inside that message. A Codex role seat's launch line holds the
// whole composed fleet prompt, so it died at birth with "command too long".

// launchJail is a real tmux socket directory under a short jail root.
func launchJail(t *testing.T) (root, tmuxDir string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root = testjail.ShortRoot(t)
	tmuxDir = filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	t.Setenv(paths.EnvTmuxConf, "/dev/null")
	return root, tmuxDir
}

// launchQuery reads one pane format from the chat server on socket.
func launchQuery(t *testing.T, tmuxDir, socket, format string) string {
	t.Helper()
	path := filepath.Join(tmuxDir, socket)
	query := exec.Command("tmux", "-S", path, "display-message", "-p", "-t", socket+":", format)
	query.Env = append(os.Environ(), "TMUX=")
	output, err := query.CombinedOutput()
	if err != nil {
		t.Fatalf("tmux display-message %s: %v: %s", format, err, output)
	}
	return strings.TrimSpace(string(output))
}

func launchKillServer(t *testing.T, tmuxDir, socket string) {
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", filepath.Join(tmuxDir, socket), "kill-server")
		kill.Env = append(os.Environ(), "TMUX=")
		if output, err := kill.CombinedOutput(); err != nil {
			t.Logf("kill test server %s: %v: %s", socket, err, output)
		}
	})
}

// launchEventually polls check for up to ten seconds; the last failure names
// what never became true.
func launchEventually(t *testing.T, what string, check func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		ok, detail := check()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never held: %s", what, detail)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// launchLeftovers names every entry in the socket directory except socket.
func launchLeftovers(t *testing.T, tmuxDir, socket string) []string {
	t.Helper()
	entries, err := os.ReadDir(tmuxDir)
	if err != nil {
		t.Fatalf("read socket directory %s: %v", tmuxDir, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.Name() != socket {
			names = append(names, entry.Name())
		}
	}
	return names
}

func TestNewSessionLaunchesARunLongerThanATmuxMessage(t *testing.T) {
	root, tmuxDir := launchJail(t)
	marker := filepath.Join(root, "launched")
	// An env-assignment prefix, as a launch line may carry one, a 64 KiB
	// argument, and a target that execs sleep so the pane process is known.
	run := "PFM_LAUNCH_MARK='" + marker + "' sh -c 'touch \"$PFM_LAUNCH_MARK\"; exec sleep 120' pfm-launch '" +
		strings.Repeat("x", 64<<10) + "'"
	const socket = "cx-1800000011-1-1"
	tmux := TmuxSpawner{TmuxDir: tmuxDir}
	if err := tmux.NewSession(context.Background(), SessionSpec{
		Socket: socket, Session: socket, Window: "Codex", CWD: root,
		Width: 180, Height: 45, Run: run,
	}); err != nil {
		t.Fatalf("launch a %d-byte run: %v", len(run), err)
	}
	launchKillServer(t, tmuxDir, socket)

	start := launchQuery(t, tmuxDir, socket, "#{pane_start_command}")
	if !strings.HasPrefix(strings.Trim(start, `"`), "/bin/sh ") {
		t.Fatalf("an over-budget run did not launch through a script: pane_start_command starts %.80q", start)
	}
	launchEventually(t, "marker written", func() (bool, string) {
		_, err := os.Stat(marker)
		return err == nil, "stat marker: " + errString(err)
	})
	launchEventually(t, "launch script removed", func() (bool, string) {
		left := launchLeftovers(t, tmuxDir, socket)
		return len(left) == 0, "socket directory still holds " + strings.Join(left, ", ")
	})
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Fatalf("resolve sleep: %v", err)
	}
	want, err := filepath.EvalSymlinks(sleep)
	if err != nil {
		t.Fatalf("resolve sleep symlinks: %v", err)
	}
	launchEventually(t, "pane process is the target, not a lingering shell", func() (bool, string) {
		pid := launchQuery(t, tmuxDir, socket, "#{pane_pid}")
		exe, err := os.Readlink("/proc/" + pid + "/exe")
		if err != nil {
			return false, "read pane exe: " + err.Error()
		}
		return exe == want, "pane " + pid + " runs " + exe + ", want " + want
	})
}

func TestNewSessionKeepsAShortRunInline(t *testing.T) {
	root, tmuxDir := launchJail(t)
	const socket = "cx-1800000012-1-1"
	tmux := TmuxSpawner{TmuxDir: tmuxDir}
	if err := tmux.NewSession(context.Background(), SessionSpec{
		Socket: socket, Session: socket, Window: "Codex", CWD: root,
		Width: 180, Height: 45, Run: "sleep 120",
	}); err != nil {
		t.Fatalf("launch a short run: %v", err)
	}
	launchKillServer(t, tmuxDir, socket)
	if start := launchQuery(t, tmuxDir, socket, "#{pane_start_command}"); strings.Trim(start, `"`) != "sleep 120" {
		t.Fatalf("a short run did not launch inline: pane_start_command = %q", start)
	}
	if left := launchLeftovers(t, tmuxDir, socket); len(left) != 0 {
		t.Fatalf("a short run wrote %v into the socket directory", left)
	}
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
