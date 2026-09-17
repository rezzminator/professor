// Package testjail holds the platform setup the suite needs before any test
// runs. It is imported only by _test.go files, so it never reaches the binary.
package testjail

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
	base := os.TempDir()
	if short, err := filepath.EvalSymlinks("/tmp"); err == nil {
		base = short
	}
	directory, err := os.MkdirTemp(base, "j")
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
	codexRoot := pfmengine.MustLookup(pfmengine.Codex).LongName
	for _, directory := range []string{"t", "sid", claudeRoot, codexRoot, "tmux", "home", "proc"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	setenv("TMUX_TMPDIR", filepath.Join(root, "t"))
	// The index DB, under the name it is migrating to (design § Glossary).
	setenv(paths.EnvDB, filepath.Join(root, "index.db"))
	setenv(paths.EnvSIDDir, filepath.Join(root, "sid"))
	setenv(paths.EnvClaudeRoots, filepath.Join(root, claudeRoot))
	setenv(paths.EnvCodexRoot, filepath.Join(root, codexRoot))
	setenv(paths.EnvTmuxDir, filepath.Join(root, "tmux"))
	setenv(paths.EnvHome, filepath.Join(root, "home"))
	setenv("HOME", filepath.Join(root, "home"))
	setenv(paths.EnvProcRoot, filepath.Join(root, "proc"))
	setenv(paths.EnvTmuxConf, "/dev/null")
	return root
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
