// Package testjail holds the platform setup the suite needs before any test
// runs. It is imported only by _test.go files, so it never reaches the binary.
package testjail

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	harnessprompts "github.com/rezzminator/professor/pfm/harness-prompts"
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

// Run places every test under a short, canonical scratch root. Unix socket paths
// include t.TempDir's test name, so macOS's long TMPDIR can exceed its 104-byte
// limit. Resolving /tmp once also collapses macOS's /private alias.
func Run(m *testing.M) int {
	// The operator's ~/.local/bin and pfm's managed install bin hold pfm's own
	// `claude` launcher shim; a jailed test resolving a binary through PATH must
	// never reach it. Scrub before anything below resolves a binary.
	hostEnv := paths.OSEnv{}
	operatorHome, homeErr := hostEnv.Home()
	if homeErr != nil {
		warnSetup("user home unknown, stripping only pfm install bin dirs from PATH: %v", homeErr)
		operatorHome = ""
	}
	if err := os.Setenv("PATH", scrubOperatorPATH(hostEnv.Get("PATH"), operatorHome)); err != nil {
		warnSetup("scrub PATH: %v", err)
		return 1
	}
	if err := pinLaunchShell(deps.Resolve); err != nil {
		warnSetup("%v", err)
		return 1
	}
	// No operator account or login default reaches an installer test as an MCP write target; installers enter here.
	sentinelErr := os.Unsetenv("PFM_CLAUDE_CONFIG_DIR_DEFAULT") // claudelaunch.ConfigDirDefaultEnv; import cycle
	if err := errors.Join(os.Setenv("CLAUDE_CONFIG_DIR", ""), sentinelErr); err != nil {
		warnSetup("clear CLAUDE_CONFIG_DIR and the login default's sentinel: %v", err)
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
	// The gate's step profiler exports BASH_ENV with `set -E` and an ERR trap so
	// every shell suite records its failures. Bash skips its exec-the-last-command
	// step while an ERR trap is set, so a tmux pane launched as `bash -c '<launch>'`
	// stays bash instead of becoming the launched program. Shell suites keep the
	// tracer; a Go test process, and every child it starts, does not. Unset, not
	// emptied, so no child sees the name at all.
	if err := os.Unsetenv("BASH_ENV"); err != nil {
		warnSetup("clear BASH_ENV: %v", err)
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
			warnSetup("set %s to %s: %v", name, value, err)
			return 1
		}
	}
	// No test fetches a source-fetched global skill from its public repo: an
	// install run in the jail skips the fetch and doctor reports OFFLINE.
	for _, name := range []string{paths.EnvSkillSourcesOffline, paths.EnvThemesOffline} {
		if err := os.Setenv(name, "1"); err != nil {
			warnSetup("set %s: %v", name, err)
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
		cleanup, setupErr := jailHome(os.TempDir())
		defer cleanup()
		if setupErr != nil {
			warnSetup("%v", setupErr)
			return 1
		}
		return runProfiled(m)
	}
	// No wrapper directory of our own: t.TempDir() already makes a unique path
	// per test and removes it. An extra layer would only spend a dozen of the
	// 104 bytes a socket path is allowed, which is exactly the budget the
	// longest test names need.
	if err := os.Setenv("TMPDIR", base); err != nil {
		warnSetup("set TMPDIR to %s: %v", base, err)
		return 1
	}
	cleanup, setupErr := jailHome(base)
	defer cleanup()
	if setupErr != nil {
		warnSetup("%v", setupErr)
		return 1
	}
	return runProfiled(m)
}

// activeProfiler is the profiler of this process while runProfiled runs its
// tests, nil when profiling is off; PauseFlightRecorder reaches it here.
var activeProfiler atomic.Pointer[profiler]

// runProfiled runs the package tests under the always-on profiler (profile.go).
func runProfiled(m *testing.M) int {
	p, off := startProfile()
	activeProfiler.Store(p)
	code := m.Run()
	activeProfiler.Store(nil)
	p.finish(code)
	if off != "" && code != 0 {
		warnSetup("profile off — %s; no diagnosis bundle for this red run", off)
	}
	return code
}

// pfmInstallBinSuffix is the tail of pfm's managed install bin dir, whichever
// home owns it.
var pfmInstallBinSuffix = filepath.Join(".local", "share", "pfm", "install", "bin")

// scrubOperatorPATH drops every PATH entry that is <home>/.local/bin or a pfm
// install bin dir (a path ending .local/share/pfm/install/bin, any home),
// comparing after filepath.Clean and, where it resolves, EvalSymlinks. Order
// and every other entry — empty ones included — are kept. An empty home matches
// no ~/.local/bin, so only install bin dirs go.
func scrubOperatorPATH(pathEnv, home string) string {
	var homeBins []string
	if home != "" {
		bin := filepath.Join(home, ".local", "bin")
		homeBins = append(homeBins, bin)
		if resolved, err := filepath.EvalSymlinks(bin); err == nil {
			homeBins = append(homeBins, filepath.Clean(resolved))
		}
	}
	isOperatorBin := func(candidate string) bool {
		candidate = filepath.Clean(candidate)
		for _, bin := range homeBins {
			if candidate == bin {
				return true
			}
		}
		return candidate == pfmInstallBinSuffix ||
			strings.HasSuffix(candidate, string(filepath.Separator)+pfmInstallBinSuffix)
	}
	var kept []string
	for _, entry := range filepath.SplitList(pathEnv) {
		if entry != "" {
			if isOperatorBin(entry) {
				continue
			}
			if resolved, err := filepath.EvalSymlinks(entry); err == nil && isOperatorBin(resolved) {
				continue
			}
		}
		kept = append(kept, entry)
	}
	return strings.Join(kept, string(os.PathListSeparator))
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
// A home or default-account creation failure returns an error to Run, which
// refuses to run the package tests and still cleans up the jail.
func jailHome(base string) (func(), error) {
	home, err := os.MkdirTemp(base, "pfm-jail-home-")
	if err != nil {
		return func() {}, fmt.Errorf("create jailed home under %s: %w", base, err)
	}
	cleanup := func() {
		if err := os.RemoveAll(home); err != nil && !errors.Is(err, fs.ErrNotExist) {
			warnSetup("remove jail home %s: %v", home, err)
		}
	}
	accountDir := config.DefaultAccountDir(home, 1)
	if err := os.MkdirAll(accountDir, 0o700); err != nil {
		return cleanup, fmt.Errorf("create jailed account directory %s: %w", accountDir, err)
	}
	if err := os.Setenv(paths.EnvHome, home); err != nil {
		return cleanup, fmt.Errorf("set %s to %s: %w", paths.EnvHome, home, err)
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
	if err := pinXDGConfigHome(home); err != nil {
		return cleanup, err
	}
	if paths.EnvOr(paths.EnvSIDDir, "") == "" {
		sidDir := filepath.Join(home, "sid")
		if err := os.Mkdir(sidDir, 0o700); err != nil {
			warnSetup("create %s under %s: %v", paths.EnvSIDDir, home, err)
		} else if err := os.Setenv(paths.EnvSIDDir, sidDir); err != nil {
			warnSetup("set %s to %s: %v", paths.EnvSIDDir, sidDir, err)
		}
	}
	return cleanup, nil
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
	if err := WriteExecutable(canonical, []byte("jailed-pfm"), 0o700); err != nil {
		t.Fatal(err)
	}
	managedClaude := filepath.Join(jailedHome, ".local", "share", "pfm", "install", "bin", claudeBinary)
	if err := os.MkdirAll(filepath.Dir(managedClaude), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteExecutable(managedClaude, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(managedClaude, filepath.Join(jailedHome, ".local", "bin", claudeBinary)); err != nil {
		t.Fatal(err)
	}
	// A healthy install carries both overlays as managed copies and links.
	for _, overlay := range []string{"pfm-statusline", "tmux-title-renudge"} {
		managedOverlay := filepath.Join(jailedHome, ".local", "share", "pfm", "install", "bin", overlay)
		if err := os.MkdirAll(filepath.Dir(managedOverlay), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := WriteExecutable(managedOverlay, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
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
	account := config.DefaultAccountDir(jailedHome, 1)
	if err := os.MkdirAll(account, 0o700); err != nil {
		t.Fatal(err)
	}
	content, err := json.Marshal(
		map[string]any{"version": 2, "accounts": []map[string]any{{"id": 1, "configDir": account}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jailedHome, "pfm.config.json"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	StageGlobalAgents(t, jailedHome)
	stageCloneZshrc(t, jailedHome)
	return root
}

// StageAccountLinks stages shared directories and files, then links them into an account.
func StageAccountLinks(t *testing.T, home, accountDir string, dirs []string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(accountDir, 0o700); err != nil {
		t.Fatalf("create account directory %s: %v", accountDir, err)
	}
	for _, entry := range dirs {
		store := filepath.Join(home, ".claude", entry)
		if err := os.MkdirAll(store, 0o700); err != nil {
			t.Fatalf("create store directory %s: %v", store, err)
		}
		link := filepath.Join(accountDir, entry)
		if err := os.Symlink(store, link); err != nil {
			t.Fatalf("link account entry %s: %v", link, err)
		}
	}
	for name, seed := range files {
		store := filepath.Join(home, ".claude", name)
		if err := os.MkdirAll(filepath.Dir(store), 0o700); err != nil {
			t.Fatalf("create store directory for %s: %v", store, err)
		}
		if _, err := os.Lstat(store); errors.Is(err, fs.ErrNotExist) {
			if err := os.WriteFile(store, []byte(seed), 0o600); err != nil {
				t.Fatalf("create store file %s: %v", store, err)
			}
		} else if err != nil {
			t.Fatalf("inspect store file %s: %v", store, err)
		}
		link := filepath.Join(accountDir, name)
		if err := os.Symlink(store, link); err != nil {
			t.Fatalf("link account entry %s: %v", link, err)
		}
	}
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
func CleanHome(t *testing.T, dirs []string, files map[string]string) config.Runtime {
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
	for _, id := range []int{1, 2} {
		StageAccountLinks(t, home, config.DefaultAccountDir(home, id), dirs, files)
	}

	canonical := filepath.Join(canonicalDir, "pfm")
	if err := WriteExecutable(canonical, []byte("target-pfm"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteExecutable(filepath.Join(hostShimDir, "pfm"), []byte("host-pfm"), 0o700); err != nil {
		t.Fatal(err)
	}
	managedClaude := filepath.Join(home, ".local", "share", "pfm", "install", "bin", claudeBinary)
	if err := os.MkdirAll(filepath.Dir(managedClaude), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteExecutable(managedClaude, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(managedClaude, filepath.Join(canonicalDir, claudeBinary)); err != nil {
		t.Fatal(err)
	}
	// Match the installed-home overlay shape.
	for _, overlay := range []string{"pfm-statusline", "tmux-title-renudge"} {
		managedOverlay := filepath.Join(home, ".local", "share", "pfm", "install", "bin", overlay)
		if err := os.MkdirAll(filepath.Dir(managedOverlay), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := WriteExecutable(managedOverlay, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(managedOverlay, filepath.Join(canonicalDir, overlay)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv(paths.EnvHome, home)
	configPath := filepath.Join(home, "pfm.config.json")
	if err := os.WriteFile(configPath, []byte("{\"version\":2,\"ask\":{\"engine\":\"claude\"}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvConfig, configPath)
	StageSourceRepoMarker(t, home)
	StageGlobalAgents(t, home)
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

// StageClaudePlugins stages the caller's plugin roster in a shared Claude store.
func StageClaudePlugins(t *testing.T, store string, ids []string) {
	t.Helper()
	settingsPath := filepath.Join(store, "settings.json")
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	enabled := make(map[string]bool)
	records := make(map[string]any)
	for _, id := range ids {
		installPath := filepath.Join(store, "plugins", "cache", id)
		if err := os.MkdirAll(installPath, 0o700); err != nil {
			t.Fatal(err)
		}
		enabled[id] = true
		records[id] = []any{map[string]any{"installPath": installPath}}
	}
	document["enabledPlugins"] = enabled
	for path, value := range map[string]any{settingsPath: document, filepath.Join(store, "plugins", "installed_plugins.json"): map[string]any{"version": 2, "plugins": records}} {
		content, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
