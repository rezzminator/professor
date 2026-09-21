package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/mockengine"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }

// TestBuiltBinaryAnswersByTheNameItIsInstalledUnder builds the real binary and
// runs it through symlinks named after each engine — the door every jail uses.
func TestBuiltBinaryAnswersByTheNameItIsInstalledUnder(t *testing.T) {
	goBinary, err := exec.LookPath("go")
	if err != nil {
		t.Skip("TOOLCHAIN-MISSING go: cannot build cmd/mock-engine")
	}
	root := t.TempDir()
	binary := filepath.Join(root, "mock-engine")
	build := exec.Command(goBinary, "build", "-o", binary, ".")
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOTELEMETRY=off")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, output)
	}
	scenario := filepath.Join(root, "scenario.json")
	if err := (mockengine.Scenario{Version: "9.9.9 (Fixture)"}).Write(scenario); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "codex", "opencode"} {
		link := filepath.Join(root, name)
		if err := os.Symlink(binary, link); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(link, "--version")
		command.Env = append(os.Environ(), mockengine.EnvScenario+"="+scenario)
		var stdout, stderr bytes.Buffer
		command.Stdout, command.Stderr = &stdout, &stderr
		if err := command.Run(); err != nil || strings.TrimSpace(stdout.String()) != "9.9.9 (Fixture)" {
			t.Fatalf("%s --version: err=%v stdout=%q stderr=%q", name, err, stdout.String(), stderr.String())
		}
	}
	// An argv[0] that names no engine is refused with the usage code, never
	// silently one engine.
	stray := filepath.Join(root, "2.1.238")
	if err := os.Symlink(binary, stray); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(stray, "--version")
	command.Env = append(os.Environ(), mockengine.EnvScenario+"="+scenario, mockengine.EnvEngine+"=")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	err = command.Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != mockengine.ExitUsage ||
		!strings.Contains(stderr.String(), "2.1.238") {
		t.Fatalf(
			"stray name: err=%v stderr=%q, want exit %d naming the basename",
			err,
			stderr.String(),
			mockengine.ExitUsage,
		)
	}
}
