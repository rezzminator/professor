package deps

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// resetResolveCache clears the memoized resolution table before and after a
// test so PATH fixtures from other tests in this package (or a previous run
// of this one) cannot leak a cached path across t.Setenv("PATH", ...) calls.
func resetResolveCache(t *testing.T) {
	t.Helper()
	resolveCacheMu.Lock()
	previous := resolveCache
	resolveCache = map[string]string{}
	resolveCacheMu.Unlock()
	t.Cleanup(func() {
		resolveCacheMu.Lock()
		resolveCache = previous
		resolveCacheMu.Unlock()
	})
}

func writeExecutable(t *testing.T, directory, name string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fixture executable %s: %v", name, err)
	}
	return path
}

// TestResolveMemoizesASuccessfulLookupPerName pins the cache population this
// change adds: a resolved path is remembered under (name, $PATH) so a second
// call needs neither a fresh Registry() build nor a fresh PATH walk. Every
// tmux capture-pane and every spawned `codex app-server` crosses
// deps.Executable, and paying for both on EVERY exec is what a parked
// Limits/Codex-idle poll was doing every 2s (2026-09-08 measurement,
// devbox).
func TestResolveMemoizesASuccessfulLookupPerName(t *testing.T) {
	resetResolveCache(t)
	directory := t.TempDir()
	want := writeExecutable(t, directory, "probe-fixture")
	t.Setenv("PATH", directory)

	got, err := Resolve("probe-fixture")
	if err != nil || got != want {
		t.Fatalf("Resolve() = %q, %v; want %q, nil", got, err, want)
	}

	resolveCacheMu.Lock()
	cached, ok := resolveCache[resolveCacheKey("probe-fixture")]
	resolveCacheMu.Unlock()
	if !ok || cached != want {
		t.Fatalf("resolveCache after Resolve() = %q, %v; want %q cached", cached, ok, want)
	}

	// A second call under the identical PATH must return the exact same
	// path without needing the fixture to still be the only thing on PATH
	// — it hits the memoized entry via the cache, not a fresh walk.
	got, err = Resolve("probe-fixture")
	if err != nil || got != want {
		t.Fatalf("second Resolve() = %q, %v; want %q, nil", got, err, want)
	}

	if got := Executable("probe-fixture"); got != want {
		t.Fatalf("Executable() = %q, want %q", got, want)
	}
}

// TestResolveCacheInvalidatesWhenTheBinaryDisappears pins the safety half of
// the memoization: a cached path must never survive its target vanishing —
// the fixture in the parked-poll comment (2026-09-08) is a Codex binary
// disappearing mid-session, and a stale hit there would silently misreport
// dependency health.
func TestResolveCacheInvalidatesWhenTheBinaryDisappears(t *testing.T) {
	resetResolveCache(t)
	directory := t.TempDir()
	path := writeExecutable(t, directory, "probe-vanishing")
	t.Setenv("PATH", directory)

	if got, err := Resolve("probe-vanishing"); err != nil || got != path {
		t.Fatalf("Resolve() = %q, %v; want %q, nil", got, err, path)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove fixture executable: %v", err)
	}

	if _, err := Resolve("probe-vanishing"); err == nil {
		t.Fatalf("Resolve() after the binary disappeared = nil error, want a lookup failure")
	}

	resolveCacheMu.Lock()
	_, stillCached := resolveCache[resolveCacheKey("probe-vanishing")]
	resolveCacheMu.Unlock()
	if stillCached {
		t.Fatalf("resolveCache still holds an entry for a binary that no longer exists")
	}
}

// TestResolveCacheIsScopedByPATH proves the cache key includes $PATH, not
// just the binary name: a name that resolves to a different file once PATH
// changes must never return the earlier PATH's answer.
func TestResolveCacheIsScopedByPATH(t *testing.T) {
	resetResolveCache(t)
	first := t.TempDir()
	second := t.TempDir()
	wantFirst := writeExecutable(t, first, "probe-scoped")
	wantSecond := writeExecutable(t, second, "probe-scoped")

	t.Setenv("PATH", first)
	if got, err := Resolve("probe-scoped"); err != nil || got != wantFirst {
		t.Fatalf("Resolve() under first PATH = %q, %v; want %q, nil", got, err, wantFirst)
	}

	t.Setenv("PATH", second)
	if got, err := Resolve("probe-scoped"); err != nil || got != wantSecond {
		t.Fatalf("Resolve() under second PATH = %q, %v; want %q, nil", got, err, wantSecond)
	}
}

// TestResolveUsesTheRegistryCommandForAStableName pins the distinction
// between a registry row's stable Name and the command it must execute. A
// configured absolute command must win over a same-named PATH entry; this is
// the security boundary for fixed commands such as macOS's keychain helper.
func TestResolveUsesTheRegistryCommandForAStableName(t *testing.T) {
	resetResolveCache(t)

	configuredDir := t.TempDir()
	configured := writeExecutable(t, configuredDir, "configured-security")
	shadowDir := t.TempDir()
	shadow := writeExecutable(t, shadowDir, "security-alias")
	t.Setenv("PATH", shadowDir)

	const stableName = "security-alias"
	previous := fixedCommands
	fixedCommands = append(append([]Entry(nil), fixedCommands...), Entry{
		Name:      stableName,
		Command:   configured,
		Platforms: []string{runtime.GOOS},
	})
	t.Cleanup(func() { fixedCommands = previous })

	got, err := Resolve(stableName)
	if err != nil {
		t.Fatalf("Resolve(%q): %v", stableName, err)
	}
	if got != configured {
		t.Fatalf("Resolve(%q) = %q, want configured command %q (PATH shadow %q)", stableName, got, configured, shadow)
	}
}

// TestRumdlRegistryEntryIsOptionalPinnedAndPlatformGated pins the shape of
// the rumdl dependency entry: pfm install provisions rumdl as a soft
// dependency, so a missing or outdated rumdl must never fail `pfm doctor`,
// yet the pinned MinVersion must still match what the shipped .rumdl.toml
// policy was validated against.
func TestRumdlRegistryEntryIsOptionalPinnedAndPlatformGated(t *testing.T) {
	entries := Registry(Options{Home: t.TempDir(), GOOS: "linux", GOARCH: "amd64"})
	var entry *Entry
	for index, candidate := range entries {
		if candidate.Name == "rumdl" {
			entry = &entries[index]
		}
	}
	if entry == nil {
		t.Fatal("registry lost the rumdl entry")
	}
	if entry.Required {
		t.Error("rumdl must stay non-Required — it is a provisioned soft dependency, not a hard one")
	}
	for _, platform := range []string{"linux", "darwin"} {
		if !entry.AppliesTo(platform) {
			t.Errorf("rumdl must apply to %s", platform)
		}
	}
	if entry.AppliesTo("windows") {
		t.Error("rumdl must not apply to an ungated platform")
	}
	if entry.MinVersion != "0.2.73" {
		t.Errorf(
			"rumdl MinVersion = %q, want %q (the version the shipped .rumdl.toml policy was validated against)",
			entry.MinVersion,
			"0.2.73",
		)
	}
	if strings.TrimSpace(entry.InstallHint) == "" {
		t.Error("rumdl must carry a non-empty InstallHint")
	}
	if !Registered("rumdl") {
		t.Error(`Registered("rumdl") = false, want true`)
	}
}

// TestPrefixedVersionParsesRumdlVersionOutput pins the exact parse the
// doctor row depends on: prefixedVersion("rumdl") must turn the real host
// output of `rumdl --version` into a bare dotted version, never silently
// yielding "" for a healthy install.
func TestPrefixedVersionParsesRumdlVersionOutput(t *testing.T) {
	parse := prefixedVersion("rumdl")
	got, err := parse("rumdl 0.2.73\n")
	if err != nil {
		t.Fatalf("prefixedVersion(\"rumdl\")(%q): %v", "rumdl 0.2.73\n", err)
	}
	if got != "0.2.73" {
		t.Fatalf("prefixedVersion(\"rumdl\")(%q) = %q, want %q", "rumdl 0.2.73\n", got, "0.2.73")
	}
}

// TestAtLeastComparesNumericFieldsIncludingPreReleaseSuffixes pins the one
// version comparison in pfm — the same answer the doctor row's MinVersion gate
// and the installer's already-present gate both need. The last two cases are
// the reason there is only one: a dotted split plus Atoi reads "0.2.73-beta"
// as a 0 third field and reports it BELOW 0.2.73, so a host running a
// pre-release would be reinstalled on every install while doctor called it
// satisfied.
func TestAtLeastComparesNumericFieldsIncludingPreReleaseSuffixes(t *testing.T) {
	for _, test := range []struct {
		version, minimum string
		want             bool
	}{
		{version: "0.2.73", minimum: "0.2.73", want: true},
		{version: "0.2.74", minimum: "0.2.73", want: true},
		{version: "0.2.72", minimum: "0.2.73", want: false},
		{version: "0.3.0", minimum: "0.2.73", want: true},
		{version: "1.0", minimum: "1.0.0", want: true},
		{version: "0.9.99", minimum: "0.10.0", want: false},
		{version: "v0.2.73", minimum: "0.2.73", want: true},
		{version: "0.2.73-beta", minimum: "0.2.73", want: true},
		{version: "0.2.74-rc1", minimum: "0.2.73", want: true},
	} {
		if got := AtLeast(test.version, test.minimum); got != test.want {
			t.Errorf("AtLeast(%q, %q) = %v, want %v", test.version, test.minimum, got, test.want)
		}
	}
}
