package seat

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProcJailerKillsARealStubbornDescendantAndItsPIDDisappears(t *testing.T) {
	command := exec.Command(
		"sh",
		"-c",
		`trap '' TERM; (trap '' TERM; while :; do sleep 30; done) & printf '%s\n' "$!"; wait`,
	)
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	rootPID := command.Process.Pid
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	waited := false
	defer func() {
		// The negative target is the exact group this test created.
		_ = syscall.Kill(-rootPID, syscall.SIGKILL)
		if !waited {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
			}
		}
	}()

	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil {
		t.Fatalf("read stubborn descendant pid: %v", err)
	}
	childPID, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || childPID <= 1 {
		t.Fatalf("stubborn descendant pid = %q", line)
	}

	jailer := ProcJailer{Root: "/proc"}
	jail, err := jailer.Capture(context.Background(), rootPID)
	if err != nil {
		t.Fatalf("Capture() error = %v", err)
	}
	if jail.GroupID != rootPID {
		t.Fatalf("captured group = %d, want root %d", jail.GroupID, rootPID)
	}
	if err := syscall.Kill(-jail.GroupID, syscall.SIGTERM); err != nil {
		t.Fatalf("send TERM to stubborn group: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if err := syscall.Kill(childPID, 0); err != nil {
		t.Fatalf("descendant did not ignore TERM: %v", err)
	}

	cleanupContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := jailer.Kill(cleanupContext, jail); err != nil {
		t.Fatalf("Kill() error = %v", err)
	}
	select {
	case <-done:
		waited = true
	case <-cleanupContext.Done():
		t.Fatalf("root pid %d survived cleanup: %v", rootPID, cleanupContext.Err())
	}
	for {
		err := syscall.Kill(childPID, 0)
		if errors.Is(err, syscall.ESRCH) {
			break
		}
		if err != nil {
			t.Fatalf("probe descendant pid %d: %v", childPID, err)
		}
		select {
		case <-cleanupContext.Done():
			t.Fatalf("stubborn descendant pid %d still exists after cleanup", childPID)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestProcJailerRefusesAReusedRootIdentity(t *testing.T) {
	jailer := ProcJailer{Root: "/proc"}
	command := exec.Command("sleep", "30")
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		_ = command.Wait()
	}()
	jail, err := jailer.Capture(context.Background(), command.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	jail.RootStartTicks++
	err = jailer.Kill(context.Background(), jail)
	if err == nil || !strings.Contains(err.Error(), "changed before cleanup") {
		t.Fatalf("Kill() error = %v, want recycled-pid refusal", err)
	}
	if signalErr := syscall.Kill(command.Process.Pid, 0); signalErr != nil {
		t.Fatalf("identity mismatch killed unrelated process: %v", signalErr)
	}
}

func TestInspectJailedGroupSkipsReleasedUnrelatedTuple(t *testing.T) {
	procRoot := t.TempDir()
	writeProcessFixture(t, procRoot, 100, 1, []string{"root"})
	writeProcessStatFixture(t, procRoot, 100, 1, 100, 100, 1, 1000)
	writeProcessFixture(t, procRoot, 101, 100, []string{"member"})
	writeProcessStatFixture(t, procRoot, 101, 100, 100, 100, 1, 1010)
	writeProcessFixture(t, procRoot, 200, 1, []string{"unrelated"})
	// Linux can expose an exiting task after directory enumeration with this
	// exact tuple. It is not a process-group identity and must be ignored by
	// the group walk, while the captured root and its valid member still prove
	// that the group is live.
	writeProcessStatFixture(t, procRoot, 200, 0, -1, -1, 0, 2000)

	live, err := inspectJailedGroup(context.Background(), procRoot, ProcessGroupJail{
		RootPID: 100, RootStartTicks: 1000, GroupID: 100, SessionID: 100,
	})
	if err != nil || !live {
		t.Fatalf("inspectJailedGroup()=(%v, %v), want live group with released unrelated tuple skipped", live, err)
	}
}

func TestInspectJailedGroupRejectsPartialNegativeGroupTuple(t *testing.T) {
	procRoot := t.TempDir()
	writeProcessFixture(t, procRoot, 100, 1, []string{"root"})
	writeProcessStatFixture(t, procRoot, 100, 1, 100, 100, 1, 1000)
	writeProcessFixture(t, procRoot, 101, 100, []string{"member"})
	writeProcessStatFixture(t, procRoot, 101, 100, 100, 100, 1, 1010)
	writeProcessFixture(t, procRoot, 201, 1, []string{"partial"})
	// A negative process-group field without the complete released-task
	// marker is malformed and must remain an inspection error.
	writeProcessStatFixture(t, procRoot, 201, 0, -1, 100, 1, 2010)

	_, err := inspectJailedGroup(context.Background(), procRoot, ProcessGroupJail{
		RootPID: 100, RootStartTicks: 1000, GroupID: 100, SessionID: 100,
	})
	if err == nil || !strings.Contains(err.Error(), "parse proc process group id") {
		t.Fatalf("inspectJailedGroup() error=%v, want malformed negative group identity error", err)
	}
}

func TestProcJailerCaptureRejectsReleasedRootTuple(t *testing.T) {
	procRoot := t.TempDir()
	writeProcessFixture(t, procRoot, 100, 1, []string{"root"})
	writeProcessStatFixture(t, procRoot, 100, 0, -1, -1, 0, 1000)

	_, err := (ProcJailer{Root: procRoot}).Capture(context.Background(), 100)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Capture() error=%v, want released-root absence", err)
	}
}

func writeProcessStatFixture(t *testing.T, root string, pid, parent, group, session, threads int, start uint64) {
	t.Helper()
	fields := []string{"S", strconv.Itoa(parent), strconv.Itoa(group), strconv.Itoa(session)}
	for len(fields) <= 19 {
		fields = append(fields, "0")
	}
	fields[17] = strconv.Itoa(threads)
	fields[19] = strconv.FormatUint(start, 10)
	path := filepath.Join(root, strconv.Itoa(pid), "stat")
	if err := os.WriteFile(
		path,
		[]byte(strconv.Itoa(pid)+" (fixture process) "+strings.Join(fields, " ")+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
}
