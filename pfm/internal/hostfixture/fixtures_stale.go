package hostfixture

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"hostops/pfm/internal/deps"
)

// StaleArtifactsFixture is StaleArtifacts's result: the jailed Base plus
// every leftover artifact a crashed writer or an exited chat can leave
// behind.
type StaleArtifactsFixture struct {
	Base
	DeadSocket   string // a tmux socket file with no server behind it
	StalePIDFile string // names StalePID, a process that has already exited
	StalePID     int
	WALFile      string // fleet.db-wal left by a crashed writer
	ReloadLock   string // a reload lock naming StalePID
}

// StaleArtifacts jails a fleet holding a dead tmux socket file, a pid file
// naming an already-exited process, a fleet.db-wal left by a crashed
// writer, and a leftover reload lock — the state reap, stale, fleetdb and
// reload.InFlight must clear rather than treat as a live claim.
func StaleArtifacts(t *testing.T) StaleArtifactsFixture {
	t.Helper()
	base := newBase(t)

	socket := filepath.Join(base.Values.TmuxDir, "dead")
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatalf("hostfixture: create dead tmux socket file: %v", err)
	}

	stalePID := deadPID(t)

	pidFile := filepath.Join(base.Root, "stale.pid")
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(stalePID)), 0o600); err != nil {
		t.Fatalf("hostfixture: write stale pid file: %v", err)
	}

	wal := base.Values.FleetDB + "-wal"
	if err := os.MkdirAll(filepath.Dir(wal), 0o700); err != nil {
		t.Fatalf("hostfixture: create fleet.db directory: %v", err)
	}
	if err := os.WriteFile(wal, []byte("stale-wal-frames"), 0o600); err != nil {
		t.Fatalf("hostfixture: write stale fleet.db-wal: %v", err)
	}

	lock := filepath.Join(base.Root, "reload.lock")
	if err := os.WriteFile(lock, []byte(strconv.Itoa(stalePID)), 0o600); err != nil {
		t.Fatalf("hostfixture: write stale reload lock: %v", err)
	}

	return StaleArtifactsFixture{
		Base:         base,
		DeadSocket:   socket,
		StalePIDFile: pidFile,
		StalePID:     stalePID,
		WALFile:      wal,
		ReloadLock:   lock,
	}
}

// deadPID spawns a real, trivial child process, waits for it to exit, and
// hands back its now-dead pid — the same shape a crashed writer's leftover
// artifact names, without hardcoding a number that might collide with a
// real live process on this host. It runs through deps.Executable("sh"),
// the same registered-command seam every other production subprocess spawn
// in this module must cross (internal/deps's own source guard,
// TestProductionExecLiteralsAreRegisteredAndResolved, refuses a bare
// literal command name outside internal/deps).
func deadPID(t *testing.T) int {
	t.Helper()
	command := exec.Command(deps.Executable("sh"), "-c", "exit 0")
	if err := command.Run(); err != nil {
		t.Fatalf("hostfixture: run throwaway process for a dead pid: %v", err)
	}
	return command.Process.Pid
}
