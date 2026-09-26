package hostfixture

import (
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// NoHome jails a fleet, then unsets HOME and PFM_HOME — the state
// paths.Home() and every caller downstream of it (installer, fleetdb,
// store) must REFUSE rather than silently resolve to the operator's real
// account. Base.Values still names the fleet's original jailed home
// (Base is built before the unset); it is the fixture's own bookkeeping,
// not a value a NoHome caller should read.
func NoHome(t *testing.T) Base {
	t.Helper()
	base := newBase(t)
	unsetEnv(t, base, "HOME")
	unsetEnv(t, base, paths.EnvHome)
	return base
}

// ReadOnlyHome jails a fleet whose home directory exists but carries mode
// 0o555 — no writer bit, even for its own owner — the state an install, a
// fleetdb write or a store migration must report by name rather than fail
// with a bare permission error. Skipped by name when the fence itself runs
// as root, which ignores 0o555 the same way it ignores every other
// permission bit (a real host running this suite is never root).
func ReadOnlyHome(t *testing.T) Base {
	t.Helper()
	if isRoot() {
		t.Skip("hostfixture: running as root — read-only home is not enforceable")
	}
	base := newBase(t)
	if err := os.Chmod(base.Values.Home, 0o555); err != nil {
		t.Fatalf("hostfixture: chmod read-only home: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(base.Values.Home, 0o700); err != nil && !os.IsNotExist(err) {
			t.Errorf("hostfixture: restore home mode after ReadOnlyHome: %v", err)
		}
	})
	return base
}
