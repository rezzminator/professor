//go:build jail || e2e || journey

package testjail

import (
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestMachineBinaryRefusesAHostEvenArmed: a binary built with the jail tag never runs on a host, the
// unit net armed or not: Run re-runs it in the fence.
func TestMachineBinaryRefusesAHostEvenArmed(t *testing.T) {
	host := &paths.MapEnv{Values: map[string]string{}}
	absent := filepath.Join(t.TempDir(), "dockerenv")
	if got := runsHere(unitBinary, true, host, absent); got {
		t.Fatalf("runsHere(unitBinary=%v, armed, host, %s) = true, want false", unitBinary, absent)
	}
}
