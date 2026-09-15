package deps

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDetachLauncherPrefersSetsidWhenItResolves pins the fork-and-return
// branch: when setsid resolves, DetachLauncher must hand back its own path,
// the "-f" flag it needs, and forked=true so a caller knows it may Run() and
// let setsid do the detaching.
func TestDetachLauncherPrefersSetsidWhenItResolves(t *testing.T) {
	root := t.TempDir()
	setsidPath := writeDetachTestScript(t, filepath.Join(root, "setsid"))
	nohupPath := writeDetachTestScript(t, filepath.Join(root, "nohup"))
	path, prefixArgs, forked, err := DetachLauncher(setsidPath, nohupPath)
	if err != nil {
		t.Fatalf("DetachLauncher returned an error with both binaries present: %v", err)
	}
	if path != setsidPath {
		t.Errorf("path = %q, want setsid's path %q", path, setsidPath)
	}
	if len(prefixArgs) != 1 || prefixArgs[0] != "-f" {
		t.Errorf("prefixArgs = %q, want [\"-f\"]", prefixArgs)
	}
	if !forked {
		t.Error("forked = false, want true — setsid -f forks and returns immediately")
	}
}

// TestDetachLauncherFallsBackToNohupWhenSetsidIsAbsent pins the branch this
// fix exists for: every macOS host, where setsid never resolves. nohup must
// come back with no "-f" (it accepts no such flag) and forked=false, telling
// the caller it IS the detached helper rather than a forking launcher.
func TestDetachLauncherFallsBackToNohupWhenSetsidIsAbsent(t *testing.T) {
	root := t.TempDir()
	missingSetsid := filepath.Join(root, "no-such-setsid")
	nohupPath := writeDetachTestScript(t, filepath.Join(root, "nohup"))
	path, prefixArgs, forked, err := DetachLauncher(missingSetsid, nohupPath)
	if err != nil {
		t.Fatalf("DetachLauncher returned an error though nohup resolves: %v", err)
	}
	if path != nohupPath {
		t.Errorf("path = %q, want nohup's path %q", path, nohupPath)
	}
	if len(prefixArgs) != 0 {
		t.Errorf("prefixArgs = %q, want empty — nohup takes no -f flag", prefixArgs)
	}
	if forked {
		t.Error("forked = true, want false — nohup IS the detached helper, it does not fork and return")
	}
}

// TestDetachLauncherErrorsNamingBothFailuresWhenNeitherResolves pins that a
// caller told "no launcher" can see WHY on both counts, not just that one of
// two things failed.
func TestDetachLauncherErrorsNamingBothFailuresWhenNeitherResolves(t *testing.T) {
	root := t.TempDir()
	missingSetsid := filepath.Join(root, "missing-setsid-marker")
	missingNohup := filepath.Join(root, "missing-nohup-marker")
	_, _, _, err := DetachLauncher(missingSetsid, missingNohup)
	if err == nil {
		t.Fatal("DetachLauncher returned no error though neither binary resolves")
	}
	if !strings.Contains(err.Error(), "missing-setsid-marker") {
		t.Errorf("error %q does not name the setsid failure", err)
	}
	if !strings.Contains(err.Error(), "missing-nohup-marker") {
		t.Errorf("error %q does not name the nohup failure", err)
	}
}

// TestDetachLauncherBareNameLookupTakesTheBranchTheRegistryAllows exercises
// the OTHER path into DetachLauncher: an empty override, forcing the real
// exec.LookPath PATH search the two fixed-path tests above never touch. A
// real, executable "setsid" is staged directly on $PATH — proving this is
// not a "the binary doesn't exist" story. On linux setsid is registered for
// this platform (internal/deps/registry.go's fixedCommands entry carries
// Platforms: []string{"linux"}), so Resolve finds it and DetachLauncher
// forks through it. On darwin that same entry's AppliesTo(runtime.GOOS)
// is false, so Resolve refuses it before ever calling exec.LookPath — the
// staged binary is irrelevant, and DetachLauncher falls to nohup. This is
// the exact mechanism the fix rides on every macOS host.
func TestDetachLauncherBareNameLookupTakesTheBranchTheRegistryAllows(t *testing.T) {
	var wantForked bool
	switch runtime.GOOS {
	case "linux":
		wantForked = true
	case "darwin":
		wantForked = false
	default:
		t.Skip("registry platform contract is Linux/Darwin only")
	}
	directory := t.TempDir()
	fakeSetsid := writeDetachTestScript(t, filepath.Join(directory, "setsid"))
	fakeNohup := writeDetachTestScript(t, filepath.Join(directory, "nohup"))
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))

	path, prefixArgs, forked, err := DetachLauncher("", "")
	if err != nil {
		t.Fatalf("DetachLauncher returned an error: %v", err)
	}
	if forked != wantForked {
		t.Fatalf("forked = %v, want %v on %s", forked, wantForked, runtime.GOOS)
	}
	if wantForked {
		if path != fakeSetsid || len(prefixArgs) != 1 || prefixArgs[0] != "-f" {
			t.Fatalf("path=%q prefixArgs=%q, want the staged setsid branch (%q)", path, prefixArgs, fakeSetsid)
		}
		return
	}
	if path != fakeNohup || len(prefixArgs) != 0 {
		t.Fatalf(
			"path=%q prefixArgs=%q, want the staged nohup branch (%q) — a real setsid on $PATH must not win on darwin",
			path,
			prefixArgs,
			fakeNohup,
		)
	}
}

// TestDetachLauncherOnDarwinIsRefusedByThePlatformGateNotAbsence pins WHICH
// of two legitimate mechanisms decides the darwin branch above: absence (no
// setsid binary reachable) or the registry's platform gate (setsid is
// registered Platforms: []string{"linux"} only, so Resolve refuses it on
// darwin before ever asking exec.LookPath). A real, executable "setsid" is
// staged on $PATH here — if absence were the mechanism, Resolve would
// succeed and return its path; instead it must refuse by platform policy,
// carrying "not supported on darwin" rather than a not-found error. This is
// the fact the nohup fallback in DetachLauncher (and the fix in
// internal/kill/spawn.go) actually depends on.
func TestDetachLauncherOnDarwinIsRefusedByThePlatformGateNotAbsence(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("pins the darwin-only platform gate that decides DetachLauncher's fallback")
	}
	directory := t.TempDir()
	writeDetachTestScript(t, filepath.Join(directory, "setsid"))
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := Resolve("setsid")
	if err == nil {
		t.Fatal(
			"Resolve(\"setsid\") succeeded on darwin though setsid is registered Linux-only — the staged binary should never be reached",
		)
	}
	if !strings.Contains(err.Error(), "not supported on darwin") {
		t.Fatalf("Resolve(\"setsid\") error = %q, want the platform-gate refusal, not an absence error", err)
	}
	if strings.Contains(err.Error(), "executable file not found") {
		t.Fatalf(
			"Resolve(\"setsid\") error = %q reads as absence, not the platform gate — the staged binary was on $PATH the whole time",
			err,
		)
	}
}

func writeDetachTestScript(t *testing.T, path string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
