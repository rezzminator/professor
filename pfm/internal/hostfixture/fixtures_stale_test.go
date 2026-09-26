package hostfixture

import (
	"errors"
	"net"
	"os"
	"strconv"
	"syscall"
	"testing"
)

func TestStaleArtifactsDeadSocketAcceptsNoConnection(t *testing.T) {
	fixture := StaleArtifacts(t)

	info, err := os.Stat(fixture.DeadSocket)
	if err != nil {
		t.Fatalf("Stat(DeadSocket): %v", err)
	}
	if info.Mode()&os.ModeSocket != 0 {
		t.Fatalf("DeadSocket %q is a bound socket, want a plain leftover file", fixture.DeadSocket)
	}
	if _, err := net.Dial("unix", fixture.DeadSocket); err == nil {
		t.Fatal("net.Dial on DeadSocket succeeded, want a connection failure (no server behind it)")
	}
}

func TestStaleArtifactsStalePIDNamesAnAlreadyExitedProcess(t *testing.T) {
	fixture := StaleArtifacts(t)

	raw, err := os.ReadFile(fixture.StalePIDFile)
	if err != nil {
		t.Fatalf("read StalePIDFile: %v", err)
	}
	pid, err := strconv.Atoi(string(raw))
	if err != nil {
		t.Fatalf("parse StalePIDFile content %q: %v", raw, err)
	}
	if pid != fixture.StalePID {
		t.Fatalf("StalePIDFile pid = %d, want fixture.StalePID = %d", pid, fixture.StalePID)
	}

	err = syscall.Kill(pid, 0)
	if !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("syscall.Kill(%d, 0) = %v, want ESRCH (no such process)", pid, err)
	}
}

func TestStaleArtifactsWALAndReloadLockExistAtTheExpectedPaths(t *testing.T) {
	fixture := StaleArtifacts(t)

	if got, want := fixture.WALFile, fixture.Values.FleetDB+"-wal"; got != want {
		t.Fatalf("WALFile = %q, want %q", got, want)
	}
	if _, err := os.Stat(fixture.WALFile); err != nil {
		t.Fatalf("Stat(WALFile): %v", err)
	}
	raw, err := os.ReadFile(fixture.ReloadLock)
	if err != nil {
		t.Fatalf("read ReloadLock: %v", err)
	}
	if string(raw) != strconv.Itoa(fixture.StalePID) {
		t.Fatalf("ReloadLock content = %q, want the stale pid %d", raw, fixture.StalePID)
	}
}
