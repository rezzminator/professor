package installer

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

var plistEnvironmentPath = regexp.MustCompile(
	`<key>EnvironmentVariables</key>\s*<dict>\s*<key>PATH</key>\s*<string>([^<]*)</string>`,
)

// TestMCPLaunchAgentGivesTheDaemonAPathThatFindsTmux is the regression for
// the deaf shared daemon: the plist carried no EnvironmentVariables, so launchd
// ran `pfm mcp serve` on /usr/bin:/bin:/usr/sbin:/sbin. tmux (Homebrew:
// /opt/homebrew/bin, or /usr/local/bin on Intel) was off that PATH, every
// socket probe failed to start, and every chat tool a Codex chat called over
// the daemon answered as if no chat were live. The systemd unit had pinned its
// PATH all along; the launchd twin never did.
//
// It drives the real install path and reads the plist launchd will load.
func TestMCPLaunchAgentGivesTheDaemonAPathThatFindsTmux(t *testing.T) {
	home := t.TempDir()
	installer := &engine{
		options: Options{
			Home:       home,
			Stdout:     io.Discard,
			MCPEnabled: map[string]bool{"chat": true},
			Runner:     &loadedRunner{},
			Sleep:      func(time.Duration) {},
		},
		apply: true,
		stamp: "test",
	}
	if err := installer.wireMCPLaunchAgent(context.Background()); err != nil {
		t.Fatalf("wireMCPLaunchAgent() error = %v", err)
	}
	written, err := os.ReadFile(installer.mcpLaunchAgentPath())
	if err != nil {
		t.Fatalf("read installed MCP launch agent: %v", err)
	}
	match := plistEnvironmentPath.FindSubmatch(written)
	if match == nil {
		t.Fatalf(
			"installed MCP launch agent declares no EnvironmentVariables PATH; launchd will run the daemon on its bare default PATH:\n%s",
			written,
		)
	}
	daemonPath := strings.Split(string(match[1]), ":")
	for _, want := range []string{
		filepath.Join(home, ".local", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/usr/bin",
		"/bin",
	} {
		if !slices.Contains(daemonPath, want) {
			t.Errorf("daemon PATH %q is missing %s", match[1], want)
		}
	}
	if strings.Contains(string(written), "__PFM_HOME__") {
		t.Fatalf("installed MCP launch agent still holds an unrendered __PFM_HOME__:\n%s", written)
	}
}

// TestEveryServiceUnitTakesTheOneServicePath keeps the search path ONE
// implementation: every launchd agent and systemd unit that starts pfm takes
// its PATH from the servicePath marker — never a PATH spelled by hand, which
// is how the systemd unit had one and its launchd twins had none — and the
// rendered unit carries servicePath exactly.
func TestEveryServiceUnitTakesTheOneServicePath(t *testing.T) {
	home := t.TempDir()
	units := 0
	err := fs.WalkDir(embeddedAssets, "assets", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative := strings.TrimPrefix(name, "assets/")
		if !strings.HasPrefix(relative, "launchd/") &&
			(!strings.HasPrefix(relative, "systemd/") || !strings.HasSuffix(relative, ".service")) {
			return nil
		}
		content, err := readAsset(relative)
		if err != nil {
			return err
		}
		if !strings.Contains(string(content), ".local/bin/pfm") {
			return nil
		}
		units++
		if got := strings.Count(string(content), servicePathMarker); got != 1 {
			t.Errorf("%s holds the service PATH marker %d times, want exactly once", relative, got)
			return nil
		}
		rendered, err := renderServicePath(content, home)
		if err != nil {
			return fmt.Errorf("render %s: %w", relative, err)
		}
		if got := strings.Count(string(rendered), servicePath(home)); got != 1 {
			t.Errorf("%s renders servicePath %d times, want once:\n%s", relative, got, rendered)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// 2 launchd agents + 2 systemd services start pfm; fewer means the walk
	// did not look.
	if units < 4 {
		t.Fatalf("found %d pfm service units, want at least 4 — the asset walk did not look", units)
	}
}

// TestNameSyncLaunchAgentGivesTheJobAPathThatFindsTmux is the same regression
// on the other launchd agent: `pfm name-sync` converges every chat's tmux
// window name — the name a VS Code tab shows through set-titles — and under
// launchd's bare PATH it could not exec tmux. It ran 159 times on a live Mac,
// planned "0 windows" each time, and exited 0, so no Codex rename ever reached
// a window or a tab.
func TestNameSyncLaunchAgentGivesTheJobAPathThatFindsTmux(t *testing.T) {
	home := t.TempDir()
	installer := &engine{
		options: Options{Home: home, Stdout: io.Discard, Runner: &loadedRunner{}, Sleep: func(time.Duration) {}},
		apply:   true,
		stamp:   "test",
	}
	if err := installer.wireLaunchAgent(context.Background()); err != nil {
		t.Fatalf("wireLaunchAgent() error = %v", err)
	}
	written, err := os.ReadFile(installer.launchAgentPath())
	if err != nil {
		t.Fatalf("read installed name-sync launch agent: %v", err)
	}
	match := plistEnvironmentPath.FindSubmatch(written)
	if match == nil {
		t.Fatalf(
			"installed name-sync launch agent declares no EnvironmentVariables PATH; launchd will run it on its bare default PATH:\n%s",
			written,
		)
	}
	if want := servicePath(home); string(match[1]) != want {
		t.Fatalf("name-sync PATH = %q, want the one service path %q", match[1], want)
	}
}

// TestLaunchAgentPlistsCarryLogPaths is the regression for a daemon whose
// crash or logged error was silent: launchd drops a background agent's
// stdout/stderr unless StandardOutPath/StandardErrorPath are set, and neither
// embedded plist carried them.
func TestLaunchAgentPlistsCarryLogPaths(t *testing.T) {
	for _, tc := range []struct {
		asset   string
		logFile string
	}{
		{mcpLaunchdAsset, "mcp.log"},
		{launchdAsset, "name-sync.log"},
	} {
		content, err := readAsset(tc.asset)
		if err != nil {
			t.Fatalf("read embedded asset %s: %v", tc.asset, err)
		}
		want := "<string>__PFM_HOME__/Library/Logs/pfm/" + tc.logFile + "</string>"
		if got := strings.Count(string(content), want); got != 2 {
			t.Errorf(
				"%s: StandardOutPath/StandardErrorPath = %d occurrence(s) of %q, want 2 (one each)",
				tc.asset,
				got,
				want,
			)
		}
		for _, key := range []string{"<key>StandardOutPath</key>", "<key>StandardErrorPath</key>"} {
			if !strings.Contains(string(content), key) {
				t.Errorf("%s is missing %s", tc.asset, key)
			}
		}
	}
}

// TestWireLaunchAgentCreatesLogDirInApplyMode is the regression for a plist
// pointing StandardOutPath/StandardErrorPath at a directory that never
// existed — launchd never creates one for a log path, so every line the
// daemon wrote before this fix vanished silently.
func TestWireLaunchAgentCreatesLogDirInApplyMode(t *testing.T) {
	home := t.TempDir()
	installer := &engine{
		options: Options{Home: home, Stdout: io.Discard, Runner: &loadedRunner{}, Sleep: func(time.Duration) {}},
		apply:   true,
		stamp:   "test",
	}
	if err := installer.ensureLaunchdLogDir(); err != nil {
		t.Fatalf("ensureLaunchdLogDir() error = %v", err)
	}
	info, err := os.Stat(filepath.Join(home, "Library", "Logs", "pfm"))
	if err != nil {
		t.Fatalf("stat launchd log dir after apply: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("launchd log dir %s is not a directory", info.Name())
	}
}

// TestWireLaunchAgentPlansLogDirInDryRun mirrors CreatesLogDirInApplyMode: a
// dry run must report the same planned step without touching the filesystem.
func TestWireLaunchAgentPlansLogDirInDryRun(t *testing.T) {
	home := t.TempDir()
	var stdout strings.Builder
	installer := &engine{
		options: Options{Home: home, Stdout: &stdout, Runner: &loadedRunner{}, Sleep: func(time.Duration) {}},
		apply:   false,
		stamp:   "test",
	}
	if err := installer.ensureLaunchdLogDir(); err != nil {
		t.Fatalf("ensureLaunchdLogDir() error = %v", err)
	}
	logDir := filepath.Join(home, "Library", "Logs", "pfm")
	if !strings.Contains(stdout.String(), "change  create "+logDir) {
		t.Fatalf("dry run did not plan the launchd log dir; output:\n%s", stdout.String())
	}
	if _, err := os.Stat(logDir); !os.IsNotExist(err) {
		t.Fatalf("dry run created %s; a preview must not touch the filesystem", logDir)
	}
}

// TestMCPLaunchAgentRemovalNamesTheConfigItReadEnabledFrom is a REGRESSION
// test for issue #24 finding 3/4 (M3 change E): a rollback (or any install
// run over a config with every MCP server disabled) removes the MCP launch
// agent with a bare "remove <path>" — indistinguishable from a deliberate
// opt-out. The removal must name the config it read the disabled state from,
// so "enabled=false because that file is gone after a migration" reads
// differently from "the operator turned it off". Unfixed: the change line is
// "remove <path>" with no config named.
func TestMCPLaunchAgentRemovalNamesTheConfigItReadEnabledFrom(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, ".config", "pfm", "pfm.config.json")
	installer := &engine{
		options: Options{
			Home:          home,
			Stdout:        io.Discard,
			MCPEnabled:    map[string]bool{"chat": false, "harvester": false},
			MCPConfigPath: configPath,
			Runner:        &loadedRunner{},
			Sleep:         func(time.Duration) {},
		},
		apply: false,
		stamp: "test",
	}
	if err := os.MkdirAll(filepath.Dir(installer.mcpLaunchAgentPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installer.mcpLaunchAgentPath(), []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout strings.Builder
	installer.options.Stdout = &stdout
	if err := installer.wireMCPLaunchAgent(context.Background()); err != nil {
		t.Fatalf("wireMCPLaunchAgent() error = %v", err)
	}
	want := "(no MCP server is enabled in " + configPath + ")"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("wireMCPLaunchAgent() output=%q, want the removal to name %q", stdout.String(), want)
	}
}
