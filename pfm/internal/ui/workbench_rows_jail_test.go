//go:build jail

package ui

// The launcher registry is process-global: the test re-executes the test binary so a launcher registered
// only here never reaches the other tests' shipped engines.

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"testing"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

const ownLauncherHelper = "PFM_UI_OWN_LAUNCHER_HELPER"

// TestNewChatPickerOffersALauncherRegisteredOnlyInItsOwnPackage pins that the
// new-chat picker reads the launcher registry, never a fixed engine list: an
// engine whose launcher is registered only in its own package (a fourth
// engine) is offered. The registration runs in a child process of this test
// binary, so every other test here keeps the shipped engines.
func TestNewChatPickerOffersALauncherRegisteredOnlyInItsOwnPackage(t *testing.T) {
	const name = "TestNewChatPickerOffersALauncherRegisteredOnlyInItsOwnPackage"
	if os.Getenv(ownLauncherHelper) != "1" {
		t.Parallel()
		// No -test.v: a verbose run prints the test's elapsed time, which differs run to run. Without it
		// a run that matched this test prints PASS alone, and a run that matched none adds a warning line.
		command := exec.Command(os.Args[0], "-test.run=^"+name+"$")
		command.Env = append(os.Environ(), ownLauncherHelper+"=1")
		output, err := command.CombinedOutput()
		if got := testjail.ChildOutput(output); err != nil || got != "PASS\n" {
			t.Fatalf("own-launcher proof: child exit = %v, output = %q, want a nil exit and %q", err, got, "PASS\n")
		}
		return
	}
	id := pfmengine.ID("zz")
	spawn.RegisterLauncher(id, ownLauncher{})
	if got := workbenchGoldenModel(80).newChatEngines(); !slices.Contains(got, id) {
		t.Fatalf("new-chat engines = %v, want the launcher registered as %s", got, id)
	}
}

type ownLauncher struct{}

func (ownLauncher) ComposerReady(string) bool { return true }

func (ownLauncher) Rename(
	context.Context, pfmcall.Screen, pfmcall.Target, string, spawn.Timings, spawn.Trace,
) (string, error) {
	return "", nil
}
