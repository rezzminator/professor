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

	harnessprompts "github.com/rezzminator/professor/pfm/harness-prompts"
	"github.com/rezzminator/professor/pfm/internal/codexgen"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// KeepAmbientIdentity, set by a TestMain before Run, leaves the ambient
// identity Run otherwise scrubs in place. cmd/pfm's re-exec'd attach-helper
// process sets it: that process stands in for the real pfm binary running
// inside a LIVE tmux pane on purpose — a real tmux server sets its
// TMUX/TMUX_PANE for it exactly as it would for the shipped binary, which is
// never jailed at all. Scrubbing them would defeat the one fixture built to
// prove that real-tmux behavior.
var KeepAmbientIdentity bool

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
		warnSetup("clear CLAUDE_CONFIG_DIR: %v", err)
		return 1
	}
	// The fence has none of these — no tmux pane, no chat socket, no host
	// Claude/Codex session — and a jail must not inherit a live seat from the
	// executor's own shell. A test that needs one sets it with t.Setenv.
	// Literal names: testjail cannot import internal/resolve (import cycle).
	//
	// Exception: KeepAmbientIdentity (see its declaration).
	if !KeepAmbientIdentity {
		for _, name := range []string{
			"TMUX",
			"TMUX_PANE",
			"CHAT_INJECT_SOCKET",
			"CLAUDE_CODE_SESSION_ID",
			"CODEX_THREAD_ID",
		} {
			if err := os.Setenv(name, ""); err != nil {
				warnSetup("clear %s: %v", name, err)
				return 1
			}
		}
	}
	// Git fixtures must read only repository-local configuration. A developer's
	// global identity, aliases, hooks, signing policy, or system configuration
	// must never steer a test subprocess.
	for name, value := range map[string]string{
		"GIT_CONFIG_GLOBAL":   "/dev/null",
		"GIT_CONFIG_NOSYSTEM": "1",
	} {
		if err := os.Setenv(name, value); err != nil {
			warnSetup("set %s to %s: %v", name, value, err)
			return 1
		}
	}
	// A `go` child (internal/update's rebuild, a `go run`) derives its cache and
	// telemetry directories from HOME/XDG_CONFIG_HOME when they are unset, and
	// every jail below rehomes both — so those directories would land INSIDE
	// the jail, and a child still writing at teardown makes
	// RemoveAll fail with "directory not empty" (measured: TestKillSelfResolve-
	// AndInternalCLI, 5/6 red under load). Pin the Go directories outside the
	// jail here, before HOME/XDG_CONFIG_HOME move. A missing home or cache dir
	// is reported, never silently left to the jail.
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
		warnSetup("set TMPDIR to %s: %v", base, err)
		return 1
	}
	defer jailHome(base)()
	return m.Run()
}

// warnSetup reports a testjail setup failure on stderr, in the "testjail:
// ..." shape every caller here uses — the one door every message in this
// file writes stderr through.
func warnSetup(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "testjail: "+format+"\n", args...)
}

// jailHome points PFM_HOME at a private directory for the WHOLE package, so a
// test that never builds a jail of its own still cannot reach the operator's
// real home — the pfm-cache.db their live chats are indexed in, the
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
		warnSetup("no jailed home under %s: %v", base, err)
		return func() {}
	}
	if err := os.Setenv(paths.EnvHome, home); err != nil {
		warnSetup("set %s to %s: %v", paths.EnvHome, home, err)
		if removeErr := os.RemoveAll(home); removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			warnSetup("remove unused jail home %s: %v", home, removeErr)
		}
		return func() {}
	}
	configPath := filepath.Join(home, "pfm.config.json")
	if err := os.WriteFile(configPath, []byte("{\"version\":2}\n"), 0o600); err != nil {
		warnSetup("seed config %s: %v", configPath, err)
	}
	if err := os.Setenv(paths.EnvConfig, configPath); err != nil {
		warnSetup("set %s: %v", paths.EnvConfig, err)
	}
	managedDir := filepath.Join(home, "managed-settings.d")
	if err := os.MkdirAll(managedDir, 0o700); err != nil {
		warnSetup("create managed settings directory: %v", err)
	} else if err := os.WriteFile(filepath.Join(managedDir, "pfm.json"), []byte("{\"cleanupPeriodDays\":36500}\n"), 0o600); err != nil {
		warnSetup("seed managed settings: %v", err)
	}
	if err := os.Setenv(paths.EnvManagedSettingsDir, managedDir); err != nil {
		warnSetup("set %s: %v", paths.EnvManagedSettingsDir, err)
	}
	if err := paths.WriteSourceRepoMarker(home, checkoutRoot()); err != nil {
		warnSetup("write source repository marker: %v", err)
	}
	// Keep child tools' XDG files inside the package jail.
	if err := os.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config")); err != nil {
		warnSetup("set XDG_CONFIG_HOME under %s: %v", home, err)
	}
	return func() {
		if err := os.RemoveAll(home); err != nil && !errors.Is(err, fs.ErrNotExist) {
			warnSetup("remove jail home %s: %v", home, err)
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
// it — TMUX_TMPDIR, PFM_CACHE_DB, PFM_SID_DIR, both engine roots, the tmux dir, the
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
	setenv(paths.EnvCacheDB, filepath.Join(root, "index.db"))
	setenv(paths.EnvSIDDir, filepath.Join(root, "sid"))
	setenv(paths.EnvClaudeRoots, filepath.Join(root, claudeRoot))
	setenv(paths.EnvCodexHome, filepath.Join(root, codexHome))
	setenv(paths.EnvTmuxDir, filepath.Join(root, "tmux"))
	setenv(paths.EnvHome, filepath.Join(root, "home"))
	configPath := filepath.Join(root, "home", "pfm.config.json")
	if err := os.WriteFile(configPath, []byte("{\"version\":2}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	setenv(paths.EnvConfig, configPath)
	managedDir := filepath.Join(root, "home", "managed-settings.d")
	if err := os.MkdirAll(managedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(managedDir, "pfm.json"),
		[]byte("{\"cleanupPeriodDays\":36500}\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	setenv(paths.EnvManagedSettingsDir, managedDir)
	if err := paths.WriteSourceRepoMarker(filepath.Join(root, "home"), checkoutRoot()); err != nil {
		t.Fatal(err)
	}
	setenv("HOME", filepath.Join(root, "home"))
	// Keep child tools' XDG files inside this fleet.
	setenv("XDG_CONFIG_HOME", filepath.Join(root, "home", ".config"))
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
	StageGlobalAgents(t, jailedHome, root)
	stageCloneZshrc(t, jailedHome)
	return root
}

func stageCloneZshrc(t *testing.T, home string) {
	t.Helper()
	shim := filepath.Join(checkoutRoot(), "pfm", "internal", "installer", "assets", "shim", "pfm.zsh")
	line := "[[ -r \"" + shim + "\" ]] && source \"" + shim + "\"\n"
	if err := os.WriteFile(filepath.Join(home, ".zshrc"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
}

func checkoutRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(file))))
}

// StageSourceRepoMarker points a jail at this checkout's tracked prompts.
func StageSourceRepoMarker(t *testing.T, home string) {
	t.Helper()
	if err := paths.WriteSourceRepoMarker(home, checkoutRoot()); err != nil {
		t.Fatal(err)
	}
}

// StageGlobalAgents wires the checkout's current role roster into a doctor jail.
func StageGlobalAgents(t *testing.T, home string, extraClaudeConfigDirs ...string) {
	t.Helper()
	claudeConfigDirs := []string{filepath.Join(home, ".cc", "1"), filepath.Join(home, ".cc", "2")}
	claudeConfigDirs = append(claudeConfigDirs, extraClaudeConfigDirs...)
	_, err := codexgen.RunGlobalAgents(codexgen.GlobalAgentsOptions{
		Home: home, SourceRepo: checkoutRoot(),
		ClaudeConfigDirs: claudeConfigDirs,
		CodexHomes:       []string{filepath.Join(home, ".codex")}, Mode: codexgen.ModeBuild,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// ShippedHarnessPrompt returns the reviewed baseline carried by this build.
func ShippedHarnessPrompt(alias string) (string, error) {
	stem := "harness-original"
	if alias == "opus" {
		stem = "harness-opus"
	}
	base := filepath.ToSlash(filepath.Join(pfmengine.MustLookup(pfmengine.Claude).LongName, "baselines"))
	pin, err := harnessprompts.ReadPart(base + "/" + stem + ".sha256")
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(pin))
	if len(fields) != 2 {
		return "", fmt.Errorf("malformed %s baseline pin", stem)
	}
	content, err := harnessprompts.ReadPart(base + "/" + fields[1])
	return string(content), err
}

// StageHarnessPromptBaseline writes one baseline into a private fixture clone.
func StageHarnessPromptBaseline(t *testing.T, home, alias, stem, captured, name string) {
	t.Helper()
	sum := sha256.Sum256([]byte(captured))
	pin := hex.EncodeToString(sum[:]) + "  " + name + "\n"
	clone := filepath.Join(home, ".test-source")
	if err := os.MkdirAll(clone, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	dir, err := paths.HarnessBaselineDir(home)
	if err != nil {
		t.Fatal(err)
	}
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
		filepath.Join(home, ".cc", "1"),
		filepath.Join(home, ".cc", "2"),
		filepath.Join(home, ".claude"),
		filepath.Join(home, ".codex"),
		filepath.Join(home, ".local", "state", "pfm"),
		filepath.Join(home, "proc"),
		filepath.Join(home, "tmux"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range []string{"projects", "file-history", "tasks", "session-env"} {
		store := filepath.Join(home, ".claude", entry)
		if err := os.MkdirAll(store, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, account := range []string{"1", "2"} {
			if err := os.Symlink(store, filepath.Join(home, ".cc", account, entry)); err != nil {
				t.Fatal(err)
			}
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
	t.Setenv("HOME", home)
	t.Setenv(paths.EnvHome, home)
	configPath := filepath.Join(home, "pfm.config.json")
	if err := os.WriteFile(configPath, []byte("{\"version\":2}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvConfig, configPath)
	StageSourceRepoMarker(t, home)
	StageGlobalAgents(t, home, filepath.Join(home, ".claude"))
	stageCloneZshrc(t, home)
	// Pinned alongside HOME/PFM_HOME (L3-F9) — see the same comment in
	// fleetSetenv.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv(paths.EnvCacheDB, filepath.Join(home, ".local", "state", "pfm", "pfm-cache.db"))
	t.Setenv(paths.EnvStateDB, filepath.Join(home, ".local", "state", "pfm", "pfm.db"))
	t.Setenv(paths.EnvSIDDir, filepath.Join(home, "sid"))
	t.Setenv(paths.EnvClaudeRoots, filepath.Join(home, ".claude", "projects"))
	managedDir := filepath.Join(home, "managed-settings.d")
	if err := os.MkdirAll(managedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(managedDir, "pfm.json"),
		[]byte("{\"cleanupPeriodDays\":36500}\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvManagedSettingsDir, managedDir)
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

// pinGoDirs sets GOCACHE, GOPATH, GOMODCACHE and TEST_TELEMETRY_DIR — each only
// when unset — outside the jail, so a `go` child spawned under a jailed
// HOME/XDG_CONFIG_HOME never writes into the jail. Returns a non-zero exit code
// when a required value cannot be chosen or set; a missing user cache/home dir
// is reported and the variable left as it was.
func pinGoDirs() int {
	set := func(name, value string) int {
		if err := os.Setenv(name, value); err != nil {
			warnSetup("set %s: %v", name, err)
			return 1
		}
		return 0
	}
	// One door for all four lookups: paths.OSEnv wraps the same process
	// environment and home-directory reads this jail would otherwise call
	// directly.
	env := paths.OSEnv{}
	unset := func(name string) bool { return env.Get(name) == "" }
	goCache := env.Get("GOCACHE")
	if goCache == "" {
		if cache, err := os.UserCacheDir(); err != nil {
			warnSetup("GOCACHE left unpinned — user cache dir: %v", err)
		} else {
			goCache = filepath.Join(cache, "go-build")
			if code := set("GOCACHE", goCache); code != 0 {
				return code
			}
		}
	}
	if unset("TEST_TELEMETRY_DIR") {
		if goCache == "" {
			warnSetup("TEST_TELEMETRY_DIR left unpinned — GOCACHE is unavailable")
			return 1
		}
		telemetryDirectory, err := filepath.Abs(filepath.Join(goCache, "telemetry"))
		if err != nil {
			warnSetup("TEST_TELEMETRY_DIR left unpinned — resolve under GOCACHE: %v", err)
			return 1
		}
		if code := set("TEST_TELEMETRY_DIR", telemetryDirectory); code != 0 {
			return code
		}
	}
	gopath := env.Get("GOPATH")
	if gopath == "" {
		home, err := env.Home()
		if err != nil {
			warnSetup("GOPATH/GOMODCACHE left unpinned — user home dir: %v", err)
			return 0
		}
		gopath = filepath.Join(home, "go")
		if code := set("GOPATH", gopath); code != 0 {
			return code
		}
	}
	if unset("GOMODCACHE") {
		if code := set("GOMODCACHE", filepath.Join(filepath.SplitList(gopath)[0], "pkg", "mod")); code != 0 {
			return code
		}
	}
	return 0
}
