package hostfixture

import (
	"context"
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

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
