//go:build journey

package journey

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/journey/harness"
)

// budget bounds the whole tree, first step start to last step end: phase 2's cap, until the whole
// phase-2 tree is measured.
const budget = 600 * time.Second

// ownerVars is everything a fresh login of the owner has; no multiplexer, engine or test variable.
var ownerVars = []string{
	"HOME", "USER", "LOGNAME", "SHELL", "PATH", "LANG", "TMPDIR", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS",
}

func TestMain(m *testing.M) {
	if !testjail.Disposable() {
		fmt.Fprintln(os.Stderr, "journey: refusing to run: not a disposable machine "+
			"(the machine infra/fence/journey/run.sh boots, or a GitHub-hosted runner)")
		os.Exit(2)
	}
	os.Exit(m.Run())
}

func TestJourney(t *testing.T) {
	env := paths.OSEnv{}
	home, shell := env.Get("HOME"), env.Get("SHELL")
	if home == "" || shell == "" {
		t.Fatalf("journey: HOME=%q SHELL=%q: the owner's login is incomplete", home, shell)
	}
	version, err := os.ReadFile(filepath.Join("..", "..", "VERSION"))
	if err != nil {
		t.Fatalf("journey: read the tree's VERSION: %v", err)
	}
	ownerHome = home
	release, _, _ := strings.Cut(strings.TrimSpace(string(version)), "-")
	var vars []string
	for _, name := range ownerVars {
		value := env.Get(name)
		if name == "PATH" {
			// The owner's login shell puts ~/.local/bin first; a direct read needs it too.
			value = filepath.Join(
				home,
				".local",
				"bin",
			) + ":" + filepath.Join(
				home,
				".local",
				"share",
				"journey",
				"bin",
			) +
				":" + value
		}
		if value != "" {
			vars = append(vars, name+"="+value)
		}
	}
	treeStarted = clock.Real.Now()
	harness.Run(t, tree(), State{Home: home, Release: "v" + release}, harness.Hooks{
		Env:       vars,
		Shell:     []string{shell, "-l", "-i"},
		Artifacts: env.Get("JOURNEY_ARTIFACTS"),
		Budget:    budget,
		AfterStep: logGate,
	})
}
