package hostfixture

import (
	"fmt"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// NoCreds jails a fleet with no ~/.credentials.json at all, and a
// FakeRunner "security" call scripted to answer the not-found exit status
// (44 — usagehook's own keychainNotFoundStatus) with nothing on stdout —
// the NOT-LOGGED-IN state reached by absence on both the file-based and
// Keychain-based paths.
func NoCreds(t *testing.T) Base {
	t.Helper()
	base := newBase(t)
	base.Runner.Script(
		[]string{"security", "find-generic-password"},
		deps.RunResult{ExitCode: 44},
		fmt.Errorf("security find-generic-password: exit status 44"),
	)
	return base
}
