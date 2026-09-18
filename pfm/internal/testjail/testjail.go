// Package testjail holds the platform setup the suite needs before any test
// runs. It is imported only by _test.go files, so it never reaches the binary.
package testjail

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"hostops/pfm/internal/config"
	"hostops/pfm/internal/deps"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
)

// Run points TMPDIR at a base that is both SHORT and CANONICAL, then runs the
// package's tests. Every t.TempDir() in the package inherits it, which is why
// this is one call per package instead of an edit at hundreds of call sites.
//
// Both properties are load-bearing, and macOS violates both by default:
//
//   - SHORT, because a unix socket path is capped at 104 bytes on macOS (108 on
//     Linux) — and the cap is on the WHOLE path. The default macOS TMPDIR is
//     already ~50 characters of /var/folders/<hash>/T, and t.TempDir() appends
//     the test's own name, so a jail that binds a socket named after a long
//     test fails with "bind: invalid argument" — an error that reads like a
//     bug in the code under test rather than a path that ran out of room.
//
//   - CANONICAL, because /var is a symlink to /private/var on macOS, so every
//     default temp path resolves to something other than itself. Paths used as
//     stable filesystem identities must have one spelling: allowing one
//     location to appear under both its symlinked and resolved paths splits
//     state that belongs together. A normal checkout satisfies that invariant;
//     only the default temp dir cannot.
//
// /tmp is the answer to both: it is short everywhere, and resolving it once
// yields /private/tmp on macOS and /tmp on Linux — canonical on each.
func Run(m *testing.M) int {
	// Installer tests must not inherit an operator account as an MCP write
	// target. Packages that can install host state enter through this jail.
	if err := os.Setenv("CLAUDE_CONFIG_DIR", ""); err != nil {
		fmt.Fprintf(os.Stderr, "testjail: clear CLAUDE_CONFIG_DIR: %v\n", err)
		return 1
	}
	// Git fixtures must read only repository-local configuration. A developer's
	// global identity, aliases, hooks, signing policy, or system configuration
	// must never steer a test subprocess.
	for name, value := range map[string]string{
		"GIT_CONFIG_GLOBAL":   "/dev/null",
		"GIT_CONFIG_NOSYSTEM": "1",
	} {
		if err := os.Setenv(name, value); err != nil {
			fmt.Fprintf(os.Stderr, "testjail: set %s to %s: %v\n", name, value, err)
			return 1
		}
	}
	// A `go` child (internal/update's rebuild, a `go run`) derives GOCACHE,
	// GOPATH and GOMODCACHE from HOME when they are unset, and every jail below
	// rehomes HOME — so the build cache and the module download cache would
	// land INSIDE the jail, and a child still writing at teardown makes
	// RemoveAll fail with "directory not empty" (measured: TestKillSelfResolve-
	// AndInternalCLI, 5/6 red under load). Pin all three to the real user's
	// locations here, while HOME is still the real one. A missing home or
	// cache dir is reported, never silently left to the jail.
	if code := pinGoDirs(); code != 0 {
		return code
	}
	base, err := filepath.EvalSymlinks(os.TempDir())
	if short, shortErr := filepath.EvalSymlinks("/tmp"); shortErr == nil {
		base, err = short, nil
	}
	if err != nil {
		// No canonical base to stand on. Run anyway rather than failing the
		// whole package: on a platform where the default temp dir is already
		// short and canonical, nothing here was needed in the first place.
		defer jailHome(os.TempDir())()
		return m.Run()
	}
	// No wrapper directory of our own: t.TempDir() already makes a unique path
	// per test and removes it. An extra layer would only spend a dozen of the
	// 104 bytes a socket path is allowed, which is exactly the budget the
	// longest test names need.
	if err := os.Setenv("TMPDIR", base); err != nil {
		fmt.Fprintf(os.Stderr, "testjail: set TMPDIR to %s: %v\n", base, err)
		return 1
	}
	defer jailHome(base)()
	return m.Run()
}

// jailHome points PFM_HOME at a private directory for the WHOLE package, so a
// test that never builds a jail of its own still cannot reach the operator's
// real home — the fleet.db their live chats are indexed in, the
// ~/.claude/projects their transcripts live in.
//
// It deliberately does not touch HOME. Packages here shell out to `go build`,
// and the module cache and build cache live under the real HOME; moving it
// would trade a data-safety bug for a toolchain one. PFM_HOME is the variable
// paths.Resolve() reads, and Resolve() refuses an unset one under test — so a
// package that opts out of this helper fails loudly rather than escaping.
//
// BROKEN STATE: if the jail directory cannot be created this says so on stderr
// and leaves PFM_HOME unset, which makes paths.Resolve() refuse. An unjailed
// package fails its tests; it never silently writes to a live account.
func jailHome(base string) func() {
	home, err := os.MkdirTemp(base, "pfm-jail-home-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "testjail: no jailed home under %s: %v\n", base, err)
		return func() {}
	}
	if err := os.Setenv(paths.EnvHome, home); err != nil {
		fmt.Fprintf(os.Stderr, "testjail: set %s to %s: %v\n", paths.EnvHome, home, err)
		if removeErr := os.RemoveAll(home); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "testjail: remove unused jail home %s: %v\n", home, removeErr)
		}
		return func() {}
	}
	return func() {
		if err := os.RemoveAll(home); err != nil && !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(os.Stderr, "testjail: remove jail home %s: %v\n", home, err)
		}
	}
}

// ShortRoot returns a unique temporary directory whose path is as short as this
// platform allows, for jails that bind a unix socket underneath it.
//
// t.TempDir() embeds the TEST'S OWN NAME, and a descriptive Go test name is
// easily sixty characters. Under macOS's 104-byte sun_path cap that leaves no
// room for tmux's "tmux-<uid>/" convention plus a chat socket name, and the
// bind fails with "invalid argument" — which reads as a bug in the code under
// test rather than a path that ran out of room. Callers that only need a
// scratch directory should keep using t.TempDir(); this is for the ones whose
// children include a socket.
func ShortRoot(t *testing.T) string {
	t.Helper()
	directory, err := CreateShortRoot()
	if err != nil {
		t.Fatalf("create short jail root: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("remove short jail root %s: %v", directory, err)
		}
	})
	return directory
}

// CreateShortRoot returns an unregistered short root for harnesses that do not
// have a *testing.T. The caller owns cleanup.
func CreateShortRoot() (string, error) {
	base := os.TempDir()
	if short, err := filepath.EvalSymlinks("/tmp"); err == nil {
		base = short
	}
	return os.MkdirTemp(base, "j")
}

// Fleet builds a scratch fleet under a ShortRoot and points every pfm path at
// it — TMUX_TMPDIR, PFM_DB, PFM_SID_DIR, both engine roots, the tmux dir, the
// process table — and returns the root; a caller layers install artifacts on
// top. A chat server's tmux config is /dev/null: in real life it loads the
// user's ~/.tmux.conf, and a fixture must not let the machine it runs on
// steer the test.
//
// PFM_HOME and HOME are set together. HOME is the same concept under its
// other name: pinning only PFM_HOME leaves anything reading the plain
// variable — the test itself, a subprocess, a library — writing into the
// operator's real account, which is how fixture transcripts reached a live
// ~/.claude/projects. The two must never be allowed to disagree.
func Fleet(t *testing.T) string {
	t.Helper()
	return fleetSetenv(t, t.Setenv)
}

// FleetEnv builds the same scratch fleet as Fleet without changing the test
// process environment. It returns an environment suitable for exec.Cmd.Env, so
// a test whose jail is confined to subprocesses may run in parallel.
func FleetEnv(t *testing.T) (string, []string) {
	t.Helper()
	environment := append([]string(nil), os.Environ()...)
	root := fleetSetenv(t, func(name, value string) {
		environment = append(environment, name+"="+value)
	})
	return root, environment
}

func fleetSetenv(t *testing.T, setenv func(string, string)) string {
	t.Helper()
	root := ShortRoot(t)
	// The engine roots are named for their engines, spelled by the registry.
	claudeRoot := pfmengine.MustLookup(pfmengine.Claude).LongName
	codexHome := pfmengine.MustLookup(pfmengine.Codex).LongName
	for _, directory := range []string{"t", "sid", claudeRoot, codexHome, "tmux", "home", "proc"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	setenv("TMUX_TMPDIR", filepath.Join(root, "t"))
	// The index DB, under the name it is migrating to (design § Glossary).
	setenv(paths.EnvDB, filepath.Join(root, "index.db"))
	setenv(paths.EnvSIDDir, filepath.Join(root, "sid"))
	setenv(paths.EnvClaudeRoots, filepath.Join(root, claudeRoot))
	setenv(paths.EnvCodexHome, filepath.Join(root, codexHome))
	setenv(paths.EnvTmuxDir, filepath.Join(root, "tmux"))
	setenv(paths.EnvHome, filepath.Join(root, "home"))
	setenv("HOME", filepath.Join(root, "home"))
	setenv(paths.EnvProcRoot, filepath.Join(root, "proc"))
	setenv(paths.EnvTmuxConf, "/dev/null")
	return root
}

// InstalledHome builds the host artifacts a healthy pfm install carries on
// top of a scratch fleet and returns the fleet root.
func InstalledHome(t *testing.T) string {
	t.Helper()

	root := Fleet(t)
	jailedHome := filepath.Join(root, "home")
	claudeBinary := pfmengine.MustLookup(pfmengine.Claude).Binary
	if err := os.MkdirAll(filepath.Join(jailedHome, ".local", "bin"), 0o700); err != nil {
		t.Fatal(err)
	}
	canonical := filepath.Join(jailedHome, ".local", "bin", "pfm")
	if err := os.WriteFile(canonical, []byte("jailed-pfm"), 0o700); err != nil {
		t.Fatal(err)
	}
	managedClaude := filepath.Join(jailedHome, ".local", "share", "pfm", "install", "bin", claudeBinary)
	if err := os.MkdirAll(filepath.Dir(managedClaude), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedClaude, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(managedClaude, filepath.Join(jailedHome, ".local", "bin", claudeBinary)); err != nil {
		t.Fatal(err)
	}
	// The pfm-statusline and tmux-title-renudge host overlays are contracted
	// pfm-install artifacts (issue #14 F1) the same way the Claude launcher
	// is — a jail meant to represent a healthy install carries both, same
	// managed-copy-then-symlink shape.
	for _, overlay := range []string{"pfm-statusline", "tmux-title-renudge"} {
		managedOverlay := filepath.Join(jailedHome, ".local", "share", "pfm", "install", "bin", overlay)
		if err := os.MkdirAll(filepath.Dir(managedOverlay), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(managedOverlay, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(managedOverlay, filepath.Join(jailedHome, ".local", "bin", overlay)); err != nil {
			t.Fatal(err)
		}
	}
	testPath := []string{filepath.Dir(canonical)}
	for _, directory := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(directory, "pfm")); os.IsNotExist(err) {
			testPath = append(testPath, directory)
		}
	}
	t.Setenv("PATH", strings.Join(testPath, string(os.PathListSeparator)))
	return root
}

// StageHarnessPromptBaseline writes one managed harness-prompt baseline pin.
func StageHarnessPromptBaseline(t *testing.T, home, alias, stem, captured, name string) {
	t.Helper()
	sum := sha256.Sum256([]byte(captured))
	pin := hex.EncodeToString(sum[:]) + "  " + name + "\n"
	dir := filepath.Join(home, ".local", "share", "pfm", "install", "prompts")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for filename, data := range map[string]string{
		stem + ".sha256": pin,
		name:             captured,
		stem + ".model":  "claude-" + alias + "-5\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, filename), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// CleanHome stages a healthy target HOME and returns the runtime a clean
// diagnostic reads.
func CleanHome(t *testing.T) config.Runtime {
	t.Helper()
	home := t.TempDir()
	claudeBinary := pfmengine.MustLookup(pfmengine.Claude).Binary
	canonicalDir := filepath.Join(home, ".local", "bin")
	hostShimDir := filepath.Join(t.TempDir(), "bin")
	for _, directory := range []string{
		canonicalDir,
		hostShimDir,
		filepath.Join(home, ".cc", "1", "projects"),
		filepath.Join(home, ".cc", "2", "projects"),
		filepath.Join(home, ".codex"),
		filepath.Join(home, ".local", "state", "pfm"),
		filepath.Join(home, "proc"),
		filepath.Join(home, "tmux"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	canonical := filepath.Join(canonicalDir, "pfm")
	if err := os.WriteFile(canonical, []byte("target-pfm"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hostShimDir, "pfm"), []byte("host-pfm"), 0o700); err != nil {
		t.Fatal(err)
	}
	managedClaude := filepath.Join(home, ".local", "share", "pfm", "install", "bin", claudeBinary)
	if err := os.MkdirAll(filepath.Dir(managedClaude), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managedClaude, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(managedClaude, filepath.Join(canonicalDir, claudeBinary)); err != nil {
		t.Fatal(err)
	}
	// The pfm-statusline and tmux-title-renudge host overlays are contracted
	// pfm-install artifacts (issue #14 F1); a fixture representing a healthy
	// target HOME carries both, same managed-copy-then-symlink shape as the
	// Claude launcher above.
	for _, overlay := range []string{"pfm-statusline", "tmux-title-renudge"} {
		managedOverlay := filepath.Join(home, ".local", "share", "pfm", "install", "bin", overlay)
		if err := os.MkdirAll(filepath.Dir(managedOverlay), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(managedOverlay, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(managedOverlay, filepath.Join(canonicalDir, overlay)); err != nil {
			t.Fatal(err)
		}
	}
	const (
		captured = "pfm jail fixture harness prompt\n"
		name     = "harness-prompt-fixture.md"
	)
	for _, model := range []struct{ alias, stem string }{{"sonnet", "harness-original"}, {"opus", "harness-opus"}} {
		StageHarnessPromptBaseline(t, home, model.alias, model.stem, captured, name)
	}

	t.Setenv("HOME", home)
	t.Setenv(paths.EnvHome, home)
	t.Setenv(paths.EnvDB, filepath.Join(home, ".local", "state", "pfm", "fleet.db"))
	t.Setenv(paths.EnvFleetDB, filepath.Join(home, ".cc", "fleet.db"))
	t.Setenv(paths.EnvSIDDir, filepath.Join(home, "sid"))
	t.Setenv(paths.EnvClaudeRoots, filepath.Join(home, ".cc", "1", "projects")+
		string(os.PathListSeparator)+filepath.Join(home, ".cc", "2", "projects"))
	t.Setenv(paths.EnvCodexHome, filepath.Join(home, ".codex"))
	t.Setenv(paths.EnvTmuxDir, filepath.Join(home, "tmux"))
	t.Setenv(paths.EnvTmuxConf, "/dev/null")
	t.Setenv(paths.EnvProcRoot, filepath.Join(home, "proc"))
	t.Setenv("PATH", canonicalDir+string(os.PathListSeparator)+hostShimDir)

	loadedRuntime, err := config.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	return loadedRuntime
}

// PTYCommand builds a command that runs argv on a REAL pty via script(1), for
// behaviour that only happens on a terminal.
//
// The two script(1) implementations disagree about their own argv. util-linux
// takes the command through -c and the typescript file LAST; BSD (macOS) takes
// the typescript file FIRST and then the command and its arguments directly,
// with no -c and no -f. Handing Linux's form to the BSD binary prints a usage
// message and exits — and a test that then writes to its stdin fails with
// "broken pipe", which reads as the code under test closing the pipe rather
// than as the harness being wrong about the platform.
func PTYCommand(argv ...string) *exec.Cmd {
	scriptBinary, err := deps.Resolve("script")
	if err != nil {
		scriptBinary = "script"
	}
	if runtime.GOOS == "linux" {
		quoted := make([]string, len(argv))
		for index, argument := range argv {
			quoted[index] = "'" + strings.ReplaceAll(argument, "'", `'\''`) + "'"
		}
		return exec.Command(scriptBinary, "-qefc", strings.Join(quoted, " "), "/dev/null")
	}
	return exec.Command(scriptBinary, append([]string{"-qe", "/dev/null"}, argv...)...)
}

// pinGoDirs sets GOCACHE, GOPATH and GOMODCACHE — each only when unset — to the
// defaults Go itself would derive from the REAL home, so a `go` child spawned
// under a jailed HOME never writes into the jail. Returns a non-zero exit code
// when a value cannot be set; a missing user cache/home dir is reported and the
// variable left as it was.
func pinGoDirs() int {
	set := func(name, value string) int {
		if err := os.Setenv(name, value); err != nil {
			fmt.Fprintf(os.Stderr, "testjail: set %s: %v\n", name, err)
			return 1
		}
		return 0
	}
	if os.Getenv("GOCACHE") == "" {
		if cache, err := os.UserCacheDir(); err != nil {
			fmt.Fprintf(os.Stderr, "testjail: GOCACHE left unpinned — user cache dir: %v\n", err)
		} else if code := set("GOCACHE", filepath.Join(cache, "go-build")); code != 0 {
			return code
		}
	}
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "testjail: GOPATH/GOMODCACHE left unpinned — user home dir: %v\n", err)
			return 0
		}
		gopath = filepath.Join(home, "go")
		if code := set("GOPATH", gopath); code != 0 {
			return code
		}
	}
	if os.Getenv("GOMODCACHE") == "" {
		return set("GOMODCACHE", filepath.Join(filepath.SplitList(gopath)[0], "pkg", "mod"))
	}
	return 0
}
