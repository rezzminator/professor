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
		command := exec.Command(os.Args[0], "-test.run=^"+name+"$", "-test.v")
		command.Env = append(os.Environ(), ownLauncherHelper+"=1")
		output, err := command.CombinedOutput()
		if err != nil ||
			testjail.ChildOutput(output) != `=== RUN   TestNewChatPickerOffersALauncherRegisteredOnlyInItsOwnPackage
--- PASS: TestNewChatPickerOffersALauncherRegisteredOnlyInItsOwnPackage (0.00s)
PASS
` {
			t.Fatalf("own-launcher proof failed: %v\n%s", err, output)
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
