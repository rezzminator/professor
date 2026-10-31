package testjail

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// pfm's suites run only in the fence, or on a GitHub-hosted CI runner. On a real machine a test
// reaches that machine's tmux, git and pfm state: a host run on 2026-10-31 overwrote the
// operator's live pfm.db. A test binary started anywhere else re-runs its package in the fence
// and never runs a test where it was started.
const (
	fenceEnv      = "PFM_DEV_FENCE" // set by infra/fence for every fence exec
	fenceMarker   = "/.dockerenv"   // exists only inside a container: an exported variable alone never passes
	fenceLauncher = ".claude/scripts/dev.sh"
)

func inFence() bool {
	_, err := os.Stat(fenceMarker)
	return err == nil && os.Getenv(fenceEnv) == "1"
}

func onHostedRunner() bool {
	return os.Getenv("GITHUB_ACTIONS") == "true" && os.Getenv("RUNNER_ENVIRONMENT") == "github-hosted"
}

// hostPathFlags name files on the starting machine, which the fence cannot see.
var hostPathFlags = []string{
	"-test.testlogfile", "-test.gocoverdir", "-test.coverprofile", "-test.outputdir",
	"-test.cpuprofile", "-test.memprofile", "-test.blockprofile", "-test.mutexprofile", "-test.trace",
}

// fenceArgs keeps the test binary's flags the fence can honour, dropping those that name host paths.
func fenceArgs(args []string) []string {
	kept := make([]string, 0, len(args))
	for _, arg := range args {
		name, _, _ := strings.Cut(arg, "=")
		drop := false
		for _, flag := range hostPathFlags {
			if name == flag || name == "-"+flag {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, arg)
		}
	}
	return kept
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// runInFence re-runs this binary's package through `dev.sh iso run` and returns its exit code.
// It never runs a test here: with no fence to reach, it refuses.
func runInFence() int {
	pkgDir, err := os.Getwd() // go test starts a test binary in its package directory
	if err != nil {
		return refuseHost("package directory unknown: %v", err)
	}
	root := pkgDir
	for {
		if _, statErr := os.Stat(filepath.Join(root, fenceLauncher)); statErr == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			return refuseHost("no %s above %s to reach the fence through", fenceLauncher, pkgDir)
		}
		root = parent
	}
	rel, err := filepath.Rel(root, pkgDir)
	if err != nil {
		return refuseHost("package %s outside checkout %s: %v", pkgDir, root, err)
	}
	inner := "cd " + shellQuote(rel) + " && go test -count=1 . -args"
	for _, arg := range fenceArgs(os.Args[1:]) {
		inner += " " + shellQuote(arg)
	}
	fmt.Fprintf(os.Stderr, "testjail: no test runs on this machine — running %s in the fence\n", rel)
	cmd := exec.Command("bash", filepath.Join(root, fenceLauncher), "iso", "run", inner)
	cmd.Dir, cmd.Stdout, cmd.Stderr = root, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() > 0 {
			return exit.ExitCode()
		}
		return refuseHost("could not run the fence: %v", err)
	}
	return 0
}

func refuseHost(format string, args ...any) int {
	fmt.Fprintf(os.Stderr, "testjail: REFUSED — pfm tests run only in the fence (dev.sh iso test pfm); "+format+"\n", args...)
	return 1
}
