//go:build jail

package testjail

import (
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// TmuxServer lets the test run the real tmux for the rest of it: in a test
// process the tmux façade (internal/tmux) otherwise refuses the real binary.
// A fixture that starts a real server calls it, so a jail test, or a test
// whose subject is a real server, says so; a unit test scripts a
// deps.FakeRunner through tmux.WithClientRunner instead. Built only under the
// jail tag, so a unit test (untagged) that calls it fails to compile.
func TmuxServer(t testing.TB) {
	t.Helper()
	t.Cleanup(deps.AllowInTest("tmux"))
}
