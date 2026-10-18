package installer

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/agentlauncher"
	"github.com/rezzminator/professor/pfm/internal/themesync"
)

func launcherInstaller(home string, runner CommandRunner, apply bool) *engine {
	return &engine{
		options: Options{
			Home:       home,
			Stdout:     io.Discard,
			MCPEnabled: map[string]bool{"chat": true},
			Runner:     runner,
			Sleep:      func(time.Duration) {},
		},
		apply: apply,
		stamp: "test",
	}
}

func wireAllLaunchAgents(t *testing.T, installer *engine) {
	t.Helper()
	ctx := context.Background()
	for name, wire := range map[string]func(context.Context) error{
		"name-sync": installer.wireLaunchAgent,
		"schedule":  installer.wireScheduleLaunchAgent,
		"mcp":       installer.wireMCPLaunchAgent,
	} {
		if err := wire(ctx); err != nil {
			t.Fatalf("wire %s launch agent: %v", name, err)
		}
	}
}

// TestEveryLaunchAgentRunsThroughTheStableLauncher: ProgramArguments[0] of each
// plist install writes is the launcher, never the rebuilt pfm macOS would
// re-register, and the launcher it names exists, executable, exec'ing this
// home's pfm.
func TestEveryLaunchAgentRunsThroughTheStableLauncher(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	installer := launcherInstaller(home, &loadedRunner{}, true)
	wireAllLaunchAgents(t, installer)
	launcher := agentlauncher.Program(home)
	for name, path := range map[string]string{
		"name-sync": installer.launchAgentPath(),
		"schedule":  installer.scheduleLaunchAgentPath(),
		"mcp":       installer.mcpLaunchAgentPath(),
	} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s plist: %v", name, err)
		}
		program, err := plistProgram(content)
		if err != nil || program != launcher {
			t.Errorf("%s ProgramArguments[0] = %q, %v; want the launcher %q", name, program, err, launcher)
		}
		if strings.Contains(string(content), "__PFM_") {
			t.Errorf("%s plist keeps an unrendered marker:\n%s", name, content)
		}
	}
	if current, err := agentlauncher.UpToDate(home); err != nil || !current {
		t.Fatalf("launcher %s is not the one install writes: current=%v, %v", launcher, current, err)
	}
}

// TestReinstallLeavesLauncherPlistsAndLoadedAgentsUntouched is the headline
// regression: a second install over an unchanged host writes no file (same
// inode, same mtime) and never boots an agent out or in — each of which macOS
// counts as a new background item. A moved plist still reloads.
func TestReinstallLeavesLauncherPlistsAndLoadedAgentsUntouched(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	first := &loadedRunner{}
	wireAllLaunchAgents(t, launcherInstaller(home, first, true))
	if !callKind(first.calls, "bootstrap") {
		t.Fatalf("first install bootstrapped nothing: %q", first.calls)
	}
	files := []string{
		agentlauncher.Program(home),
		filepath.Join(home, "Library", "LaunchAgents", launchdLabel+".plist"),
		filepath.Join(home, "Library", "LaunchAgents", scheduleLaunchdLabel+".plist"),
		filepath.Join(home, "Library", "LaunchAgents", mcpLaunchdLabel+".plist"),
	}
	old := time.Unix(1_700_000_000, 0)
	before := map[string]os.FileInfo{}
	for _, path := range files {
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		before[path] = info
	}

	second := &loadedRunner{}
	wireAllLaunchAgents(t, launcherInstaller(home, second, true))
	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if !os.SameFile(before[path], info) || !info.ModTime().Equal(old) {
			t.Errorf("%s was rewritten or touched by a reinstall of unchanged content", path)
		}
	}
	if callKind(second.calls, "bootout") || callKind(second.calls, "bootstrap") {
		t.Fatalf("a reinstall of unchanged agents reloaded one: %q", second.calls)
	}

	// A plist that moved is rewritten and its agent reloaded — and only it.
	if err := os.WriteFile(files[2], []byte("<plist/>\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	third := &loadedRunner{}
	wireAllLaunchAgents(t, launcherInstaller(home, third, true))
	reloaded := 0
	for _, call := range third.calls {
		if strings.Contains(call, " bootstrap ") {
			reloaded++
		}
	}
	if reloaded != 1 || !callKind(third.calls, "bootout") {
		t.Fatalf("a moved plist reloaded %d agents, want exactly its own; calls %q", reloaded, third.calls)
	}
}

// TestInstallMovesARawBinaryPlistOntoTheLauncher: the plist an older pfm wrote
// (ProgramArguments[0] = pfm itself) is rewritten onto the launcher and its
// loaded agent re-registered once.
func TestInstallMovesARawBinaryPlistOntoTheLauncher(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	installer := launcherInstaller(home, &loadedRunner{}, true)
	raw := []byte("<plist><dict><key>ProgramArguments</key><array>\n<string>" +
		filepath.Join(home, ".local", "bin", "pfm") + "</string></array></dict></plist>\n")
	path := installer.scheduleLaunchAgentPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	runner := &loadedRunner{}
	installer = launcherInstaller(home, runner, true)
	if err := installer.wireScheduleLaunchAgent(context.Background()); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if program, err := plistProgram(content); err != nil || program != agentlauncher.Program(home) {
		t.Fatalf("ProgramArguments[0] = %q, %v; want the launcher", program, err)
	}
	if !callKind(runner.calls, "bootout") || !callKind(runner.calls, "bootstrap") {
		t.Fatalf("the migrated agent was not re-registered: %q", runner.calls)
	}
}

func TestDryRunPlansTheLauncherAndWritesNothing(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	installer := launcherInstaller(home, &loadedRunner{}, false)
	var out strings.Builder
	installer.options.Stdout = &out
	wireAllLaunchAgents(t, installer)
	if _, err := os.Lstat(agentlauncher.Program(home)); !os.IsNotExist(err) {
		t.Fatalf("a dry run wrote the launcher: %v", err)
	}
	if !strings.Contains(out.String(), agentlauncher.Program(home)) {
		t.Fatalf("a dry run planned nothing for the missing launcher:\n%s", out.String())
	}
}

// TestUninstallRemovesTheLauncher: the launcher goes with an uninstall on
// every OS, since `pfm theme` stages it wherever it runs; a second uninstall
// finds nothing to remove.
func TestUninstallRemovesTheLauncher(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if err := agentlauncher.Stage(home); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := Run(
			context.Background(),
			Options{Mode: ModeUninstall, Home: home, Runner: &fakeRunner{}, MCPConfigPath: testConfigPath(t)},
		); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(agentlauncher.Program(home)); !os.IsNotExist(err) {
			t.Fatalf("uninstall left the launcher: %v", err)
		}
	}
}

func TestLaunchAgentLauncherDrift(t *testing.T) {
	t.Parallel()
	agentsDir := func(home string) string { return filepath.Join(home, "Library", "LaunchAgents") }
	rawFollower := func(t *testing.T, home string) {
		t.Helper()
		wireAllLaunchAgents(t, launcherInstaller(home, &loadedRunner{}, true))
		writeFixture(t, filepath.Join(agentsDir(home), themesync.FollowerLabel+".plist"),
			"<plist><dict><key>ProgramArguments</key><array><string>"+
				filepath.Join(home, ".local", "bin", "pfm")+"</string></array></dict></plist>")
	}
	syncState := func(t *testing.T, home, body string) {
		t.Helper()
		writeFixture(t, filepath.Join(themesync.DefaultStateDir(home), themesync.StateFileName), body)
	}
	for _, test := range []struct {
		name     string
		setup    func(t *testing.T, home string)
		agents   int
		findings int
		detail   string
		fix      string
	}{
		{"no agents installed", func(*testing.T, string) {}, 0, 0, "", ""},
		{"all on the launcher", func(t *testing.T, home string) {
			wireAllLaunchAgents(t, launcherInstaller(home, &loadedRunner{}, true))
		}, 3, 0, "", ""},
		{"follower on the raw binary, sync on", func(t *testing.T, home string) {
			rawFollower(t, home)
			syncState(t, home, `{"enabled":true}`)
		}, 4, 1, "not the launcher", installFix},
		{"follower on the raw binary, sync off", func(t *testing.T, home string) {
			rawFollower(t, home)
			syncState(t, home, `{"enabled":false}`)
		}, 4, 1, "not the launcher", "pfm theme off"},
		{"follower on the raw binary, no sync state", rawFollower, 4, 1, "not the launcher", "launchctl bootout"},
		{"launcher missing", func(t *testing.T, home string) {
			wireAllLaunchAgents(t, launcherInstaller(home, &loadedRunner{}, true))
			if err := os.Remove(agentlauncher.Program(home)); err != nil {
				t.Fatal(err)
			}
		}, 3, 1, "missing or differs", installFix},
		{"unparsable plist", func(t *testing.T, home string) {
			writeFixture(t, filepath.Join(agentsDir(home), mcpLaunchdLabel+".plist"), "bplist00\x00")
			if _, err := agentlauncher.Ensure(home); err != nil {
				t.Fatal(err)
			}
		}, 1, 1, "unreadable", installFix},
		{"dangling symlink plist", func(t *testing.T, home string) {
			if _, err := agentlauncher.Ensure(home); err != nil {
				t.Fatal(err)
			}
			symlinkFixture(t, filepath.Join(home, "gone.plist"), filepath.Join(agentsDir(home), launchdLabel+".plist"))
		}, 1, 1, "not a regular file", installFix},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			test.setup(t, home)
			agents, findings := LaunchAgentLauncherDrift(home)
			if agents != test.agents || len(findings) != test.findings {
				t.Fatalf("agents=%d findings=%v; want %d agents, %d findings",
					agents, findings, test.agents, test.findings)
			}
			if test.findings == 0 {
				return
			}
			if !strings.Contains(findings[0].Detail, test.detail) || !strings.Contains(findings[0].Fix, test.fix) {
				t.Fatalf("finding %+v, want detail %q and fix %q", findings[0], test.detail, test.fix)
			}
		})
	}
}

func TestRefreshThemeFollowerRunsOnlyWhileTheSyncIsOn(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		on     bool
		apply  bool
		failed error
		called bool
		err    bool
	}{
		{name: "sync off", apply: true},
		{name: "sync on", on: true, apply: true, called: true},
		{name: "sync on, dry run", on: true},
		{
			name: "sync on, refresh fails", on: true, apply: true,
			failed: errors.New("launchctl bootstrap: exit 5"), called: true, err: true,
		},
		{name: "sync on, turned off mid-install", on: true, apply: true, failed: errThemeSyncInert, called: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			if test.on {
				state := filepath.Join(themesync.DefaultStateDir(home), themesync.StateFileName)
				writeFixture(t, state, `{"enabled":true}`)
			}
			called := false
			installer := launcherInstaller(home, &loadedRunner{}, test.apply)
			installer.options.themeFollowerRefresh = func(_ context.Context, got string) error {
				called = got == home
				return test.failed
			}
			err := installer.refreshThemeFollower(context.Background())
			if called != test.called || (err != nil) != test.err {
				t.Fatalf("called=%v err=%v; want called=%v err=%v", called, err, test.called, test.err)
			}
		})
	}
}

func TestThemeFollowerOutcomeNeedsTheFollowerRow(t *testing.T) {
	t.Parallel()
	row := func(surface string, verdict themesync.Verdict, detail string) themesync.SurfaceResult {
		return themesync.SurfaceResult{Surface: surface, Verdict: verdict, Detail: detail}
	}
	for _, test := range []struct {
		name   string
		report themesync.Report
		inert  bool
		err    string
	}{
		{name: "follower applied", report: themesync.Report{Surfaces: []themesync.SurfaceResult{
			row("claude", themesync.VerdictSource, ""), row(themesync.FollowerSurface, themesync.VerdictApplied, ""),
		}}},
		{name: "follower in sync", report: themesync.Report{Surfaces: []themesync.SurfaceResult{
			row(themesync.FollowerSurface, themesync.VerdictInSync, ""),
		}}},
		{name: "follower drift", report: themesync.Report{Surfaces: []themesync.SurfaceResult{
			row(themesync.FollowerSurface, themesync.VerdictDrift, "reload owed"),
		}}, err: "reload owed"},
		{name: "source unresolvable", report: themesync.Report{Surfaces: []themesync.SurfaceResult{
			row("claude", themesync.VerdictError, "no claude theme found"),
		}}, err: "no claude theme found"},
		{name: "sync inert", inert: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := themeFollowerOutcome(test.report)
			switch {
			case test.inert:
				if !errors.Is(err, errThemeSyncInert) {
					t.Fatalf("err = %v, want errThemeSyncInert", err)
				}
			case test.err == "":
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			case err == nil || !strings.Contains(err.Error(), test.err):
				t.Fatalf("err = %v, want it to name %q", err, test.err)
			}
		})
	}
}
