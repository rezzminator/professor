package installer

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestJailPinsClaudeConfigDir(t *testing.T) {
	const (
		childEnv    = "PFM_TEST_INSTALLER_JAIL_CHILD"
		sentinelEnv = "PFM_TEST_INSTALLER_JAIL_SENTINEL"
	)
	if os.Getenv(childEnv) == "1" {
		sentinel := os.Getenv(sentinelEnv)
		home := t.TempDir()
		canonical := filepath.Join(home, ".claude")
		writeFixture(t, filepath.Join(canonical, "settings.json"), `{}`)
		if _, err := Run(context.Background(), Options{
			Mode: ModeApply, Home: home, ConfigDir: canonical,
			ConfigDirs: []string{canonical}, MCPEnabled: map[string]bool{"chat": true},
			MCPPort: 8377, Runner: &fakeRunner{}, Stdout: io.Discard,
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

// TestMCPInstallCreatesClientJSONWithoutClaimingABackup is an end-to-end pin
// on the #9 fix: writeMCPClientJSON must run its message through
// changeDescription, not report a hardcoded "rewrite ... (backup preserved)"
// regardless of whether .mcp.json existed. Asserting changeDescription alone
// would not catch the original bug — the bug was that the helper was never
// consulted at this call site.
func TestMCPInstallCreatesClientJSONWithoutClaimingABackup(t *testing.T) {
	home := t.TempDir()
	canonical := filepath.Join(home, ".claude")
	writeFixture(t, filepath.Join(canonical, "settings.json"), `{}`)
	var applied strings.Builder
	options := Options{
		Mode: ModeApply, Home: home, ConfigDir: canonical,
		ConfigDirs: []string{canonical}, MCPEnabled: map[string]bool{"chat": true},
		MCPPort: 8377, Runner: &fakeRunner{}, Stdout: &applied,
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatalf("apply on a fresh home: %v\n%s", err, applied.String())
	}
	clientPath := filepath.Join(home, ".claude.json")
	out := applied.String()
	if !strings.Contains(out, "create "+physicalSettingsPath(clientPath)) {
		t.Fatalf("apply output never says it created %s:\n%s", clientPath, out)
	}
	if strings.Contains(out, "rewrite "+physicalSettingsPath(clientPath)+" (backup preserved)") {
		t.Fatalf("claimed a backed-up rewrite for a client registration that never existed:\n%s", out)
	}
	if matches, _ := filepath.Glob(clientPath + ".pre-professor-*"); len(matches) != 0 {
		t.Fatalf("backup written for a client registration that did not exist: %v", matches)
	}
}

// TestMCPInstallWiresConfigDrivenUnauthenticatedLoopbackClients pins a fresh
// install's client wiring: with both families enabled, the Claude registry
// gains the one professor stdio registration and the ledger owns it.
func TestMCPInstallWiresConfigDrivenUnauthenticatedLoopbackClients(t *testing.T) {
	home := t.TempDir()
	canonical := filepath.Join(home, ".claude")
	secondary := filepath.Join(home, "account-two")
	writeFixture(t, filepath.Join(canonical, "settings.json"), `{}`)
	writeFixture(t, filepath.Join(secondary, "settings.json"), `{}`)
	configPath := filepath.Join(home, ".config", "pfm", "config.json")
	writeFixture(t, configPath, `{"version":2,"mcp":{"servers":{"chat":{"enabled":true}},"http":{"port":8456}}}`)
	runner := &fakeRunner{manager: true}

	options := Options{
		Mode:          ModeApply,
		Home:          home,
		ConfigDir:     canonical,
		ConfigDirs:    []string{canonical, secondary},
		MCPEnabled:    map[string]bool{"chat": true, "harvester": true},
		MCPPort:       8456,
		MCPConfigPath: configPath,
		Runner:        runner,
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(canonical, "settings.json"),
		filepath.Join(secondary, "settings.json"),
	} {
		raw := readFixture(t, path)
		for _, command := range []string{
			home + "/.local/bin/pfm internal explore-deny",
			home + "/.local/bin/pfm internal epic-inject",
			home + "/.local/bin/pfm internal compact-nudge",
		} {
			if !strings.Contains(raw, command) {
				t.Fatalf("%s missing installer hook %q", path, command)
			}
		}
		var document map[string]any
		if err := json.Unmarshal([]byte(raw), &document); err != nil {
			t.Fatal(err)
		}
		if document["cleanupPeriodDays"] != float64(36500) {
			t.Fatalf("%s cleanupPeriodDays=%v", path, document["cleanupPeriodDays"])
		}
	}
	credential := filepath.Join(home, ".local", "share", "pfm", "install", mcpCredentialName)
	if _, err := os.Stat(credential); !os.IsNotExist(err) {
		t.Fatalf("credential file exists in an unauthenticated MCP install: %v", err)
	}
	var clients map[string]any
	clientJSON := readFixture(t, filepath.Join(home, ".claude.json"))
	if err := json.Unmarshal([]byte(clientJSON), &clients); err != nil {
		t.Fatal(err)
	}
	servers, _ := clients["mcpServers"].(map[string]any)
	wantProfessor := map[string]any{
		"type": "stdio", "command": home + "/.local/bin/pfm", "args": []any{"mcp", "serve", "--stdio"},
	}
	if len(servers) != 1 || !sameJSONValue(servers[professorName], wantProfessor) {
		t.Fatalf("Claude registry servers=%#v, want only professor=%#v", servers, wantProfessor)
	}
	ownership, err := readMCPOwnership(filepath.Join(home, ".local", "share", "pfm", "install", mcpOwnershipName))
	if err != nil {
		t.Fatal(err)
	}
	owned := ownership.Registrations[physicalSettingsPath(filepath.Join(home, ".claude.json"))]
	if len(owned) != 1 || !sameJSONValue(owned[professorName], wantProfessor) {
		t.Fatalf("ledger owns %#v, want professor alone", owned)
	}
	for _, forbidden := range []string{"Authorization", "Bearer", "headers"} {
		if strings.Contains(clientJSON, forbidden) {
			t.Fatalf("client registration retained MCP authentication %q: %s", forbidden, clientJSON)
		}
	}
	if codex := readFixture(
		t,
		filepath.Join(home, ".codex", "config.toml"),
	); strings.Contains(codex, "Authorization") ||
		strings.Contains(codex, "Bearer") {
		t.Fatalf("Codex registration retained MCP authentication: %s", codex)
	}
	if config := readFixture(t, configPath); strings.Contains(config, "authToken") {
		t.Fatalf("PFM config retained MCP authentication: %s", config)
	}
	unitPath := filepath.Join(home, ".config", "systemd", "user", mcpUnitName)
	activation := "systemctl --user restart " + mcpUnitName
	if schedulerIsLaunchd {
		unitPath = filepath.Join(home, "Library", "LaunchAgents", mcpLaunchdLabel+".plist")
		activation = "launchctl bootstrap gui/"
	}
	if _, err := os.Stat(unitPath); err != nil {
		t.Fatalf("MCP systemd unit missing: %v", err)
	}
	if calls := strings.Join(runner.calls, "\n"); !strings.Contains(calls, activation) {
		t.Fatalf("MCP daemon was not restarted after complete client wiring:\n%s", calls)
	}

	if report, err := Run(context.Background(), options); err != nil || report.Changed != 0 {
		t.Fatalf("second apply report=%#v err=%v", report, err)
	}
	if _, err := Run(context.Background(), Options{
		Mode:       ModeUninstall,
		Home:       home,
		ConfigDir:  canonical,
		ConfigDirs: []string{canonical, secondary},
		MCPEnabled: options.MCPEnabled,
		Runner:     &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(credential); !os.IsNotExist(err) {
		t.Fatalf("uninstall retained credential: %v", err)
	}
}

func TestMCPInstallRemovesLegacyCredentialAndAuthHeadersEverywhere(t *testing.T) {
	home := t.TempDir()
	canonical := filepath.Join(home, ".claude")
	writeFixture(t, filepath.Join(canonical, "settings.json"), `{}`)
	configPath := filepath.Join(home, ".config", "pfm", "config.json")
	credentialPath := filepath.Join(home, ".local", "share", "pfm", "install", mcpCredentialName)
	legacyToken := strings.Repeat("a", 64)
	writeFixture(
		t,
		configPath,
		`{"version":2,"mcp":{"servers":{"chat":{"enabled":true}},"authToken":"`+legacyToken+`"}}`,
	)
	writeFixture(t, credentialPath, legacyToken+"\n")
	writeFixture(
		t,
		filepath.Join(home, ".local", "share", "pfm", "install", mcpOwnershipName),
		`{"credential":"`+credentialPath+`","clients":["chat"]}`,
	)
	writeFixture(
		t,
		filepath.Join(home, ".mcp.json"),
		`{"mcpServers":{"chat":{"type":"http","url":"http://127.0.0.1:8377/mcp/chat","headers":{"Authorization":"Bearer `+legacyToken+`"}}}}`,
	)
	writeFixture(t, filepath.Join(home, ".codex", "config.toml"), mcpFenceBegin+"\n"+
		"[mcp_servers.chat]\n"+
		"url = \"http://127.0.0.1:8377/mcp/chat\"\n"+
		"[mcp_servers.chat.headers]\n"+
		"Authorization = \"Bearer "+legacyToken+"\"\n"+
		mcpFenceEnd+"\n")
	options := Options{
		Mode: ModeApply, Home: home, ConfigDir: canonical,
		ConfigDirs: []string{canonical}, MCPEnabled: map[string]bool{"chat": true},
		MCPPort: 8377, MCPConfigPath: configPath, Runner: &fakeRunner{},
	}
	previewOptions := options
	previewOptions.Mode = ModeDryRun
	var preview strings.Builder
	previewOptions.Stdout = &preview
	if _, err := Run(context.Background(), previewOptions); err != nil {
		t.Fatalf("preview legacy MCP cleanup: %v\n%s", err, preview.String())
	}
	for _, path := range []string{
		configPath,
		credentialPath,
		physicalSettingsPath(filepath.Join(home, ".claude.json")),
		filepath.Join(home, ".codex", "config.toml"),
		filepath.Join(home, ".local", "share", "pfm", "install", mcpOwnershipName),
	} {
		if !strings.Contains(preview.String(), path) {
			t.Errorf("preview omitted MCP apply path %s:\n%s", path, preview.String())
		}
	}
	if config := readFixture(t, configPath); !strings.Contains(config, "authToken") {
		t.Fatalf("preview mutated the legacy MCP config: %s", config)
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(credentialPath); !os.IsNotExist(err) {
		t.Fatalf("legacy credential remains: %v", err)
	}
	for _, path := range []string{
		configPath,
		filepath.Join(home, ".claude.json"),
		filepath.Join(home, ".codex", "config.toml"),
	} {
		raw := readFixture(t, path)
		for _, forbidden := range []string{legacyToken, "authToken", "Authorization", "Bearer"} {
			if strings.Contains(raw, forbidden) {
				t.Fatalf("%s retained legacy MCP authentication %q: %s", path, forbidden, raw)
			}
		}
	}
}

func TestMCPManualConflictIsNotClaimedOrRemoved(t *testing.T) {
	home := t.TempDir()
	canonical := filepath.Join(home, ".claude")
	writeFixture(t, filepath.Join(canonical, "settings.json"), `{}`)
	manual := `{"mcpServers":{"harvester":{"type":"stdio","command":"manual-harvester"}}}`
	writeFixture(t, filepath.Join(home, ".claude.json"), manual)
	options := Options{
		Mode: ModeApply, Home: home, ConfigDir: canonical,
		ConfigDirs: []string{canonical},
		MCPEnabled: map[string]bool{"chat": true, "harvester": true},
		MCPPort:    8377, Runner: &fakeRunner{},
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	var ownership mcpOwnership
	ownershipPath := filepath.Join(home, ".local", "share", "pfm", "install", mcpOwnershipName)
	if err := json.Unmarshal([]byte(readFixture(t, ownershipPath)), &ownership); err != nil {
		t.Fatal(err)
	}
	owned := ownership.Registrations[physicalSettingsPath(filepath.Join(home, ".claude.json"))]
	if len(owned) != 1 || owned[professorName] == nil {
		t.Fatalf("owned registrations=%v, want professor only", owned)
	}
	codexConfig := readFixture(t, filepath.Join(home, ".codex", "config.toml"))
	if !strings.Contains(codexConfig, "[mcp_servers.professor]") {
		t.Fatalf("an unrelated Claude conflict prevented Codex wiring: %s", codexConfig)
	}

	if _, err := Run(context.Background(), Options{
		Mode: ModeUninstall, Home: home, ConfigDir: canonical,
		ConfigDirs: []string{canonical}, MCPEnabled: options.MCPEnabled,
		Runner: &fakeRunner{},
	}); err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(readFixture(t, filepath.Join(home, ".claude.json"))), &document); err != nil {
		t.Fatal(err)
	}
	servers, _ := document["mcpServers"].(map[string]any)
	if _, ok := servers["harvester"]; !ok {
		t.Fatal("uninstall removed the conflicting manual Harvester registration")
	}
	if _, ok := servers[professorName]; ok {
		t.Fatal("uninstall retained PFM's owned professor registration")
	}
}

func TestMCPOpenCodeWiringPreservesJSONCAndUnownedServers(t *testing.T) {
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

func readClaudeServers(t *testing.T, path string) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(readFixture(t, path)), &document); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	servers, _ := document["mcpServers"].(map[string]any)
	return servers
}

func claudeProfessorShape(home string) map[string]any {
	return map[string]any{
		"type": "stdio", "command": home + "/.local/bin/pfm", "args": []any{"mcp", "serve", "--stdio"},
	}
}

func applyChatMCP(t *testing.T, home string) string {
	t.Helper()
	canonical := filepath.Join(home, ".claude")
	writeFixture(t, filepath.Join(canonical, "settings.json"), `{}`)
	var applied strings.Builder
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, ConfigDir: canonical,
		ConfigDirs: []string{canonical}, MCPEnabled: map[string]bool{"chat": true},
		MCPPort: 8377, Runner: &fakeRunner{}, Stdout: &applied,
	}); err != nil {
		t.Fatalf("apply: %v\n%s", err, applied.String())
	}
	return applied.String()
}

// TestMCPInstallRemovesPFMLegacyClaudeEntriesTheLedgerNeverListed pins that
// pfm's own pre-professor Claude entries — stdio chat, HTTP chat and
// harvester, the retired bearer shape included — go by exact shape alone,
// with an empty ledger, while every other key stays as it was.
func TestMCPInstallRemovesPFMLegacyClaudeEntriesTheLedgerNeverListed(t *testing.T) {
	bearer := `{"type":"http","url":"http://127.0.0.1:8377/mcp/chat","headers":{"Authorization":"Bearer ` +
		strings.Repeat("0f", 32) + `"}}`
	for name, fixture := range map[string]struct{ legacy, removed string }{
		"stdio chat and http harvester": {
			legacy: `"chat":{"type":"stdio","command":"HOME/.local/bin/pfm","args":["mcp","chat","serve"]},` +
				`"harvester":{"type":"http","url":"http://127.0.0.1:8377/mcp/harvester"}`,
			removed: "chat,harvester",
		},
		"bearer http chat": {legacy: `"chat":` + bearer, removed: "chat"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			home := t.TempDir()
			clientPath := filepath.Join(home, ".claude.json")
			writeFixture(t, clientPath, `{"theme":"dark","mcpServers":{`+
				strings.ReplaceAll(fixture.legacy, "HOME", home)+`,"foreign":{"command":"custom","args":["x"]}}}`)
			applied := applyChatMCP(t, home)
			if !strings.Contains(applied, "remove pfm's legacy MCP clients "+fixture.removed) {
				t.Fatalf("change line does not name the removed legacy keys %s:\n%s", fixture.removed, applied)
			}
			var document map[string]any
			if err := json.Unmarshal([]byte(readFixture(t, clientPath)), &document); err != nil {
				t.Fatal(err)
			}
			servers, _ := document["mcpServers"].(map[string]any)
			want := map[string]any{
				"foreign":     map[string]any{"command": "custom", "args": []any{"x"}},
				professorName: claudeProfessorShape(home),
			}
			if document["theme"] != "dark" || !sameJSONValue(servers, want) {
				t.Fatalf("registry=%#v, want theme kept and servers=%#v", document, want)
			}
		})
	}
}

// TestMCPInstallMigratesALedgerListedLegacyHTTPChatClient pins the names-only
// predecessor ledger: its listed HTTP chat at ~/.mcp.json is pfm's own legacy
// entry, removed rather than treated as a manual conflict, and professor
// lands in the registry a pfm-launched Claude reads.
func TestMCPInstallMigratesALedgerListedLegacyHTTPChatClient(t *testing.T) {
	// Issue #24 F11: pin CLAUDE_CONFIG_DIR to empty so a host that exports it
	// ambiently cannot steer MCP registration at an extra, real
	// $CLAUDE_CONFIG_DIR/.claude.json this fixture never wrote (host-only —
	// cannot be watched failing inside a fence that does not export it).
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	writeFixture(t, filepath.Join(home, ".mcp.json"),
		`{"mcpServers":{"chat":{"type":"http","url":"http://127.0.0.1:8377/mcp/chat"}}}`)
	writeFixture(t, filepath.Join(home, ".local", "share", "pfm", "install", mcpOwnershipName), `{"clients":["chat"]}`)
	applied := applyChatMCP(t, home)
	if strings.Contains(applied, "preserve conflicting manual MCP client") {
		t.Fatalf("pfm's own legacy HTTP chat registration was treated as a manual conflict:\n%s", applied)
	}
	if servers := readClaudeServers(t, filepath.Join(home, ".mcp.json")); len(servers) != 0 {
		t.Fatalf("legacy .mcp.json kept %#v", servers)
	}
	servers := readClaudeServers(t, filepath.Join(home, ".claude.json"))
	if !sameJSONValue(servers[professorName], claudeProfessorShape(home)) {
		t.Fatalf("professor was not registered: %#v", servers)
	}
	// Doctor reads the registration install just wrote as pfm's own, never
	// as a foreign one it would tell the operator to reinstall over.
	for _, report := range InspectClaudeServers(filepath.Join(home, ".claude.json"), home, 8377, professorName) {
		if report.State != MCPClientPFM {
			t.Fatalf("doctor classifies install's own professor as %s, want %s", report.State, MCPClientPFM)
		}
	}
}

// TestMCPInstallRemovesRootMCPJSONLegacyEntriesTheLedgerNeverListed pins the
// remediation doctor's cutover row prints for a legacy-pfm harvester at
// ~/.mcp.json ("run pfm install --yes"): install removes pfm's legacy entries
// there by exact shape alone, with no ledger naming them, and leaves every
// other key as it was.
func TestMCPInstallRemovesRootMCPJSONLegacyEntriesTheLedgerNeverListed(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	root := filepath.Join(home, ".mcp.json")
	writeFixture(t, root, `{"mcpServers":{`+
		`"harvester":{"type":"http","url":"http://127.0.0.1:8377/mcp/harvester"},`+
		`"chat":{"type":"http","url":"http://127.0.0.1:8377/mcp/chat"},`+
		`"manual":{"command":"custom"}}}`)
	applyChatMCP(t, home)
	if servers := readClaudeServers(t, root); !sameJSONValue(
		servers, map[string]any{"manual": map[string]any{"command": "custom"}},
	) {
		t.Fatalf("~/.mcp.json servers=%#v, want the manual entry alone", servers)
	}
	for _, report := range InspectClaudeServers(root, home, 8377, mcpServerHarvester, chatName) {
		if report.State != MCPClientAbsent {
			t.Fatalf("doctor still classifies %s at %s as %s after install", report.Name, root, report.State)
		}
	}
}

// TestMCPDoctorLegacyStateMatchesWhatInstallRemoves pins doctor's legacy-pfm
// state to install's exact legacy shapes: an entry under a legacy key that
// differs from pfm's shape by one key or its binary path is kept by install,
// so doctor must not prescribe "run pfm install --yes" for it.
func TestMCPDoctorLegacyStateMatchesWhatInstallRemoves(t *testing.T) {
	for name, fixture := range map[string]struct{ key, entry string }{
		"http with an extra key": {
			"harvester", `{"type":"http","url":"http://127.0.0.1:8377/mcp/harvester","timeout":30}`,
		},
		"stdio chat on another binary": {
			"chat", `{"type":"stdio","command":"/opt/pfm/bin/pfm","args":["mcp","chat","serve"]}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			home := t.TempDir()
			path := filepath.Join(home, ".claude.json")
			writeFixture(t, path, `{"mcpServers":{"`+fixture.key+`":`+fixture.entry+`}}`)
			applyChatMCP(t, home)
			if _, kept := readClaudeServers(t, path)[fixture.key]; !kept {
				t.Fatalf("install removed the non-pfm %s entry %s", fixture.key, fixture.entry)
			}
			for _, report := range InspectClaudeServers(path, home, 8377, fixture.key) {
				if report.State == MCPClientLegacyPFM {
					t.Fatalf("doctor classifies %s (kept by install) as %s", fixture.entry, report.State)
				}
			}
		})
	}
}

// TestMCPInstallPreservesAForeignChatClientRegistration pins that a chat
// entry differing from pfm's legacy stdio shape — the operator's bare "pfm"
// command instead of the installer's absolute path — is not pfm's: it stays
// untouched beside the new professor registration.
func TestMCPInstallPreservesAForeignChatClientRegistration(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	foreign := `{"type":"stdio","command":"pfm","args":["mcp","chat","serve"]}`
	writeFixture(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"chat":`+foreign+`}}`)
	applyChatMCP(t, home)
	servers := readClaudeServers(t, filepath.Join(home, ".claude.json"))
	var want map[string]any
	if err := json.Unmarshal([]byte(foreign), &want); err != nil {
		t.Fatal(err)
	}
	if !sameJSONValue(servers[chatName], want) {
		t.Fatalf("lookalike chat entry changed to %#v, want untouched %s", servers[chatName], foreign)
	}
	if !sameJSONValue(servers[professorName], claudeProfessorShape(home)) {
		t.Fatalf("professor was not added beside the lookalike: %#v", servers)
	}
}

// TestMCPInstallPreservesAManualProfessorRegistration pins that a professor
// entry pfm did not write is a manual conflict: kept, named on a skip line,
// never claimed.
func TestMCPInstallPreservesAManualProfessorRegistration(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	clientPath := filepath.Join(home, ".claude.json")
	manual := `{"mcpServers":{"professor":{"type":"http","url":"http://127.0.0.1:8377/mcp/professor"}}}`
	writeFixture(t, clientPath, manual)
	applied := applyChatMCP(t, home)
	if want := "preserve conflicting manual MCP client professor in " + physicalSettingsPath(
		clientPath,
	); !strings.Contains(
		applied,
		want,
	) {
		t.Fatalf("output lacks %q:\n%s", want, applied)
	}
	if got := readFixture(t, clientPath); got != manual {
		t.Fatalf("manual professor changed=%s, want untouched %s", got, manual)
	}
}

// TestMCPInstallRecognizesAnOwnedStdioProfessorClientWithoutRewriteOrConflict
// pins that an owned professor already in the stdio shape is recognized as
// pfm's own: no rewrite, no backup, no manual-conflict skip.
func TestMCPInstallRecognizesAnOwnedStdioProfessorClientWithoutRewriteOrConflict(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	clientPath := filepath.Join(home, ".claude.json")
	owned := `{"mcpServers":{"professor":{"type":"stdio","command":"` + home +
		`/.local/bin/pfm","args":["mcp","serve","--stdio"]}}}`
	writeFixture(t, clientPath, owned)
	var existing map[string]map[string]any
	if err := json.Unmarshal([]byte(owned), &existing); err != nil {
		t.Fatal(err)
	}
	ledger, _ := json.Marshal(
		mcpOwnership{Registrations: map[string]map[string]any{clientPath: existing["mcpServers"]}},
	)
	writeFixture(t, filepath.Join(home, ".local", "share", "pfm", "install", mcpOwnershipName), string(ledger))
	applied := applyChatMCP(t, home)
	if strings.Contains(applied, "preserve conflicting manual MCP client professor") {
		t.Fatalf("an owned stdio professor registration was treated as a manual conflict:\n%s", applied)
	}
	if !strings.Contains(applied, "ok      "+physicalSettingsPath(clientPath)+" wiring") {
		t.Fatalf("an already-correct professor registration was rewritten instead of recognized:\n%s", applied)
	}
	if got := readFixture(t, clientPath); got != owned {
		t.Fatalf(".claude.json changed=%s, want byte-identical %s", got, owned)
	}
	if matches, _ := filepath.Glob(clientPath + ".pre-professor-*"); len(matches) != 0 {
		t.Fatalf("a spurious backup was written for an unchanged, already-owned registration: %v", matches)
	}
}

// TestMCPInstallRegistersTheStdioProfessorInAFreshCodexHome pins the one
// transport law on Codex: one fence at the end of the file holding the stdio
// command and args, and no url line.
func TestMCPInstallRegistersTheStdioProfessorInAFreshCodexHome(t *testing.T) {
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

// TestInstallRegistersMCPServersInEveryRegistryAPFMLaunchedClaudeReads pins
// issue #24 finding 5 under the one professor server: every registry a
// pfm-launched Claude reads — the implicit account's $HOME/.claude.json, a
// second account's, and the ambient CLAUDE_CONFIG_DIR's — gains professor,
// loses pfm's legacy entries, and the ledger owns professor in each.
func TestInstallRegistersMCPServersInEveryRegistryAPFMLaunchedClaudeReads(t *testing.T) {
	home := t.TempDir()
	canonical := filepath.Join(home, ".claude")
	writeFixture(t, filepath.Join(canonical, "settings.json"), `{}`)
	second := filepath.Join(home, "account-two")
	ambient := filepath.Join(home, ".cc", "1")
	t.Setenv("CLAUDE_CONFIG_DIR", ambient)

	accounts := []pfmconfig.Account{{ID: 1, ConfigDir: canonical, Implicit: true}, {ID: 2, ConfigDir: second}}
	resolved := ClaudeUserRegistries(home, accounts, pfmconfig.AmbientClaudeConfigDir())
	claudeRegistries := make([]string, 0, len(resolved))
	reasons := make(map[string]string, len(resolved))
	for _, registry := range resolved {
		claudeRegistries = append(claudeRegistries, registry.Path)
		reasons[registry.Path] = registry.Reason
	}
	paths := []string{
		filepath.Join(home, ".claude.json"),
		filepath.Join(second, ".claude.json"),
		filepath.Join(ambient, ".claude.json"),
	}
	for _, path := range paths {
		writeFixture(t, path, `{"mcpServers":{"chat":{"type":"http","url":"http://127.0.0.1:8377/mcp/chat"},`+
			`"harvester":{"type":"http","url":"http://127.0.0.1:8377/mcp/harvester"}}}`)
	}

	options := Options{
		Mode: ModeApply, Home: home, ConfigDir: canonical,
		ConfigDirs: []string{canonical}, ClaudeRegistries: claudeRegistries, ClaudeRegistryReasons: reasons,
		MCPEnabled: map[string]bool{"chat": true, "harvester": true}, MCPPort: 8377,
		Runner: &fakeRunner{}, Stdout: io.Discard,
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}

	ledger, err := readMCPOwnership(filepath.Join(home, ".local", "share", "pfm", "install", mcpOwnershipName))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		servers := readClaudeServers(t, path)
		if len(servers) != 1 || !sameJSONValue(servers[professorName], claudeProfessorShape(home)) {
			t.Fatalf("registry %s servers=%#v, want professor alone", path, servers)
		}
		owned := ledger.Registrations[physicalSettingsPath(path)]
		if len(owned) != 1 || owned[professorName] == nil {
			t.Fatalf("ledger does not own professor alone in %s: %#v", path, ledger.Registrations)
		}
	}
}

func TestInspectHarvesterClientCutoverNamesPFMLegacyStandaloneAndUnreadableStates(t *testing.T) {
	home := t.TempDir()
	writeFixture(
		t,
		filepath.Join(home, ".claude.json"),
		`{"mcpServers":{"harvester":{"type":"http","url":"http://127.0.0.1:8377/mcp/harvester"}}}`,
	)
	writeFixture(
		t,
		filepath.Join(home, ".codex", "config.toml"),
		"[mcp_servers.harvester]\ncommand = \"uv\"\nargs = [\"--directory\", \"/fixture/harvester\", \"run\", \"harvester\"]\n",
	)

	registries := []string{filepath.Join(home, ".claude.json")}
	reports := InspectHarvesterClientCutover(home, 8377, registries, nil)
	if len(reports) != 3 || reports[0].Client != "claude" || reports[0].State != MCPClientLegacyPFM ||
		reports[0].Error != nil {
		t.Fatalf("Claude cutover report=%#v, want pfm legacy route", reports)
	}
	if reports[1].Client != "codex" || reports[1].State != MCPClientLegacyStandalone || reports[1].Error != nil {
		t.Fatalf("Codex cutover report=%#v, want legacy standalone route", reports)
	}

	writeFixture(t, filepath.Join(home, ".codex", "config.toml"), "broken = [\n")
	reports = InspectHarvesterClientCutover(home, 8377, registries, nil)
	if reports[1].State != MCPClientUnreadable || reports[1].Error == nil ||
		!strings.Contains(reports[1].Error.Error(), "config.toml") {
		t.Fatalf("Codex unreadable report=%#v, want path-bearing parse error", reports[1])
	}
}
