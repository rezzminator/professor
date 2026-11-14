package installer

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// serviceUnitBody is a unit as its service manager reads it: the plist after
// its comment block, or the systemd unit without its # lines, with this
// test's home and install mark spelled {home} and {mark}.
func serviceUnitBody(content []byte, home string) string {
	text := string(content)
	if end := strings.Index(text, "-->"); end >= 0 {
		text = text[end+len("-->"):]
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return strings.NewReplacer(home, "{home}", paths.InstallMark(home), "{mark}").Replace(strings.Join(lines, "\n"))
}

// TestLaunchAgentWritesTheRenderedPlist drives the real install path of both
// launchd agents and reads the plist launchd will load: the regression for
// the deaf shared daemon and the name-sync job that ran on launchd's bare
// PATH (/usr/bin:/bin:/usr/sbin:/sbin, no Homebrew tmux), answered as if no
// chat were live and renamed nothing; and for the silent agent whose
// stdout/stderr launchd dropped for want of StandardOutPath/StandardErrorPath.
func TestLaunchAgentWritesTheRenderedPlist(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		wire func(*engine) error
		path func(*engine) string
		want string
	}{
		{
			name: "mcp",
			wire: func(e *engine) error { return e.wireMCPLaunchAgent(context.Background()) },
			path: (*engine).mcpLaunchAgentPath,
			want: `<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.professor.pfm.mcp</string>
	<key>ProgramArguments</key>
	<array>
		<string>{home}/.local/libexec/pfm/pfm-agent</string>
		<string>mcp</string>
		<string>serve</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>{home}/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
		<key>PFM_INSTALL_MARK</key>
		<string>{mark}</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>{home}/Library/Logs/pfm/mcp.log</string>
	<key>StandardErrorPath</key>
	<string>{home}/Library/Logs/pfm/mcp.log</string>
</dict>
</plist>`,
		},
		{
			name: "name-sync",
			wire: func(e *engine) error { return e.wireLaunchAgent(context.Background()) },
			path: (*engine).launchAgentPath,
			want: `<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.professor.pfm.name-sync</string>
	<key>ProgramArguments</key>
	<array>
		<string>{home}/.local/libexec/pfm/pfm-agent</string>
		<string>name-sync</string>
		<string>--apply</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>{home}/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
		<key>PFM_INSTALL_MARK</key>
		<string>{mark}</string>
	</dict>
	<key>WatchPaths</key>
	<array>
		<string>{home}/.codex/session_index.jsonl</string>
	</array>
	<key>StartInterval</key>
	<integer>900</integer>
	<key>RunAtLoad</key>
	<false/>
	<key>ProcessType</key>
	<string>Background</string>
	<key>LowPriorityIO</key>
	<true/>
	<key>StandardOutPath</key>
	<string>{home}/Library/Logs/pfm/name-sync.log</string>
	<key>StandardErrorPath</key>
	<string>{home}/Library/Logs/pfm/name-sync.log</string>
</dict>
</plist>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			installer := &engine{
				options: Options{
					Home: home, Stdout: &bytes.Buffer{}, MCPEnabled: map[string]bool{"chat": true},
					Runner: &loadedRunner{}, Sleep: func(time.Duration) {},
				},
				apply: true,
				stamp: "test",
			}
			if err := tc.wire(installer); err != nil {
				t.Fatalf("wire %s: %v", tc.name, err)
			}
			written, err := os.ReadFile(tc.path(installer))
			if err != nil {
				t.Fatal(err)
			}
			if got := serviceUnitBody(written, home); got != tc.want {
				t.Errorf("%s =\n%s\nwant\n%s", tc.path(installer), got, tc.want)
			}
		})
	}
}

// TestEveryServiceUnitTakesTheOneServicePath renders every launchd and systemd
// unit that starts pfm: each carries paths.ServicePath and this install's
// mark exactly once, so a stop of pfm-launched processes can prove the unit's
// daemon is its own; each launchd agent logs to its own file, each systemd
// service starts the pfm verb that applies, and the MCP daemon returns after
// login (WantedBy=default.target).
func TestEveryServiceUnitTakesTheOneServicePath(t *testing.T) {
	t.Parallel()
	const (
		plistPath = "\t\t<string>{home}/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>"
		plistMark = "\t\t<string>{mark}</string>"
		unitPath  = "Environment=PATH={home}/.local/bin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
		unitMark  = "Environment=PFM_INSTALL_MARK={mark}"
	)
	plistLog := func(name string) string { return "\t<string>__PFM_HOME__/Library/Logs/pfm/" + name + "</string>" }
	want := map[string][]string{
		"launchd/com.professor.pfm.mcp.plist": {plistPath, plistMark, plistLog("mcp.log"), plistLog("mcp.log")},
		"launchd/com.professor.pfm.name-sync.plist": {
			plistPath,
			plistMark,
			plistLog("name-sync.log"),
			plistLog("name-sync.log"),
		},
		"launchd/com.professor.pfm.schedule.plist": {
			plistPath,
			plistMark,
			plistLog("schedule.log"),
			plistLog("schedule.log"),
		},
		"systemd/pfm-mcp.service": {
			unitPath, unitMark, "ExecStart=%h/.local/bin/pfm mcp serve", "WantedBy=default.target",
		},
		"systemd/pfm-name-sync.service": {unitPath, unitMark, "ExecStart=%h/.local/bin/pfm name-sync --apply"},
		"systemd/pfm-schedule.service":  {unitPath, unitMark, "ExecStart=%h/.local/bin/pfm internal schedule-fire"},
	}
	home := t.TempDir()
	got := map[string][]string{}
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
		if !strings.Contains(string(content), ".local/bin/pfm") &&
			!strings.Contains(string(content), agentLauncherMarker) {
			return nil
		}
		rendered, err := renderServiceEnv(content, home)
		if err != nil {
			return err
		}
		got[relative] = outputLines(
			serviceUnitBody(rendered, home),
			"{home}/.local/bin:",
			"{mark}",
			"/Library/Logs/pfm/",
			"ExecStart=",
			"WantedBy=",
		)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for name := range want {
			if !yield(name) {
				return
			}
		}
	}) {
		if !slices.Equal(got[name], want[name]) {
			t.Errorf("%s service lines =\n%q\nwant\n%q", name, got[name], want[name])
		}
		delete(got, name)
	}
	if len(got) != 0 {
		t.Errorf("pfm service units with no expected lines: %q", got)
	}
}

// TestLaunchdLogDir pins ensureLaunchdLogDir both ways: launchd never creates
// a log path's directory, so apply creates it, and a dry run reports the same
// planned step without touching the filesystem.
func TestLaunchdLogDir(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		apply   bool
		wantOut string
		wantDir bool
	}{
		{apply: true, wantOut: "  change  create {home}/Library/Logs/pfm\n", wantDir: true},
		{apply: false, wantOut: "  change  create {home}/Library/Logs/pfm\n", wantDir: false},
	} {
		home := t.TempDir()
		var stdout strings.Builder
		installer := &engine{
			options: Options{Home: home, Stdout: &stdout, Runner: &loadedRunner{}, Sleep: func(time.Duration) {}},
			apply:   tc.apply,
			stamp:   "test",
		}
		if err := installer.ensureLaunchdLogDir(); err != nil {
			t.Fatalf("apply=%v: ensureLaunchdLogDir() error = %v", tc.apply, err)
		}
		if got := strings.ReplaceAll(stdout.String(), home, "{home}"); got != tc.wantOut {
			t.Errorf("apply=%v: output = %q, want %q", tc.apply, got, tc.wantOut)
		}
		info, err := os.Stat(filepath.Join(home, "Library", "Logs", "pfm"))
		if gotDir := err == nil && info.IsDir(); gotDir != tc.wantDir {
			t.Errorf("apply=%v: log dir exists = %v (%v), want %v", tc.apply, gotDir, err, tc.wantDir)
		}
	}
}

// TestMCPLaunchAgentRemovalNamesTheConfigItReadEnabledFrom: a launch agent
// removed because no MCP server is enabled names the config that said so.
func TestMCPLaunchAgentRemovalNamesTheConfigItReadEnabledFrom(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	var stdout strings.Builder
	installer := &engine{
		options: Options{
			Home:          home,
			Stdout:        &stdout,
			MCPEnabled:    map[string]bool{"chat": false},
			MCPConfigPath: filepath.Join(home, "pfm.config.json"),
			Runner:        &loadedRunner{},
			Sleep:         func(time.Duration) {},
		},
		stamp: "test",
	}
	writeFixture(t, installer.mcpLaunchAgentPath(), "<plist/>")
	if err := installer.wireMCPLaunchAgent(context.Background()); err != nil {
		t.Fatalf("wireMCPLaunchAgent() error = %v", err)
	}
	want := "  change  remove {home}/Library/LaunchAgents/com.professor.pfm.mcp.plist" +
		" (no MCP server is enabled in {home}/pfm.config.json)\n"
	if got := strings.ReplaceAll(stdout.String(), home, "{home}"); got != want {
		t.Fatalf("wireMCPLaunchAgent() output = %q, want %q", got, want)
	}
}
