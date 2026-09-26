package hostfixture

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// CredsFixture is ExpiredCreds's result: the jailed Base plus the
// credentials file path and the (already past) instant it claims to have
// expired at.
type CredsFixture struct {
	Base
	CredentialsPath string
	ExpiresAt       time.Time
}

// ExpiredCreds jails a fleet with a ~/.credentials.json whose
// claudeAiOauth.expiresAt sits 24 hours before the fixture's own Clock —
// the state usagehook, doctor's seat rows, headless and resolve must all
// report NOT-LOGGED-IN by name, never mistake for "no chats".
func ExpiredCreds(t *testing.T) CredsFixture {
	t.Helper()
	base := newBase(t)
	expiresAt := base.Clock.Now().Add(-24 * time.Hour)
	path := filepath.Join(base.Values.Home, ".credentials.json")
	body := fmt.Sprintf(
		`{"claudeAiOauth":{"accessToken":"expired-token","refreshToken":"expired-refresh","expiresAt":%d}}`,
		expiresAt.UnixMilli(),
	)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("hostfixture: write expired credentials: %v", err)
	}
	return CredsFixture{Base: base, CredentialsPath: path, ExpiresAt: expiresAt}
}

// NoCreds jails a fleet with no ~/.credentials.json at all, and a
// FakeRunner "security" call scripted to answer the not-found exit status
// (44 — usagehook's own keychainNotFoundStatus) with nothing on stdout —
// the same NOT-LOGGED-IN state ExpiredCreds proves, reached by absence
// rather than expiry, on both the file-based and Keychain-based paths.
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
