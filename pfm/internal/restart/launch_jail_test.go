//go:build jail

// Subject: the kernel's process-group hangup and wait4 on real children (a re-exec'd front, a sleeping child).

package restart

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestTheLaunchingChatsDeathDoesNotCutTheRestartShort plays the chat that
// typed pfm restart: the front runs in its own process group, as a pane's
// shell does, and the whole group is hung up and killed, as tmux kill-server
// does to a pane. The worker the front spawned must still finish.
func TestTheLaunchingChatsDeathDoesNotCutTheRestartShort(t *testing.T) {
	dir := t.TempDir()
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
	marker := filepath.Join(dir, "marker")
	deadline := time.Now().Add(15 * time.Second)
	for {
		if body, err := os.ReadFile(marker); err == nil && strings.TrimSpace(string(body)) == "survived" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker pid %d never finished after the front's group died", worker)
		}
		time.Sleep(50 * time.Millisecond)
	}
	body, err := os.ReadFile(filepath.Join(dir, "worker.log"))
	if err != nil || string(body) != "restart: exit 0\n" {
		t.Fatalf("worker log = %q, %v", body, err)
	}
}

func TestChildExitedTellsARunningChildFromAFinishedOne(t *testing.T) {
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	if exited, err := childExited(child.Process.Pid); err != nil || exited {
		t.Fatalf("a running child = %v, %v", exited, err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		exited, err := childExited(child.Process.Pid)
		if err != nil {
			t.Fatal(err)
		}
		if exited {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("a killed child never read exited")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
