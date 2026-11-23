//go:build journey

package journey

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
	"github.com/rezzminator/professor/pfm/journey/harness"
)

// ownerVars is everything a fresh login of the owner has; no multiplexer, engine or test variable.
// GOCOVERDIR is the owner's too: prepare.sh exports it in every login, so each pfm writes its counters.
var ownerVars = []string{
	"HOME", "USER", "LOGNAME", "SHELL", "PATH", "LANG", "TMPDIR", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS",
	"GOCOVERDIR",
}

func TestMain(m *testing.M) {
	if !testjail.Disposable() {
		fmt.Fprintln(os.Stderr, "journey: refusing to run: not a disposable machine "+
			"(the machine infra/fence/journey/run.sh boots, or a GitHub-hosted runner)")
		os.Exit(2)
	}
	env := paths.OSEnv{}
	artifacts := env.Get("JOURNEY_ARTIFACTS")
	if artifacts != "" {
		cpuTable = filepath.Join(artifacts, "cpu.tsv")
		if err := os.MkdirAll(artifacts, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "journey: cpu table: FAIL create %s: %v\n", artifacts, err)
			os.Exit(2)
		}
		if err := os.WriteFile(cpuTable, []byte(cpuTableHeader), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "journey: cpu table: FAIL %v\n", err)
			os.Exit(2)
		}
	}
	meter, err := startMeter()
	if err != nil {
		fmt.Fprintf(os.Stderr, "journey: profile: FAIL %v\n", err)
		os.Exit(2)
	}
	elapsed := testwait.Stopwatch() // recorded in summary.md's wall line, never judged here
	code := m.Run()
	// The profile, green run or red: steps.tsv and waits.tsv from the walk, the coverage, summary.md.
	if artifacts == "" {
		fmt.Println("journey: profile: none written: JOURNEY_ARTIFACTS is unset")
	} else if err := profile(artifacts, env.Get("GOCOVERDIR"), elapsed(), meter); err != nil {
		fmt.Fprintf(os.Stderr, "journey: profile: FAIL %v\n", err)
		code = max(code, 1)
	} else {
		fmt.Printf("journey: profile: %s\n", filepath.Join(artifacts, "summary.md"))
	}
	os.Exit(code)
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
	// The install ping switched off, as prepare.sh exports it in every login and service: a direct read
	// (c.Read) runs no login, and its pfm would otherwise send the daily ping (pfm/internal/installping).
	vars = append(vars, "PFM_INSTALL_PING_OFFLINE=1")
	treeElapsed = testwait.Stopwatch()
	// Once the whole tree has run and closed its terminals: every pfm process left writes its coverage
	// counters as it exits, before TestMain merges them.
	t.Cleanup(func() { windDown(t) })
	harness.Run(t, tree(), State{Home: home, Release: "v" + release}, harness.Hooks{
		Env:       vars,
		Shell:     []string{shell, "-l", "-i"},
		Artifacts: env.Get("JOURNEY_ARTIFACTS"),
		AfterStep: logGate,
	})
}
