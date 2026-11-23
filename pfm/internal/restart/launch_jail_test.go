//go:build jail

// Subject: the kernel's process-group hangup and wait4 on real children (a re-exec'd front, a sleeping child).

package restart

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// TestTheLaunchingChatsDeathDoesNotCutTheRestartShort plays the chat that
// typed pfm restart: the front runs in its own process group, as a pane's
// shell does, and the whole group is hung up and killed, as tmux kill-server
// does to a pane. The worker the front spawned must still finish.
func TestTheLaunchingChatsDeathDoesNotCutTheRestartShort(t *testing.T) {
	dir := t.TempDir()
	goPipe := filepath.Join(dir, "go")
	if err := syscall.Mkfifo(goPipe, 0o600); err != nil {
		t.Fatal(err)
	}
	front := exec.Command(os.Args[0], "-test.run=^$")
	front.Env = append(os.Environ(), frontRoleEnv+"="+dir)
	front.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := front.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := front.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		_ = front.Process.Kill()
		t.Fatalf("the front printed no worker pid: %v", err)
	}
	worker, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("worker pid line %q: %v", line, err)
	}
	group := front.Process.Pid
	if err := syscall.Kill(-group, syscall.SIGHUP); err != nil {
		t.Fatalf("hang up the front's group: %v", err)
	}
	_ = syscall.Kill(-group, syscall.SIGKILL)
	_ = front.Wait()
	// Release the worker once it waits at the go pipe: the open for writing fails until a reader is there.
	testwait.Until(t, fmt.Sprintf("worker pid %d to wait at its go pipe after the front's group died", worker),
		func() (bool, string) {
			release, err := os.OpenFile(goPipe, os.O_WRONLY|syscall.O_NONBLOCK, 0)
			if err != nil {
				return false, err.Error()
			}
			_, writeErr := release.WriteString("\n")
			return writeErr == nil && release.Close() == nil, fmt.Sprintf("write: %v", writeErr)
		})
	marker := filepath.Join(dir, "marker")
	finished := fmt.Sprintf("worker pid %d to finish after the front's group died", worker)
	testwait.Until(t, finished, func() (bool, string) {
		body, err := os.ReadFile(marker)
		if err != nil {
			return false, err.Error()
		}
		return strings.TrimSpace(string(body)) == "survived", string(body)
	})
	// The worker writes its marker before its exit line: the log is read once that line has landed.
	logPath := filepath.Join(dir, "worker.log")
	testwait.Until(t, fmt.Sprintf("worker pid %d to write its exit line to %s", worker, logPath),
		func() (bool, string) {
			body, err := os.ReadFile(logPath)
			if err != nil {
				return false, err.Error()
			}
			return strings.Contains(string(body), ExitMarker) && strings.HasSuffix(string(body), "\n"), string(body)
		})
	body, err := os.ReadFile(logPath)
	if err != nil || string(body) != "restart: exit 0\n" {
		t.Fatalf("worker log %s = %q, %v; want %q", logPath, body, err, "restart: exit 0\n")
	}
}

func TestChildExitedTellsARunningChildFromAFinishedOne(t *testing.T) {
	hold := testwait.HoldArgv()
	child := exec.Command(hold[0], hold[1:]...)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if exited, err := childExited(child.Process.Pid); err != nil || exited {
		t.Fatalf("a running child = %v, %v", exited, err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	testwait.Until(t, "a killed child to read exited", func() (bool, string) {
		exited, err := childExited(child.Process.Pid)
		if err != nil {
			t.Fatal(err)
		}
		return exited, "still running"
	})
}
