package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/servicemanager"
)

const chatRegistrationName = "chat-professor"

func TestJailPinsClaudeConfigDir(t *testing.T) {
	t.Parallel()
	const (
		childEnv    = "PFM_TEST_INSTALLER_JAIL_CHILD"
		sentinelEnv = "PFM_TEST_INSTALLER_JAIL_SENTINEL"
	)
	if os.Getenv(childEnv) == "1" {
		sentinel := os.Getenv(sentinelEnv)
		home := t.TempDir()
		canonical := filepath.Join(home, ".claude")
		writeFixture(t, filepath.Join(canonical, "settings.json"), `{}`)
		configPath := filepath.Join(home, "pfm.config.json")
		writeFixture(t, configPath, `{"version":2}`)
		if _, err := Run(context.Background(), Options{
			Mode: ModeApply, Home: home, ConfigDir: canonical,
			SourceRepo: t.TempDir(), MCPConfigPath: configPath,
			MCPEnabled: map[string]bool{"chat": true},
			MCPPort:    8377, Runner: &fakeRunner{}, Stdout: io.Discard,
		}); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(sentinel)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("ModeApply wrote through inherited CLAUDE_CONFIG_DIR %s: %v", sentinel, entries)
		}
		return
	}

	sentinel := t.TempDir()
	command := exec.Command(os.Args[0], "-test.run=^TestJailPinsClaudeConfigDir$", "-test.v")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "CLAUDE_CONFIG_DIR=") &&
			!strings.HasPrefix(entry, childEnv+"=") &&
			!strings.HasPrefix(entry, sentinelEnv+"=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env,
		"CLAUDE_CONFIG_DIR="+sentinel,
		childEnv+"=1",
		sentinelEnv+"="+sentinel,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("installer test process escaped its CLAUDE_CONFIG_DIR jail: %v\n%s", err, output)
	}
}

func TestMCPSystemdUnitStartsAtLogin(t *testing.T) {
	t.Parallel()
	raw, err := readAsset("systemd/pfm-mcp.service")
	if err != nil {
		t.Fatal(err)
	}
	unit := string(raw)
	for _, want := range []string{"[Install]", "WantedBy=default.target"} {
		if !strings.Contains(unit, want) {
			t.Fatalf("pfm-mcp.service missing %q; enabled chat daemon would not return after login:\n%s", want, unit)
		}
	}
	// systemd user services get systemd's bare default PATH, which cannot see
	// ~/.local/bin — where user-installed engine CLIs live. Without this line
	// every chat the daemon spawns dies at launch on "command not found".
	home := t.TempDir()
	rendered, err := renderServiceEnv(raw, home)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(rendered), "Environment=PATH="+filepath.Join(home, ".local", "bin")+":") {
		t.Fatalf(
			"pfm-mcp.service does not extend PATH with ~/.local/bin; a daemon-spawned chat cannot resolve its engine:\n%s",
			rendered,
		)
	}
}

func TestMCPWireFailureStillRefreshesRunningLinuxDaemon(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	codexConfig := filepath.Join(codexHome, "config.toml")
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "absent.toml"), codexConfig); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{manager: true}
	installer := engine{
		options: Options{
			scheduler: schedulerFor(servicemanager.KindSystemd),
			Mode:      ModeApply, Home: home, ConfigDir: filepath.Join(home, ".claude"),
			MCPEnabled: map[string]bool{"chat": true}, MCPPort: 8377, CodexHomes: []string{codexHome},
			MCPConfigPath: testConfigPath(t), Runner: runner, Stdout: io.Discard,
			Sleep: func(time.Duration) {},
		},
		apply: true, managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"), stamp: "fixture",
	}
	if err := errors.Join(
		append([]error{installer.install(context.Background())}, installer.deferred...)...); err == nil ||
		!strings.Contains(err.Error(), "MCP config is a dangling symlink: "+codexConfig) {
		t.Fatalf("fixture did not retain deferred wireMCP failure: %v", err)
	}
	if calls := strings.Join(runner.calls, "\n"); !strings.Contains(calls, "systemctl --user restart "+mcpUnitName) {
		t.Fatalf("wireMCP failure left running daemon stale:\n%s", calls)
	}
}

func TestMCPOpenCodeCreatesClientJSONWithoutClaimingABackup(t *testing.T) {
	home := t.TempDir()
	path := OpenCodeConfigPath(home)
	var output strings.Builder
	e := engine{
		options:     Options{Home: home, OpenCodeConfigPath: path, MCPPort: 8377, Stdout: &output},
		managedRoot: managedRootForHome(home), apply: true, stamp: "fixture",
	}
	if err := e.writeMCPOpenCodeJSON([]string{chatRegistrationName}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "create "+physicalSettingsPath(path)) ||
		strings.Contains(output.String(), "backup preserved") {
		t.Fatalf("fresh OpenCode registration misreported: %s", output.String())
	}
	if matches, _ := filepath.Glob(path + ".pre-professor-*"); len(matches) != 0 {
		t.Fatalf("fresh OpenCode registration made a backup: %v", matches)
	}
}

func TestMCPOpenCodeWiringPreservesJSONCAndUnownedServers(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configPath := OpenCodeConfigPath(home)
	original := `{
  // OpenCode owns this comment.
  "name": "operator-config",
  "mcp": {
    // The operator owns this server.
    "manual": {"type": "remote", "url": "https://manual.invalid/mcp", "enabled": true}
  }
}
`
	writeFixture(t, configPath, original)
	e := engine{
		options: Options{
			Home: home, OpenCodeConfigPath: configPath, MCPPort: 8456,
			Stdout: io.Discard,
		},
		managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
		apply:       true,
		stamp:       "fixture",
	}
	if err := e.writeMCPOpenCodeJSON([]string{chatRegistrationName}); err != nil {
		t.Fatal(err)
	}
	raw := readFixture(t, configPath)
	if !strings.Contains(raw, "OpenCode owns this comment") || !strings.Contains(raw, `"name": "operator-config"`) ||
		!strings.Contains(raw, `"manual":`) {
		t.Fatalf("OpenCode wiring discarded comments or unowned keys:\n%s", raw)
	}
	document, err := decodeJSONCObject([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	servers, _ := document["mcp"].(map[string]any)
	if want := openCodeChatShape(home); !sameJSONValue(servers[chatRegistrationName], want) {
		t.Fatalf("professor registration=%#v, want %#v", servers[chatRegistrationName], want)
	}
	if err := e.writeMCPOpenCodeJSON([]string{chatRegistrationName}); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, configPath); got != raw {
		t.Fatalf("idempotent OpenCode apply rewrote the machine config:\nbefore=%s\nafter=%s", raw, got)
	}

	dry := e
	dry.apply = false
	dry.options.Stdout = io.Discard
	before := readFixture(t, configPath)
	if err := dry.writeMCPOpenCodeJSON([]string{chatRegistrationName}); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, configPath); got != before {
		t.Fatal("OpenCode dry-run mutated the machine config")
	}
}

func TestMCPOpenCodeUninstallRemovesOnlyExactOwnedRegistrations(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configPath := OpenCodeConfigPath(home)
	writeFixture(
		t,
		configPath,
		`{"mcp":{"manual":{"type":"remote","url":"https://manual.invalid/mcp","enabled":true}}}`,
	)
	e := engine{
		options:     Options{Home: home, OpenCodeConfigPath: configPath, MCPPort: 8456, Stdout: io.Discard},
		managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
		apply:       true,
		stamp:       "fixture",
	}
	openCodeServers := func() map[string]any {
		document, err := decodeJSONCObject([]byte(readFixture(t, configPath)))
		if err != nil {
			t.Fatal(err)
		}
		servers, _ := document["mcp"].(map[string]any)
		return servers
	}
	if err := e.writeMCPOpenCodeJSON([]string{chatRegistrationName}); err != nil {
		t.Fatal(err)
	}
	if err := e.removeMCPOpenCodeJSON(); err != nil {
		t.Fatal(err)
	}
	if _, ok := openCodeServers()[chatRegistrationName]; ok {
		t.Fatal("uninstall retained PFM-owned professor")
	}
	if err := e.writeMCPOpenCodeJSON([]string{chatRegistrationName}); err != nil {
		t.Fatal(err)
	}
	// A manual replacement has precedence over the receipt and must survive.
	raw := readFixture(t, configPath)
	updated := strings.Replace(raw, `"type":"local"`, `"type":"remote","url":"https://manual.invalid/professor"`, 1)
	if updated == raw {
		t.Fatalf("fixture did not replace the owned professor registration:\n%s", raw)
	}
	writeFixture(t, configPath, updated)
	if err := e.removeMCPOpenCodeJSON(); err != nil {
		t.Fatal(err)
	}
	if _, ok := openCodeServers()[chatRegistrationName]; !ok {
		t.Fatal("uninstall removed a manual replacement of PFM professor")
	}
}

func openCodeChatShape(home string) map[string]any {
	return map[string]any{
		"type": "local", "enabled": true,
		"command": []any{filepath.Join(home, ".local", "bin", "pfm"), "mcp", "serve", "--stdio", "--family", "chat"},
	}
}

func applyChatMCP(t *testing.T, home string) string {
	t.Helper()
	canonical := filepath.Join(home, ".claude")
	writeFixture(t, filepath.Join(canonical, "settings.json"), `{}`)
	var applied strings.Builder
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, ConfigDir: canonical,
		MCPEnabled: map[string]bool{"chat": true},
		MCPPort:    8377, Runner: &fakeRunner{}, Stdout: &applied, MCPConfigPath: testConfigPath(t),
	}); err != nil {
		t.Fatalf("apply: %v\n%s", err, applied.String())
	}
	return applied.String()
}

// TestMCPInstallRegistersTheStdioChatFamilyInAFreshCodexHome pins the one
// transport law on Codex: one fence at the end of the file holding the stdio
// command and args, and no url line.
func TestMCPInstallRegistersTheStdioChatFamilyInAFreshCodexHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	applyChatMCP(t, home)
	codexConfig := readFixture(t, filepath.Join(home, ".codex", "config.toml"))
	want := strings.Join([]string{
		mcpFenceBegin,
		"[mcp_servers.chat-professor]",
		`command = "` + home + `/.local/bin/pfm"`,
		`args = ["mcp", "serve", "--stdio", "--family", "chat"]`,
		mcpFenceEnd,
	}, "\n") + "\n"
	if !strings.HasSuffix(codexConfig, want) || strings.Count(codexConfig, mcpFenceBegin) != 1 {
		t.Fatalf("Codex config does not end in the one professor fence:\n%s", codexConfig)
	}
	if strings.Contains(codexConfig, "url = ") {
		t.Fatalf("Codex registration carries a url line:\n%s", codexConfig)
	}
}

// An OpenCode config with no "mcp" key gains one "mcp" object holding the
// server — never "mcp" nested in "mcp" — and a second edit settles.
func TestEditOpenCodeServerCreatesOneMCPObjectAndSettles(t *testing.T) {
	registration := []byte(`{"type":"local","command":["pfm","mcp","serve","--stdio"]}`)
	first, err := editOpenCodeServer([]byte("{\n  \"theme\": \"opencode\"\n}\n"), "professor", registration, false)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(first, &document); err != nil {
		t.Fatalf("edited config %s: %v", first, err)
	}
	servers, ok := document["mcp"].(map[string]any)
	if !ok || servers["professor"] == nil || servers["mcp"] != nil || len(servers) != 1 {
		t.Fatalf("mcp = %v, want exactly the professor server; config=%s", document["mcp"], first)
	}
	second, err := editOpenCodeServer(first, "professor", registration, false)
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("second edit changed the config: %s -> %s err=%v", first, second, err)
	}
}
