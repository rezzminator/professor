package hostfixture

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// OddPathsFixture is OddPaths's result: the jailed Base, now re-homed under
// Dir, plus Dir itself for a case that needs to build further odd-charactered
// state.
type OddPathsFixture struct {
	Base
	Dir string // the odd-charactered directory HOME now sits under
}

// OddPaths jails a fleet, then re-homes it under a directory whose name
// carries a space and a non-ASCII character — the state paths, inject's
// lock namespace, fleet's scan and archive must quote/escape through
// rather than split on or mis-encode. Base.Values.Home is updated to match,
// so a caller reading Base.Values, the real environment, Env, or a fresh
// paths.Resolve() after this call all agree on the odd home.
func OddPaths(t *testing.T) OddPathsFixture {
	t.Helper()
	base := newBase(t)
	odd := filepath.Join(base.Root, "od d été")
	home := filepath.Join(odd, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatalf("hostfixture: create odd-charactered home: %v", err)
	}
	setEnv(t, base, paths.EnvHome, home)
	setEnv(t, base, "HOME", home)
	base.Values.Home = home
	return OddPathsFixture{Base: base, Dir: odd}
}
