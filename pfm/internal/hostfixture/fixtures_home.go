package hostfixture

import (
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
