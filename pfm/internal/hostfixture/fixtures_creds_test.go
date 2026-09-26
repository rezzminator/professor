package hostfixture

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func TestExpiredCredsWritesACredentialsFileWhoseExpiresAtIsInThePast(t *testing.T) {
	fixture := ExpiredCreds(t)

	raw, err := os.ReadFile(fixture.CredentialsPath)
	if err != nil {
		t.Fatalf("read %s: %v", fixture.CredentialsPath, err)
	}
	var parsed struct {
		ClaudeAiOauth struct {
			ExpiresAt int64 `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("unmarshal %s: %v", fixture.CredentialsPath, err)
	}
	if parsed.ClaudeAiOauth.ExpiresAt != fixture.ExpiresAt.UnixMilli() {
		t.Fatalf("expiresAt in file = %d, want %d", parsed.ClaudeAiOauth.ExpiresAt, fixture.ExpiresAt.UnixMilli())
	}
	if !fixture.ExpiresAt.Before(fixture.Clock.Now()) {
		t.Fatalf("ExpiresAt %v is not before the fixture clock's now %v", fixture.ExpiresAt, fixture.Clock.Now())
	}
}

func TestNoCredsHasNoCredentialsFileAndAKeychainMissScript(t *testing.T) {
	base := NoCreds(t)

	credentialsPath := base.Values.Home + "/.credentials.json"
	if _, err := os.Stat(credentialsPath); !os.IsNotExist(err) {
		t.Fatalf("Stat(%s) = %v, want a not-exist error", credentialsPath, err)
	}

	result, err := base.Runner.Run(
		context.Background(),
		[]string{"security", "find-generic-password", "-s", "Claude Code-credentials", "-w"},
		deps.RunOptions{},
	)
	if err == nil {
		t.Fatal("scripted security find-generic-password returned nil error, want the not-found status")
	}
	if result.ExitCode != 44 {
		t.Fatalf("ExitCode = %d, want 44 (usagehook's keychainNotFoundStatus)", result.ExitCode)
	}
}
