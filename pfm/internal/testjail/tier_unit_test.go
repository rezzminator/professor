//go:build !jail && !e2e && !journey

package testjail

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestUnitBinaryRunsOnAHostOnceArmed: a binary built without the jail tag runs on a host with no
// fence marker and no hosted runner, once the unit net is armed.
func TestUnitBinaryRunsOnAHostOnceArmed(t *testing.T) {
	host := &paths.MapEnv{Values: map[string]string{}}
	absent := filepath.Join(t.TempDir(), "dockerenv")
	if got := runsHere(unitBinary, true, host, absent); !got {
		t.Fatalf("runsHere(unitBinary=%v, armed, host, %s) = false, want true", unitBinary, absent)
	}
}

// TestUnitNetKeychainIsEmpty: in this armed unit binary the registry's keychain command answers in
// process, on either kernel: a find-generic-password finds no item (exit 44, security's own stderr
// line), anything else is a named refusal.
func TestUnitNetKeychainIsEmpty(t *testing.T) {
	if !UnitNetArmed() {
		t.Fatal("testjail's own unit binary is not armed: the served keychain is never reached")
	}
	var command string
	registry := deps.Registry()
	for index := range registry {
		if registry[index].Name == deps.KeychainCommand {
			command = registry[index].Command
		}
	}
	for _, row := range []struct {
		argv []string
		want string
	}{
		{
			[]string{command, "find-generic-password", "-s", "Claude Code-credentials-0a1b2c3d", "-w"},
			"exit 44 stderr \"security: SecKeychainSearchCopyNext: The specified item could not be found in the" +
				" keychain.\\n\" err <nil>",
		},
		{
			[]string{command, "dump-keychain"},
			"exit 1 stderr \"\" err testjail: the unit net's keychain plays only find-generic-password, not [\"" +
				command + "\" \"dump-keychain\"]: fake it at the caller's runner seam",
		},
	} {
		result, err := deps.RealRunner{}.Run(context.Background(), row.argv, deps.RunOptions{})
		if got := fmt.Sprintf("exit %d stderr %q err %v", result.ExitCode, result.Stderr, err); got != row.want {
			t.Errorf("Run(%q) = %s, want %s", row.argv, got, row.want)
		}
	}
}
