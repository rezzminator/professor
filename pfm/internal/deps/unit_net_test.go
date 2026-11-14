package deps

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// armUnitNetForTest arms the refusal for one test and puts back what the binary had, so an
// unarmed binary's later tests start processes as before and an armed one's stay refused.
func armUnitNetForTest(t *testing.T) {
	t.Helper()
	was := unitNetArmed.Load()
	ArmUnitNet()
	t.Cleanup(func() { unitNetArmed.Store(was) })
}

// TestUnitNetRefusesEveryStartAndLookup: armed, RealRunner's Run and Start refuse a bare argv0 and
// a path argv0 alike before anything execs, with the refusal naming the argv and its two fixes;
// Resolve refuses a name even with a cached resolution of it, as exec.ErrNotFound, and Executable
// hands the bare name back.
func TestUnitNetRefusesEveryStartAndLookup(t *testing.T) {
	armUnitNetForTest(t)
	resetResolveCache(t)
	dir := t.TempDir()
	marker := filepath.Join(dir, "started")
	fixes := " — a unit test starts no process: fake it at the caller's runner seam (deps.FakeRunner)," +
		" or move the test to a _jail_test.go file"
	for _, argv := range [][]string{
		{"/bin/sh", "-c", "touch " + marker},
		{"sh", "-c", "touch " + marker},
		{"/bin/sleep", "30"},
	} {
		want := "UNIT-NET REFUSED start: " + strings.Join(argv, " ") + fixes
		result, err := RealRunner{}.Run(context.Background(), argv, RunOptions{})
		if err == nil || err.Error() != want || result.ExitCode != -1 {
			t.Errorf("Run(%q) = (exit %d, %v), want (exit -1, %q)", argv, result.ExitCode, err, want)
		}
		process, err := RealRunner{}.Start(context.Background(), argv, StartOptions{})
		if err == nil || err.Error() != want || process != nil {
			t.Errorf("Start(%q) = (%v, %v), want (nil, %q)", argv, process, err, want)
		}
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused start ran its shell: stat %s = %v, want not exist", marker, err)
	}

	stub := filepath.Join(dir, "git")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatalf("write the cached git stub %s: %v", stub, err)
	}
	t.Setenv("PATH", dir)
	rememberResolved("git", stub)
	wantLookup := "UNIT-NET REFUSED lookup: git (" + exec.ErrNotFound.Error() + ")" + fixes
	path, err := Resolve("git")
	if err == nil || err.Error() != wantLookup || path != "" {
		t.Errorf("Resolve(git) with %s cached = (%q, %v), want (\"\", %q)", stub, path, err, wantLookup)
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("Resolve(git) error %v does not wrap exec.ErrNotFound", err)
	}
	if got := Executable("git"); got != "git" {
		t.Errorf("Executable(git) = %q, want the bare name %q", got, "git")
	}
	named := filepath.Join(dir, "engine")
	if err := writeExecutableUnderForkLock(named, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("write the named engine stub %s: %v", named, err)
	}
	if path, err := Resolve(named); err != nil || path != named {
		t.Errorf("Resolve(%s) = (%q, %v), want the named path back: a path walks no PATH", named, path, err)
	}

	standIn := filepath.Join(dir, "engine-sync")
	served := &FakeRunner{}
	served.Script([]string{standIn, "version"}, RunResult{Stdout: []byte("engine-sync 9.0.0-testjail\n")}, nil)
	ServeUnitNet(standIn, served)
	t.Cleanup(func() { unitNetServed.Delete(standIn) })
	result, err := RealRunner{}.Run(context.Background(), []string{standIn, "version"}, RunOptions{})
	if err != nil || string(result.Stdout) != "engine-sync 9.0.0-testjail\n" {
		t.Errorf(
			"Run(served %s version) = (%q, %v), want the stand-in's reply played in process",
			standIn,
			result.Stdout,
			err,
		)
	}
	process, err := RealRunner{}.Start(context.Background(), []string{standIn, "global"}, StartOptions{})
	if err != nil || process == nil || process.Pid() != 4242 {
		t.Errorf(
			"Start(served %s global) = (%v, %v), want the stand-in runner's process (pid 4242)",
			standIn,
			process,
			err,
		)
	}
	if calls := served.Calls(); len(calls) != 1 || strings.Join(calls[0].Argv, " ") != standIn+" version" {
		t.Errorf("served runner Run calls = %v, want exactly %q", calls, standIn+" version")
	}
}

// TestUnitNetResolvesAServedRegistryCommand: armed, Resolve of a registry row's name answers the
// row's command path once ServeUnitNet serves it, and refuses the same name while nothing serves it.
func TestUnitNetResolvesAServedRegistryCommand(t *testing.T) {
	armUnitNetForTest(t)
	resetResolveCache(t)
	command := filepath.Join(t.TempDir(), "keychain-tool")
	saved := fixedCommands
	fixedCommands = append(append([]Entry(nil), saved...), Entry{Name: "keychain-tool", Command: command})
	t.Cleanup(func() { fixedCommands = saved })
	wantRefused := "UNIT-NET REFUSED lookup: keychain-tool (" + exec.ErrNotFound.Error() + ") — a unit test starts" +
		" no process: fake it at the caller's runner seam (deps.FakeRunner), or move the test to a _jail_test.go file"
	if path, err := Resolve("keychain-tool"); err == nil || err.Error() != wantRefused || path != "" {
		t.Errorf("Resolve(keychain-tool) unserved = (%q, %v), want (\"\", %q)", path, err, wantRefused)
	}
	ServeUnitNet(command, &FakeRunner{})
	t.Cleanup(func() { unitNetServed.Delete(command) })
	if path, err := Resolve("keychain-tool"); err != nil || path != command {
		t.Errorf("Resolve(keychain-tool) served = (%q, %v), want (%q, nil)", path, err, command)
	}
}
