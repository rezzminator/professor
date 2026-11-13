package deps

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoginShellCommandsAreOptionalAndPlatformGated(t *testing.T) {
	for _, tc := range []struct{ command, platform string }{
		{"dscl", platformDarwin},
		{"getent", platformLinux},
	} {
		t.Run(tc.command, func(t *testing.T) {
			for _, entry := range Registry(Options{Home: t.TempDir()}) {
				if entry.Name != tc.command {
					continue
				}
				if entry.Required || len(entry.Platforms) != 1 || entry.Platforms[0] != tc.platform ||
					len(entry.VersionArgs) != 0 {
					t.Fatalf("login-shell dependency must be optional and %s-only: %+v", tc.platform, entry)
				}
				if !entry.AppliesTo(tc.platform) || entry.AppliesTo("windows") || !Registered(tc.command) {
					t.Fatalf("login-shell dependency has invalid availability: %+v", entry)
				}
				return
			}
			t.Fatalf("login-shell dependency %s is not registered", tc.command)
		})
	}
}

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
	if err := writeExecutableUnderForkLock(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fixture executable %s: %v", name, err)
	}
	return path
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

// TestPython3RegistryEntryIsRequiredAndPlatformGated pins the python3 row: the
// flights:tracer's tracetree kit runs on it, so a missing python3
// fails the install's pre-change check and the doctor dependency preflight like
// git or tmux, and the row's version parse reads the interpreter's own banner.
func TestPython3RegistryEntryIsRequiredAndPlatformGated(t *testing.T) {
	entries := Registry(Options{Home: t.TempDir(), GOOS: "linux", GOARCH: "amd64"})
	var entry *Entry
	for index, candidate := range entries {
		if candidate.Name == "python3" {
			entry = &entries[index]
		}
	}
	if entry == nil {
		t.Fatal("registry has no python3 entry")
	}
	if !entry.Required {
		t.Errorf("python3 Required=%v, want a required engine dependency", entry.Required)
	}
	for _, platform := range []string{"linux", "darwin"} {
		if !entry.AppliesTo(platform) {
			t.Errorf("python3 must apply to %s", platform)
		}
	}
	if entry.AppliesTo("windows") {
		t.Error("python3 must not apply to an ungated platform")
	}
	if entry.Command != "python3" {
		t.Errorf("python3 Command = %q, want the bare name resolved on PATH", entry.Command)
	}
	for _, word := range []string{"python3", "tracetree"} {
		if !strings.Contains(entry.InstallHint, word) {
			t.Errorf("python3 InstallHint = %q, want it to say %q", entry.InstallHint, word)
		}
	}
	if !Registered("python3") {
		t.Error(`Registered("python3") = false, want true`)
	}
	got, err := entry.Parse("Python 3.12.1\n")
	if err != nil || got != "3.12.1" {
		t.Errorf("python3 Parse(%q) = %q, %v; want 3.12.1", "Python 3.12.1\n", got, err)
	}
}

func TestGoRegistryInstallHintNamesTheModuleToolchainFloor(t *testing.T) {
	entries := Registry(Options{Home: t.TempDir(), GOOS: "linux", GOARCH: "amd64"})
	for _, entry := range entries {
		if entry.Name != "go" {
			continue
		}
		const want = "install Go 1.27.1 or newer to use pfm update"
		if entry.InstallHint != want {
			t.Errorf("go InstallHint = %q, want %q", entry.InstallHint, want)
		}
		return
	}
	t.Fatal("registry has no go entry")
}
