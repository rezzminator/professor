//go:build !jail && !e2e && !journey

package testjail

import (
	"path/filepath"
	"testing"

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
