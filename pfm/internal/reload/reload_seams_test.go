package reload

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/hostfixture"
)

// TestRunRefusesWithNoTmuxClientOnAHostWithoutTmux pins hostfixture case 4
// (NoTmux) for reload: on a host whose tmux is missing (the fixture scripts
// FakeRunner's tmux lookup to ENOENT — the same shape doctor and spawn read
// as "tmux not installed"), Run is handed no tmux client and must refuse by
// name instead of panicking on a nil interface.
// Stays serial: hostfixture.NoTmux jails through t.Setenv, which forbids a
// parallel test in the same body.
func TestRunRefusesWithNoTmuxClientOnAHostWithoutTmux(t *testing.T) {
	base := hostfixture.NoTmux(t)
	_, err := Run(
		context.Background(),
		Request{
			Engine:     pfmengine.Claude,
			SocketPath: "/tmp/tmux-1000/probe-1",
			Pane:       "%1",
			Account:    1,
			AccountIDs: []int{1},
		},
		Options{SIDDir: base.Values.SIDDir, Delay: -1, Poll: -1, Clock: clock.Real},
		nil,
		nil,
		nil,
	)
	if err == nil || err.Error() != "reload requires a tmux client" {
		t.Fatalf("Run() on a no-tmux host error = %v, want the named tmux-client refusal", err)
	}
}

// TestInFlightReadsALeftoverLockAsNotInFlight pins hostfixture case 9
// (StaleArtifacts) for reload.InFlight: a lock FILE surviving a crashed
// writer (the fixture's ReloadLock — a leftover artifact naming an already-
// exited pid) carries no live flock, because flock releases automatically
// the moment its owning process exits or closes the fd. InFlight must read
// that as "not in flight", never as a reload stuck forever.
// Stays serial: hostfixture.StaleArtifacts jails through t.Setenv, which
// forbids a parallel test in the same body.
func TestInFlightReadsALeftoverLockAsNotInFlight(t *testing.T) {
	fixture := hostfixture.StaleArtifacts(t)
	socketName := "cc-1700000000-1-1"
	pane := "%0"
	lockPath := LockPath(fixture.Values.SIDDir, socketName, pane)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatalf("create reload lock directory: %v", err)
	}
	// The content mirrors the fixture's own leftover-artifact shape (a
	// crashed writer's stale pid) — InFlight probes the flock, not the
	// content, so this is documentation, not the assertion.
	if err := os.WriteFile(lockPath, []byte(strconv.Itoa(fixture.StalePID)), 0o600); err != nil {
		t.Fatalf("write leftover reload lock: %v", err)
	}
	inFlight, err := InFlight(fixture.Values.SIDDir, socketName, pane)
	if err != nil {
		t.Fatalf("InFlight() on a leftover lock error = %v, want a clean read", err)
	}
	if inFlight {
		t.Fatal("InFlight() = true for a leftover lock file nobody holds, want false")
	}
}
