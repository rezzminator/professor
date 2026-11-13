package deps

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const hungProbeTimeout = 250 * time.Millisecond

// slowSelfDoctorTimeout bounds the slow-but-healthy self-doctor. The same bound
// covers its quick `doctor --help`, and the first exec of a freshly written stub
// costs up to ~553ms on macOS under suite load, so 1s stays above that and
// still far below the 30 s sleep the summary call must outrun.
const slowSelfDoctorTimeout = 1 * time.Second

func TestProbePlatformFiltering(t *testing.T) {
	entries := []Entry{{Name: "ps", Command: "ps", Required: true, Platforms: []string{"darwin"}}}
	linux := Probe(context.Background(), entries, ProbeOptions{GOOS: "linux"})
	if linux[0].State != StateSkipped || linux[0].Error != "not this platform" {
		t.Fatalf("linux filter=%#v", linux)
	}
	darwin := Probe(context.Background(), entries, ProbeOptions{
		GOOS:     "darwin",
		LookPath: func(string) (string, error) { return "/usr/bin/ps", nil },
	})
	if darwin[0].State != StateOK || darwin[0].Path != "/usr/bin/ps" {
		t.Fatalf("darwin ps=%#v", darwin[0])
	}
}

func TestConfiguredEngineDependenciesRemainOptionalForHostInstall(t *testing.T) {
	entries := Registry(Options{Home: t.TempDir()})
	for _, entry := range entries {
		if entry.Name != "claude" && entry.Name != "codex" {
			continue
		}
		if entry.Required {
			t.Errorf("configured engine %s is required, want optional capability", entry.Name)
		}
	}
}

func TestResolveRejectsRegisteredOffPlatformCommand(t *testing.T) {
	var command string
	switch runtime.GOOS {
	case "linux":
		command = "launchctl"
	case "darwin":
		command = "getent"
	default:
		t.Skip("registry platform contract is Linux/Darwin only")
	}
	directory := t.TempDir()
	writeProbeStub(t, directory, command, "exit 0")
	t.Setenv("PATH", directory)
	_, err := Resolve(command)
	if got, want := strings.NewReplacer(directory, "{dir}", command, "{command}", runtime.GOOS, "{goos}").
		Replace(fmt.Sprint(err)), "dependency {command} is not supported on {goos}"; got != want {
		t.Fatalf("Resolve error = %q, want %q", got, want)
	}
}

// TestProbeRecordsEachVersionProbe proves ProbeOptions.Runner is the seam a
// version probe crosses: obs cannot import deps (obs.Runner's own signature
// names deps.Runner), so the observed door lives at this boundary instead — a
// FakeRunner sees every VersionArgs invocation boundedOutputWithEnvironment
// would otherwise have run via a bare exec.CommandContext, and Probe's
// parsed result is unchanged by crossing it.
func TestProbeRecordsEachVersionProbe(t *testing.T) {
	runner := &FakeRunner{}
	runner.Script([]string{"/usr/bin/tmux", "-V"}, RunResult{Stdout: []byte("tmux 3.4\n"), ExitCode: 0}, nil)
	entries := []Entry{
		{
			Name: "tmux", Command: "/usr/bin/tmux", Required: true,
			VersionArgs: []string{"-V"}, Parse: prefixedVersion("tmux"),
		},
	}
	results := Probe(context.Background(), entries, ProbeOptions{
		GOOS: "linux", Timeout: ProbeTimeout, Runner: runner,
		LookPath: func(string) (string, error) { return "/usr/bin/tmux", nil },
	})
	if results[0].State != StateOK || results[0].Version != "3.4" {
		t.Fatalf("result=%#v, want OK 3.4 through the injected Runner", results[0])
	}
	calls := runner.Calls()
	if len(calls) != 1 || strings.Join(calls[0].Argv, " ") != "/usr/bin/tmux -V" {
		t.Fatalf("runner saw %v, want exactly one call carrying the version probe argv", calls)
	}
}

func writeProbeStub(t *testing.T, directory, name, body string) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := writeExecutableUnderForkLock(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}
