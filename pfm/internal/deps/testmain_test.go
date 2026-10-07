package deps_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestMain jails PFM_HOME for the whole package, so a test that never builds a
// jail of its own cannot resolve the operator's real home.
//
// It lives in the external test package: that is what lets it share
// internal/testjail with every other package, even though testjail imports
// this one.
//
// BROKEN STATE: if the jail cannot be set up, testjail.Run says so on stderr
// and returns a non-zero code — the package fails loudly rather than reaching
// a live account.
func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }

// TestPackageHomeIsTheTestjailJail: a deps test resolves PFM_HOME to the jail
// testjail.Run builds, with the config file it seeds beside it — not to an
// unset home, which paths.Resolve refuses, and not to a directory this package
// made for itself.
func TestPackageHomeIsTheTestjailJail(t *testing.T) {
	home := os.Getenv(paths.EnvHome)
	if home == "" {
		t.Fatalf("%s is unset: the package ran outside the testjail jail", paths.EnvHome)
	}
	info, err := os.Stat(home)
	if err != nil || !info.IsDir() {
		t.Fatalf("%s=%q is not an existing directory: %v", paths.EnvHome, home, err)
	}
	wantConfig := filepath.Join(home, "pfm.config.json")
	if got := os.Getenv(paths.EnvConfig); got != wantConfig {
		t.Fatalf("%s=%q, want the config testjail seeds in the jail home: %q", paths.EnvConfig, got, wantConfig)
	}
	if _, err := os.Stat(wantConfig); err != nil {
		t.Fatalf("the jail's seeded config is missing: %v", err)
	}
}
