package usagehook

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// fakeSecurity points the usage runner at a fake that answers the keychain tool with result.
func fakeSecurity(t *testing.T, result deps.RunResult) *deps.FakeRunner {
	t.Helper()
	fake := &deps.FakeRunner{}
	fake.Script([]string{keychainBinary, "find-generic-password"}, result, nil)
	previous := usageRunner
	usageRunner = fake
	t.Cleanup(func() { usageRunner = previous })
	return fake
}

// keychainBinary is the tool's path as the registry resolves it on macOS; the fake answers it on every kernel.
const keychainBinary = "/usr/bin/security"

func TestRunKeychainTrimsSuccessfulOutput(t *testing.T) {
	fakeSecurity(t, deps.RunResult{Stdout: []byte("  keychain-token \n")})

	got, err := runKeychain(context.Background(), keychainBinary, "service-name")
	if err != nil {
		t.Fatalf("runKeychain(): %v", err)
	}
	if string(got) != "keychain-token" {
		t.Fatalf("runKeychain() = %q, want trimmed token", got)
	}
}

func TestRunKeychainMapsSecurityNotFoundToCredentialAbsence(t *testing.T) {
	fakeSecurity(t, deps.RunResult{ExitCode: keychainNotFoundStatus})

	_, err := runKeychain(context.Background(), keychainBinary, "missing-service")
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runKeychain() error = %v, want os.ErrNotExist for security exit %d", err, keychainNotFoundStatus)
	}
}

func TestRunKeychainPreservesDeniedKeychainAsAnError(t *testing.T) {
	const detail = "User interaction is not allowed"
	fakeSecurity(t, deps.RunResult{Stderr: []byte(detail), ExitCode: 36})

	_, err := runKeychain(context.Background(), keychainBinary, "locked-service")
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
	fake := &deps.FakeRunner{}
	fake.Handle([]string{keychainBinary}, func(ctx context.Context, _ deps.RunCall) (deps.RunResult, error) {
		return deps.RunResult{ExitCode: -1}, ctx.Err()
	})
	previous := usageRunner
	usageRunner = fake
	t.Cleanup(func() { usageRunner = previous })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := runKeychain(ctx, keychainBinary, "cancelled-service")
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
