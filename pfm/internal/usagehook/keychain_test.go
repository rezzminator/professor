package usagehook

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func writeKeychainFixture(t *testing.T, body string, exitCode int) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "security-fixture")
	script := "#!/bin/sh\n"
	if body != "" {
		script += "printf '%s' " + shellQuote(body) + "\n"
	}
	if exitCode != 0 {
		script += "exit " + strconv.Itoa(exitCode) + "\n"
	}
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write keychain fixture: %v", err)
	}
	return binary
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func TestRunKeychainTrimsSuccessfulOutput(t *testing.T) {
	binary := writeKeychainFixture(t, "  keychain-token \n", 0)

	got, err := runKeychain(context.Background(), binary, "service-name")
	if err != nil {
		t.Fatalf("runKeychain(): %v", err)
	}
	if string(got) != "keychain-token" {
		t.Fatalf("runKeychain() = %q, want trimmed token", got)
	}
}

func TestRunKeychainMapsSecurityNotFoundToCredentialAbsence(t *testing.T) {
	binary := writeKeychainFixture(t, "", keychainNotFoundStatus)

	_, err := runKeychain(context.Background(), binary, "missing-service")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runKeychain() error = %v, want os.ErrNotExist for security exit %d", err, keychainNotFoundStatus)
	}
}

func TestRunKeychainPreservesDeniedKeychainAsAnError(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "security-fixture")
	const detail = "User interaction is not allowed"
	script := "#!/bin/sh\nprintf '%s' '" + detail + "' >&2\nexit 36\n"
	if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
		t.Fatalf("write denied keychain fixture: %v", err)
	}

	_, err := runKeychain(context.Background(), binary, "locked-service")
	if err == nil {
		t.Fatal("runKeychain() succeeded for a denied keychain")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runKeychain() error = %v, denied keychain must not look absent", err)
	}
	if !strings.Contains(err.Error(), detail) {
		t.Fatalf("runKeychain() error = %q, want stderr detail %q", err, detail)
	}
}

func TestRunKeychainHonorsCallerCancellation(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "security-fixture")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nsleep 0.1\n"), 0o755); err != nil {
		t.Fatalf("write hanging keychain fixture: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runKeychain(ctx, binary, "cancelled-service")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runKeychain() error = %v, want context.Canceled", err)
	}
}

func TestRunKeychainUsesWaitDelay(t *testing.T) {
	fake := &deps.FakeRunner{}
	fake.Script([]string{"security", "find-generic-password"}, deps.RunResult{Stdout: []byte("token\n")}, nil)
	got, err := runKeychainWithRunner(context.Background(), "security", "service-name", fake)
	if err != nil {
		t.Fatalf("runKeychainWithRunner(): %v", err)
	}
	if string(got) != "token" {
		t.Fatalf("runKeychainWithRunner() = %q, want token", got)
	}
	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("Run calls = %d, want 1", len(calls))
	}
	if got := calls[0].Opts.WaitDelay; got != time.Second {
		t.Fatalf("RunOptions.WaitDelay = %s, want 1s", got)
	}
}
