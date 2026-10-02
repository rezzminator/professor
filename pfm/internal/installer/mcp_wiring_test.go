package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
			t.Fatalf("pfm-mcp.service missing %q; enabled Harvester would not return after login:\n%s", want, unit)
		}
	}
	// systemd user services get systemd's bare default PATH, which cannot see
	// ~/.local/bin — where user-installed engine CLIs live. Without this line
	// every chat the daemon spawns dies at launch on "command not found".
	home := t.TempDir()
	rendered, err := renderServicePath(raw, home)
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
	if schedulerIsLaunchd {
		t.Skip("Linux systemd daemon refresh")
	}
	home := t.TempDir()
	configPath := filepath.Join(home, ".config", "pfm", "config.json")
	if err := os.MkdirAll(configPath, 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{manager: true}
	installer := engine{
		options: Options{
			Mode: ModeApply, Home: home, ConfigDir: filepath.Join(home, ".claude"),
			MCPEnabled: map[string]bool{"chat": true}, MCPPort: 8377,
			MCPConfigPath: configPath, Runner: runner, Stdout: io.Discard,
			Sleep: func(time.Duration) {},
		},
		apply: true, managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"), stamp: "fixture",
	}
	if err := installer.install(context.Background()); err == nil {
		t.Fatal("fixture did not trigger wireMCP failure")
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
	if err := e.writeMCPOpenCodeJSON([]string{professorName}); err != nil {
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

func TestMCPInstallRemovesLegacyAuthOutsideClaudeAccountFiles(t *testing.T) {
	home := t.TempDir()
	configPath := filepath.Join(home, "pfm.config.json")
	credentialPath := filepath.Join(managedRootForHome(home), mcpCredentialName)
	registry := filepath.Join(home, ".claude.json")
	legacyToken := strings.Repeat("a", 64)
	writeFixture(
		t,
		configPath,
		`{"version":2,"mcp":{"servers":{"chat":{"enabled":true}},"authToken":"`+legacyToken+`"}}`,
	)
	writeFixture(t, credentialPath, legacyToken+"\n")
	writeFixture(t, registry, `{"mcpServers":{"chat":{"headers":{"Authorization":"Bearer `+legacyToken+`"}}}}`)
	codex := filepath.Join(home, ".codex")
	configTOML := filepath.Join(codex, "config.toml")
	writeFixture(
		t,
		configTOML,
		mcpFenceBegin+"\n[mcp_servers.chat]\nurl = \"http://127.0.0.1:8377/mcp/chat\"\n[mcp_servers.chat.headers]\nAuthorization = \"Bearer "+legacyToken+"\"\n"+mcpFenceEnd+"\n",
	)
	e := engine{
		options: Options{
			Home:          home,
			MCPConfigPath: configPath,
			CodexHomes:    []string{codex},
			MCPEnabled:    map[string]bool{"chat": true},
			MCPPort:       8377,
			Stdout:        io.Discard,
		},
		managedRoot: managedRootForHome(home),
		apply:       true,
		stamp:       "fixture",
	}
	if err := e.wireMCP(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(credentialPath); !os.IsNotExist(err) {
		t.Fatalf("legacy credential remains: %v", err)
	}
	for _, path := range []string{configPath, configTOML} {
		if raw := readFixture(
			t,
			path,
		); strings.Contains(raw, legacyToken) || strings.Contains(raw, "authToken") ||
			strings.Contains(raw, "Authorization") {
			t.Fatalf("%s retained legacy MCP auth: %s", path, raw)
		}
	}
	if raw := readFixture(t, registry); !strings.Contains(raw, legacyToken) {
		t.Fatalf("Claude registry changed: %s", raw)
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
	if err := e.writeMCPOpenCodeJSON([]string{professorName}); err != nil {
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
	if want := openCodeProfessorShape(home); !sameJSONValue(servers[professorName], want) {
		t.Fatalf("professor registration=%#v, want %#v", servers[professorName], want)
	}
	if err := e.writeMCPOpenCodeJSON([]string{professorName}); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, configPath); got != raw {
		t.Fatalf("idempotent OpenCode apply rewrote the machine config:\nbefore=%s\nafter=%s", raw, got)
	}

	dry := e
	dry.apply = false
	dry.options.Stdout = io.Discard
	before := readFixture(t, configPath)
	if err := dry.writeMCPOpenCodeJSON([]string{professorName}); err != nil {
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
	if err := e.writeMCPOpenCodeJSON([]string{professorName}); err != nil {
		t.Fatal(err)
	}
	if err := e.removeMCPOpenCodeJSON(); err != nil {
		t.Fatal(err)
	}
	if _, ok := openCodeServers()[professorName]; ok {
		t.Fatal("uninstall retained PFM-owned professor")
	}
	if err := e.writeMCPOpenCodeJSON([]string{professorName}); err != nil {
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
	if _, ok := openCodeServers()[professorName]; !ok {
		t.Fatal("uninstall removed a manual replacement of PFM professor")
	}
}

func openCodeProfessorShape(home string) map[string]any {
	return map[string]any{
		"type": "local", "enabled": true,
		"command": []any{filepath.Join(home, ".local", "bin", "pfm"), "mcp", "serve", "--stdio"},
	}
}

// TestMCPOpenCodeInstallRemovesPFMLegacyEntriesTheLedgerNeverListed pins that
// pfm's own pre-professor OpenCode entries go by exact shape alone — no
// ledger entry names them — while every comment and foreign key survives.
func TestMCPOpenCodeInstallRemovesPFMLegacyEntriesTheLedgerNeverListed(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configPath := OpenCodeConfigPath(home)
	bin := filepath.Join(home, ".local", "bin", "pfm")
	writeFixture(t, configPath, `{
  // Operator comment.
  "mcp": {
    // Legacy pfm entries.
    "chat": {"type": "local", "command": ["`+bin+`", "mcp", "chat", "serve"], "enabled": true},
    "harvester": {"type": "remote", "url": "http://127.0.0.1:8456/mcp/harvester", "enabled": true},
    "manual": {"type": "remote", "url": "https://gateway.example.invalid/mcp", "enabled": true}
  }
}
`)
	e := engine{
		options:     Options{Home: home, OpenCodeConfigPath: configPath, MCPPort: 8456, Stdout: io.Discard},
		managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
		apply:       true,
		stamp:       "fixture",
	}
	if err := e.writeMCPOpenCodeJSON([]string{professorName}); err != nil {
		t.Fatal(err)
	}
	raw := readFixture(t, configPath)
	if !strings.Contains(raw, "// Operator comment.") || !strings.Contains(raw, "// Legacy pfm entries.") {
		t.Fatalf("OpenCode legacy cleanup discarded comments:\n%s", raw)
	}
	document, err := decodeJSONCObject([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	servers, _ := document["mcp"].(map[string]any)
	if _, ok := servers[chatName]; ok {
		t.Fatalf("legacy local chat survived:\n%s", raw)
	}
	if _, ok := servers[mcpServerHarvester]; ok {
		t.Fatalf("legacy remote harvester survived:\n%s", raw)
	}
	if servers["manual"] == nil || !sameJSONValue(servers[professorName], openCodeProfessorShape(home)) {
		t.Fatalf("servers=%#v, want manual kept and professor in its local stdio shape", servers)
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

// TestMCPInstallRegistersTheStdioProfessorInAFreshCodexHome pins the one
// transport law on Codex: one fence at the end of the file holding the stdio
// command and args, and no url line.
func TestMCPInstallRegistersTheStdioProfessorInAFreshCodexHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	applyChatMCP(t, home)
	codexConfig := readFixture(t, filepath.Join(home, ".codex", "config.toml"))
	want := strings.Join([]string{
		mcpFenceBegin,
		"[mcp_servers.professor]",
		`command = "` + home + `/.local/bin/pfm"`,
		`args = ["mcp", "serve", "--stdio"]`,
		mcpFenceEnd,
	}, "\n") + "\n"
	if !strings.HasSuffix(codexConfig, want) || strings.Count(codexConfig, mcpFenceBegin) != 1 {
		t.Fatalf("Codex config does not end in the one professor fence:\n%s", codexConfig)
	}
	if strings.Contains(codexConfig, "url = ") {
		t.Fatalf("Codex registration carries a url line:\n%s", codexConfig)
	}
}

func TestInspectHarvesterClientCutoverNamesStandaloneUnreadableAndLegacyPFMCodexStates(t *testing.T) {
	home := t.TempDir()
	codex := filepath.Join(home, ".codex")
	path := filepath.Join(codex, "config.toml")
	writeFixture(
		t,
		path,
		"[mcp_servers.harvester]\ncommand = \"uv\"\nargs = [\"--directory\", \"/fixture/harvester\", \"run\", \"harvester\"]\n",
	)
	reports := InspectHarvesterClientCutover(home, 8377, []string{codex})
	if len(reports) != 2 || reports[0].Client != "codex" || reports[0].State != MCPClientLegacyStandalone {
		t.Fatalf("Codex legacy report=%#v", reports)
	}
	writeFixture(t, path, "broken = [\n")
	reports = InspectHarvesterClientCutover(home, 8377, []string{codex})
	if reports[0].State != MCPClientUnreadable || reports[0].Error == nil ||
		!strings.Contains(reports[0].Error.Error(), "config.toml") {
		t.Fatalf("Codex unreadable report=%#v", reports[0])
	}
	writeFixture(t, path, "[mcp_servers.harvester]\nurl = \"http://127.0.0.1:8377/mcp/harvester\"\n")
	reports = InspectHarvesterClientCutover(home, 8377, []string{codex})
	if reports[0].State != MCPClientLegacyPFM || reports[0].Error != nil {
		t.Fatalf("Codex loopback harvester report=%#v, want pfm legacy route", reports[0])
	}
}

// TestMCPOpenCodeLegacyRemovalIsNamedOnTheChangeLine pins that the OpenCode
// writer names the pfm legacy keys it removes, in the Claude writer's words.
func TestMCPOpenCodeLegacyRemovalIsNamedOnTheChangeLine(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	configPath := OpenCodeConfigPath(home)
	bin := filepath.Join(home, ".local", "bin", "pfm")
	writeFixture(t, configPath,
		`{"mcp":{"chat":{"type":"local","command":["`+bin+`","mcp","chat","serve"],"enabled":true}}}`)
	var out strings.Builder
	e := engine{
		options:     Options{Home: home, OpenCodeConfigPath: configPath, MCPPort: 8456, Stdout: &out},
		managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"),
		apply:       true,
		stamp:       "fixture",
	}
	if err := e.writeMCPOpenCodeJSON([]string{professorName}); err != nil {
		t.Fatal(err)
	}
	var changeLine string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "rewrite "+physicalSettingsPath(configPath)) {
			changeLine = line
			break
		}
	}
	if !strings.HasSuffix(changeLine, " — remove pfm's legacy MCP clients chat") {
		t.Fatalf("OpenCode change line %q does not end naming the removed legacy chat:\n%s", changeLine, out.String())
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
