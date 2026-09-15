package usagehook

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubKeychain installs a keychain reader for one test and restores the real
// one afterwards, so no test ever depends on what this machine's login
// keychain happens to hold.
func stubKeychain(t *testing.T, reader func(string) ([]byte, error)) {
	t.Helper()
	previous := keychainReader
	keychainReader = func(_ context.Context, service string) ([]byte, error) { return reader(service) }
	t.Cleanup(func() { keychainReader = previous })
}

// The service name is the ONLY link between a config directory and its secret.
// If this derivation drifts, every account silently reads as "no credential"
// on a keychain host — or, far worse, could resolve to a different account's
// entry — so it is pinned to a value computed from the documented rule.
func TestKeychainServiceDerivesTheConfigDirScopedName(t *testing.T) {
	const configDir = "example-config-dir/.claude"
	service := KeychainService(configDir)
	if !strings.HasPrefix(service, "Claude Code-credentials-") {
		t.Fatalf("service=%q, want the Claude Code credentials prefix", service)
	}
	suffix := strings.TrimPrefix(service, "Claude Code-credentials-")
	if len(suffix) != 8 {
		t.Fatalf("suffix=%q (len %d), want 8 hex characters", suffix, len(suffix))
	}
	// Two different directories must never collide onto one entry.
	if other := KeychainService(configDir + "2"); other == service {
		t.Fatalf("two config dirs derived the same service name %q", service)
	}
}

// The regression this whole file exists for: a macOS account can be fully
// logged in with NO .credentials.json anywhere on disk, because Claude Code
// keeps the credential in the login keychain. Reading only the file reports
// such an account as having no credentials at all.
func TestLoadCredentialReadsTheKeychainWhenNoCredentialsFileExists(t *testing.T) {
	configDir := t.TempDir()
	stubKeychain(t, func(service string) ([]byte, error) {
		if service != KeychainService(configDir) {
			t.Fatalf("service=%q, want %q", service, KeychainService(configDir))
		}
		return []byte(`{"claudeAiOauth":{"accessToken":"keychain-token"}}`), nil
	})
	credential, err := loadCredential(context.Background(), configDir)
	if err != nil {
		t.Fatalf("loadCredential: %v", err)
	}
	if credential.OAuth.AccessToken != "keychain-token" {
		t.Fatalf("token=%q, want the keychain token", credential.OAuth.AccessToken)
	}
}

// A file on disk is plantable by a jail or a test and must stay authoritative,
// so the keychain is consulted only as the fallback it is.
func TestLoadCredentialPrefersTheCredentialsFileOverTheKeychain(t *testing.T) {
	configDir := t.TempDir()
	if err := os.WriteFile(
		CredentialPath(configDir),
		[]byte(`{"claudeAiOauth":{"accessToken":"file-token"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	stubKeychain(t, func(string) ([]byte, error) {
		t.Fatal("keychain consulted even though a credentials file exists")
		return nil, nil
	})
	credential, err := loadCredential(context.Background(), configDir)
	if err != nil {
		t.Fatalf("loadCredential: %v", err)
	}
	if credential.OAuth.AccessToken != "file-token" {
		t.Fatalf("token=%q, want the file token", credential.OAuth.AccessToken)
	}
}

// A signed-out account holds a credential with an empty token. It is NOT
// absent, and reporting it as absent sends the reader hunting for a missing
// file when the only repair is an interactive login. Both shapes still count
// as "unavailable" so the snapshot fallback engages for either.
func TestLoadCredentialTellsSignedOutApartFromAbsent(t *testing.T) {
	configDir := t.TempDir()
	stubKeychain(t, func(string) ([]byte, error) {
		return []byte(`{"claudeAiOauth":{"accessToken":"","refreshToken":""}}`), nil
	})
	_, err := loadCredential(context.Background(), configDir)
	if !errors.Is(err, ErrSignedOut) {
		t.Fatalf("err=%v, want ErrSignedOut", err)
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err=%v, must not report a signed-out account as absent", err)
	}
	if !IsCredentialUnavailable(err) {
		t.Fatalf("err=%v, a signed-out account is still unavailable for a direct query", err)
	}
	if !strings.Contains(err.Error(), "claude /login") {
		t.Fatalf("err=%q, want the message to name the one repair", err)
	}
}

// Absence must name BOTH doors it looked behind. A message naming only the
// file is what made this bug unreadable for so long on a keychain host.
func TestLoadCredentialNamesBothSourcesWhenNeitherHoldsACredential(t *testing.T) {
	configDir := t.TempDir()
	stubKeychain(t, func(string) ([]byte, error) { return nil, os.ErrNotExist })
	_, err := loadCredential(context.Background(), configDir)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("err=%v, want it to wrap os.ErrNotExist so the snapshot fallback engages", err)
	}
	message := err.Error()
	if !strings.Contains(message, filepath.Base(CredentialPath(configDir))) {
		t.Fatalf("err=%q, want the credentials file named", message)
	}
	if !strings.Contains(message, KeychainService(configDir)) {
		t.Fatalf("err=%q, want the keychain service named", message)
	}
}

// "We failed to look" must never render as "nothing is there": a locked
// keychain or a denied ACL is a real failure, and treating it as absence would
// quietly park the account on a stale fallback forever.
func TestLoadCredentialSurfacesAKeychainFailureRatherThanReportingAbsence(t *testing.T) {
	configDir := t.TempDir()
	stubKeychain(t, func(string) ([]byte, error) {
		return nil, errors.New("User interaction is not allowed")
	})
	_, err := loadCredential(context.Background(), configDir)
	if err == nil {
		t.Fatal("loadCredential succeeded despite an unreadable keychain")
	}
	if IsCredentialUnavailable(err) {
		t.Fatalf("err=%v, a keychain we FAILED to read must not read as an absent credential", err)
	}
	if !strings.Contains(err.Error(), "User interaction is not allowed") {
		t.Fatalf("err=%q, want the underlying keychain failure preserved", err)
	}
}

// An access token in the keychain is short-lived — hours, against a refresh
// token good for weeks — so pfm working today must not depend on the token it
// read the first time. Every resolution re-reads the source, which is what
// lets the ACK refresh chain (401 -> make the CLI mint a new token -> retry)
// actually see the new one Claude Code just wrote back to the keychain.
func TestLoadCredentialRereadsTheKeychainSoARotatedTokenIsPickedUp(t *testing.T) {
	configDir := t.TempDir()
	token := "first-token"
	var reads int
	stubKeychain(t, func(string) ([]byte, error) {
		reads++
		return []byte(`{"claudeAiOauth":{"accessToken":"` + token + `"}}`), nil
	})
	first, err := loadCredential(context.Background(), configDir)
	if err != nil {
		t.Fatal(err)
	}
	// Stand in for the CLI refreshing the credential in place.
	token = "example-rotated-token"
	second, err := loadCredential(context.Background(), configDir)
	if err != nil {
		t.Fatal(err)
	}
	if first.OAuth.AccessToken != "first-token" || second.OAuth.AccessToken != "example-rotated-token" {
		t.Fatalf("first=%q second=%q, want the rotated token on the second read",
			first.OAuth.AccessToken, second.OAuth.AccessToken)
	}
	if reads != 2 {
		t.Fatalf("reads=%d, want one keychain read per resolution (no cached credential)", reads)
	}
}

// TestCredentialAvailableForwardsTheCallerContext pins cancellation ownership
// at the public credential probe seam. The prompt hook and sampler pass their
// request context here so a blocked keychain lookup cannot outlive the work
// that asked for it.
func TestCredentialAvailableForwardsTheCallerContext(t *testing.T) {
	configDir := t.TempDir()
	type contextKey struct{}
	wantValue := "caller-context"
	ctx := context.WithValue(context.Background(), contextKey{}, wantValue)
	previous := keychainReader
	keychainReader = func(got context.Context, service string) ([]byte, error) {
		if got.Value(contextKey{}) != wantValue {
			t.Fatalf("keychain context value = %v, want %q", got.Value(contextKey{}), wantValue)
		}
		if service != KeychainService(configDir) {
			t.Fatalf("service=%q, want %q", service, KeychainService(configDir))
		}
		return nil, os.ErrNotExist
	}
	t.Cleanup(func() { keychainReader = previous })

	err := CredentialAvailable(ctx, configDir)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("CredentialAvailable() error = %v, want wrapped os.ErrNotExist", err)
	}
}
