package hostfixture

import (
	"os/exec"
	"testing"
)

// NoServiceManager jails a fleet whose FakeRunner answers ENOENT for both
// systemctl and launchctl — the state installer's unit wiring, doctor and
// kill's service scope must report as "no service manager", not crash into
// a bare exec lookup error on whichever OS this happens to run on.
func NoServiceManager(t *testing.T) Base {
	t.Helper()
	base := newBase(t)
	for _, name := range []string{"systemctl", "launchctl"} {
		base.Runner.ScriptLookPath(name, "", &exec.Error{Name: name, Err: exec.ErrNotFound})
	}
	return base
}
