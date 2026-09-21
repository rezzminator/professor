package hostfixture

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// epoch anchors every fixture's Fake clock to the same fixed instant, so a
// test asserting a duration between two fixture-derived timestamps never
// depends on when it happened to run.
var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// Base is what every hostfixture case hands its caller: the jailed fleet
// root testjail.Fleet built, the resolved paths.Values that jail computes
// to, an Env mirroring the real process environment testjail.Fleet just set
// (so a caller already taking the paths.Env seam sees exactly what a
// caller still reading the bare environment variable sees), a fresh
// FakeRunner for a case to script its host-command doors against, and a
// Fake clock a case's own expirations and timers are computed against.
type Base struct {
	Root   string
	Values paths.Values
	Env    *paths.MapEnv
	Runner *deps.FakeRunner
	Clock  *clock.Fake
}

// newBase jails a fresh fleet (testjail.Fleet: HOME, PFM_HOME, the tmux,
// sid, db and proc directories) and resolves it, then builds the Env
// mirror, FakeRunner and Fake clock every case scripts further.
func newBase(t *testing.T) Base {
	t.Helper()
	root := testjail.Fleet(t)
	values, err := paths.Resolve()
	if err != nil {
		t.Fatalf("hostfixture: resolve jailed paths: %v", err)
	}
	env := &paths.MapEnv{Values: map[string]string{}, HomeDir: values.Home}
	for _, entry := range os.Environ() {
		if name, value, ok := strings.Cut(entry, "="); ok {
			env.Values[name] = value
		}
	}
	return Base{
		Root:   root,
		Values: values,
		Env:    env,
		Runner: &deps.FakeRunner{},
		Clock:  clock.NewFake(epoch),
	}
}

// setEnv mutates the real process environment (via t.Setenv, so it is
// unwound with the test) and mirrors the same value into base.Env, keeping
// a caller reading through either seam — the bare environment read today's
// unmigrated callers still use, or the injected paths.Env a future caller
// takes — looking at identical state.
func setEnv(t *testing.T, base Base, name, value string) {
	t.Helper()
	t.Setenv(name, value)
	base.Env.Values[name] = value
}

// unsetEnv is setEnv's opposite: it clears name from both the real process
// environment and base.Env's mirror.
func unsetEnv(t *testing.T, base Base, name string) {
	t.Helper()
	t.Setenv(name, "")
	delete(base.Env.Values, name)
}

// isRoot reports whether this process itself runs as root — the dev fence
// runs its containers that way, which makes a filesystem-permission fixture
// like ReadOnlyHome unenforceable: root ignores 0o555 the same way it
// ignores every other permission bit, so the fixture must be skipped by
// name there rather than silently pass for the wrong reason.
func isRoot() bool { return os.Geteuid() == 0 }
