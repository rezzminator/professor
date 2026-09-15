package deps

import (
	"fmt"
	"os"
	"testing"

	"hostops/pfm/internal/paths"
)

// TestMain jails PFM_HOME for the whole package, so a test that never builds a
// jail of its own cannot resolve the operator's real home.
//
// It cannot use internal/testjail the way the other packages do: testjail
// imports THIS package, and an internal test file importing it back is a cycle
// Go rejects. The jail is therefore spelled out here instead of shared.
//
// BROKEN STATE: if the directory cannot be created this says so on stderr and
// leaves PFM_HOME unset, which makes paths.Resolve() refuse — the tests fail
// loudly rather than reaching a live account.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "pfm-deps-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "deps: no jailed home: %v\n", err)
		os.Exit(m.Run())
	}
	if err := os.Setenv(paths.EnvHome, home); err != nil {
		fmt.Fprintf(os.Stderr, "deps: jail home environment: %v\n", err)
		if cleanupErr := os.RemoveAll(home); cleanupErr != nil {
			fmt.Fprintf(os.Stderr, "deps: clean unused jailed home: %v\n", cleanupErr)
		}
		os.Exit(1)
	}
	code := m.Run()
	if err := os.RemoveAll(home); err != nil {
		fmt.Fprintf(os.Stderr, "deps: clean jailed home: %v\n", err)
		code = 1
	}
	os.Exit(code)
}
