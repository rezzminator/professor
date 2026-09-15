package installer

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "hostops/pfm/internal/config"
)

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
// install's default client wiring (required test #10 for the identity-blind
// "chat" transport fix). It used to hard-assert an HTTP URL for "chat" — that
// was the very defect this wave fixes (a self-addressed chat_* call over the
// shared HTTP daemon can never carry _meta.threadId, so it always failed);
// the observable contract changed on purpose, and harvester is now enabled
// alongside chat so both the new stdio shape and the unchanged HTTP shape are
// pinned in the same run.
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
	// Claude's "chat" server gets the stdio shape (regression test #10 for the
	// task B fix): a self-addressed chat_* call over the shared HTTP daemon
	// can never carry a caller identity, so a fresh install must not wire
	// Claude to it by default. Every other server (harvester) keeps the HTTP
	// shape unchanged — asserted immediately below.
	servers, _ := clients["mcpServers"].(map[string]any)
	chatEntry, _ := servers["chat"].(map[string]any)
	if chatEntry["type"] != "stdio" || chatEntry["command"] != home+"/.local/bin/pfm" {
		t.Fatalf("chat client registration is not the stdio shape: %#v", chatEntry)
	}
	if args, ok := chatEntry["args"].([]any); !ok || len(args) != 3 ||
		args[0] != "mcp" || args[1] != "chat" || args[2] != "serve" {
		t.Fatalf("chat client stdio args=%v, want [mcp chat serve]", chatEntry["args"])
	}
	harvesterEntry, _ := servers["harvester"].(map[string]any)
	if harvesterEntry["type"] != "http" || harvesterEntry["url"] != "http://127.0.0.1:8456/mcp/harvester" {
		t.Fatalf("harvester client registration lost its HTTP shape: %#v", harvesterEntry)
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
	if len(owned) != 1 || owned["chat"] == nil {
		t.Fatalf("owned registrations=%v, want chat only", owned)
	}
	codexConfig := readFixture(t, filepath.Join(home, ".codex", "config.toml"))
	if !strings.Contains(codexConfig, "[mcp_servers.chat]") {
		t.Fatalf("Codex registration omitted owned chat client: %s", codexConfig)
	}
	if !strings.Contains(codexConfig, "[mcp_servers.harvester]") {
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
	if _, ok := servers["chat"]; ok {
		t.Fatal("uninstall retained PFM's owned chat registration")
	}
}

// TestMCPInstallMigratesAnOwnedHTTPChatClientToStdio pins required test #11:
// pfm's own PREVIOUS HTTP "chat" registration (the shape every install wrote
// before this wave) is recognized as owned and migrated to the new stdio
// shape on the next apply, rather than being frozen in place forever or
// treated as a manual conflict.
func TestMCPInstallMigratesAnOwnedHTTPChatClientToStdio(t *testing.T) {
	// Issue #24 F11: pin CLAUDE_CONFIG_DIR to empty so a host that exports it
	// ambiently cannot steer MCP registration at an extra, real
	// $CLAUDE_CONFIG_DIR/.claude.json this fixture never wrote (host-only —
	// cannot be watched failing inside a fence that does not export it).
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	canonical := filepath.Join(home, ".claude")
	writeFixture(t, filepath.Join(canonical, "settings.json"), `{}`)
	writeFixture(t, filepath.Join(home, ".mcp.json"),
		`{"mcpServers":{"chat":{"type":"http","url":"http://127.0.0.1:8377/mcp/chat"}}}`)
	writeFixture(t, filepath.Join(home, ".local", "share", "pfm", "install", mcpOwnershipName), `{"clients":["chat"]}`)
	options := Options{
		Mode: ModeApply, Home: home, ConfigDir: canonical,
		ConfigDirs: []string{canonical}, MCPEnabled: map[string]bool{"chat": true},
		MCPPort: 8377, Runner: &fakeRunner{},
	}
	var applied strings.Builder
	options.Stdout = &applied
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatalf("migrate an owned HTTP chat registration: %v\n%s", err, applied.String())
	}
	if strings.Contains(applied.String(), "preserve conflicting manual MCP client chat") {
		t.Fatalf("pfm's own previous HTTP chat registration was treated as a manual conflict:\n%s", applied.String())
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(readFixture(t, filepath.Join(home, ".claude.json"))), &document); err != nil {
		t.Fatal(err)
	}
	servers, _ := document["mcpServers"].(map[string]any)
	chatEntry, _ := servers["chat"].(map[string]any)
	if chatEntry["type"] != "stdio" || chatEntry["command"] != home+"/.local/bin/pfm" {
		t.Fatalf("owned HTTP chat client was not migrated to the stdio shape: %#v", chatEntry)
	}
}

// TestMCPInstallPreservesAForeignChatClientRegistration pins required test
// #12: a chat registration that is neither pfm's HTTP shape nor its stdio
// shape — the operator's real hand-edit from the root-cause report, a bare
// "pfm" command instead of the installer's own absolute path — stays a
// manual conflict, preserved untouched with the same skip message as every
// other foreign registration.
func TestMCPInstallPreservesAForeignChatClientRegistration(t *testing.T) {
	home := t.TempDir()
	canonical := filepath.Join(home, ".claude")
	writeFixture(t, filepath.Join(canonical, "settings.json"), `{}`)
	foreign := `{"mcpServers":{"chat":{"type":"stdio","command":"pfm","args":["mcp","chat","serve"]}}}`
	writeFixture(t, filepath.Join(home, ".claude.json"), foreign)
	options := Options{
		Mode: ModeApply, Home: home, ConfigDir: canonical,
		ConfigDirs: []string{canonical}, MCPEnabled: map[string]bool{"chat": true},
		MCPPort: 8377, Runner: &fakeRunner{},
	}
	var applied strings.Builder
	options.Stdout = &applied
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatalf("apply over a foreign chat registration: %v\n%s", err, applied.String())
	}
	if !strings.Contains(applied.String(), "preserve conflicting manual MCP client chat") {
		t.Fatalf("a bare \"pfm\" chat command was not treated as a manual conflict:\n%s", applied.String())
	}
	if got := readFixture(t, filepath.Join(home, ".claude.json")); got != foreign {
		t.Fatalf(".mcp.json chat entry changed=%s, want untouched %s", got, foreign)
	}
	if _, err := os.Stat(
		filepath.Join(home, ".local", "share", "pfm", "install", mcpOwnershipName),
	); !os.IsNotExist(
		err,
	) {
		t.Fatalf("manual registration was claimed: %v", err)
	}
}

// TestMCPInstallRecognizesAnOwnedStdioChatClientWithoutRewriteOrConflict
// pins required test #13: once a chat registration already matches the
// stdio shape a previous apply wrote, a later apply must recognize it as
// pfm's own — no spurious rewrite (no backup file, unchanged bytes) and no
// "manual conflict" skip — the whole point of isPFMStdioClient existing
// alongside isPFMHTTPClient.
func TestMCPInstallRecognizesAnOwnedStdioChatClientWithoutRewriteOrConflict(t *testing.T) {
	// Issue #24 F11: see TestMCPInstallMigratesAnOwnedHTTPChatClientToStdio.
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	canonical := filepath.Join(home, ".claude")
	writeFixture(t, filepath.Join(canonical, "settings.json"), `{}`)
	clientPath := filepath.Join(home, ".claude.json")
	owned := `{"mcpServers":{"chat":{"type":"stdio","command":"` + home + `/.local/bin/pfm","args":["mcp","chat","serve"]}}}`
	writeFixture(t, clientPath, owned)
	var existing map[string]map[string]any
	if err := json.Unmarshal([]byte(owned), &existing); err != nil {
		t.Fatal(err)
	}
	ledger, _ := json.Marshal(
		mcpOwnership{Registrations: map[string]map[string]any{clientPath: existing["mcpServers"]}},
	)
	writeFixture(t, filepath.Join(home, ".local", "share", "pfm", "install", mcpOwnershipName), string(ledger))
	options := Options{
		Mode: ModeApply, Home: home, ConfigDir: canonical,
		ConfigDirs: []string{canonical}, MCPEnabled: map[string]bool{"chat": true},
		MCPPort: 8377, Runner: &fakeRunner{},
	}
	var applied strings.Builder
	options.Stdout = &applied
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatalf("apply over an already-owned stdio chat registration: %v\n%s", err, applied.String())
	}
	if strings.Contains(applied.String(), "preserve conflicting manual MCP client chat") {
		t.Fatalf("an owned stdio chat registration was treated as a manual conflict:\n%s", applied.String())
	}
	if !strings.Contains(applied.String(), "ok      "+physicalSettingsPath(clientPath)+" wiring") {
		t.Fatalf(
			"an already-correct stdio chat registration was rewritten instead of recognized:\n%s",
			applied.String(),
		)
	}
	if got := readFixture(t, clientPath); got != owned {
		t.Fatalf(".mcp.json changed=%s, want byte-identical %s", got, owned)
	}
	if matches, _ := filepath.Glob(clientPath + ".pre-professor-*"); len(matches) != 0 {
		t.Fatalf("a spurious backup was written for an unchanged, already-owned registration: %v", matches)
	}
}

// TestMCPInstallCodexChatStaysOnHTTPDespiteClaudeStdio pins required test
// #14: the Claude/Codex asymmetry is deliberate (Codex's own MCP client
// attaches _meta.threadId on every call, so HTTP resolves callers correctly
// there), and must stay pinned on its own so a later "harmonize the two
// clients" pass does not silently regress Codex onto the costlier stdio
// transport it never needed.
func TestMCPInstallCodexChatStaysOnHTTPDespiteClaudeStdio(t *testing.T) {
	home := t.TempDir()
	canonical := filepath.Join(home, ".claude")
	writeFixture(t, filepath.Join(canonical, "settings.json"), `{}`)
	options := Options{
		Mode: ModeApply, Home: home, ConfigDir: canonical,
		ConfigDirs: []string{canonical}, MCPEnabled: map[string]bool{"chat": true},
		MCPPort: 8377, Runner: &fakeRunner{},
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	codexConfig := readFixture(t, filepath.Join(home, ".codex", "config.toml"))
	if !strings.Contains(codexConfig, "[mcp_servers.chat]") ||
		!strings.Contains(codexConfig, `url = "http://127.0.0.1:8377/mcp/chat"`) {
		t.Fatalf("Codex chat registration lost its HTTP shape:\n%s", codexConfig)
	}
	if strings.Contains(codexConfig, "command") || strings.Contains(codexConfig, "stdio") {
		t.Fatalf("Codex chat registration picked up the Claude-only stdio shape:\n%s", codexConfig)
	}
	clientJSON := readFixture(t, filepath.Join(home, ".claude.json"))
	if !strings.Contains(clientJSON, `"type": "stdio"`) {
		t.Fatalf("Claude chat registration is not stdio, so this test cannot pin the asymmetry:\n%s", clientJSON)
	}
}

// TestInstallRegistersMCPServersInEveryRegistryAPFMLaunchedClaudeReads pins
// issue #24 finding 5: on a host whose shell exports CLAUDE_CONFIG_DIR, an
// install driven by ClaudeUserRegistries must wire chat and harvester into
// BOTH the implicit account's $HOME/.claude.json AND the ambient
// CLAUDE_CONFIG_DIR's .claude.json — not just the one the pre-M5 resolver
// assumed — and the ownership ledger must own both.
func TestInstallRegistersMCPServersInEveryRegistryAPFMLaunchedClaudeReads(t *testing.T) {
	home := t.TempDir()
	canonical := filepath.Join(home, ".claude")
	writeFixture(t, filepath.Join(canonical, "settings.json"), `{}`)
	ambient := filepath.Join(home, ".cc", "1")
	t.Setenv("CLAUDE_CONFIG_DIR", ambient)

	accounts := []pfmconfig.Account{{ID: 1, ConfigDir: canonical, Implicit: true}}
	resolved := ClaudeUserRegistries(home, accounts, pfmconfig.AmbientClaudeConfigDir())
	claudeRegistries := make([]string, 0, len(resolved))
	reasons := make(map[string]string, len(resolved))
	for _, registry := range resolved {
		claudeRegistries = append(claudeRegistries, registry.Path)
		reasons[registry.Path] = registry.Reason
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

	implicitPath := filepath.Join(home, ".claude.json")
	ambientPath := filepath.Join(ambient, ".claude.json")
	for _, path := range []string{implicitPath, ambientPath} {
		var doc map[string]any
		if err := json.Unmarshal([]byte(readFixture(t, path)), &doc); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		servers, ok := doc["mcpServers"].(map[string]any)
		if !ok || servers["chat"] == nil || servers["harvester"] == nil {
			t.Fatalf("registry %s did not receive chat+harvester: %#v", path, doc)
		}
	}

	var ledger mcpOwnership
	if err := json.Unmarshal(
		[]byte(readFixture(t, filepath.Join(home, ".local", "share", "pfm", "install", mcpOwnershipName))),
		&ledger,
	); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{implicitPath, ambientPath} {
		owned, ok := ledger.Registrations[physicalSettingsPath(path)]
		if !ok || owned["chat"] == nil || owned["harvester"] == nil {
			t.Fatalf("ledger did not own chat+harvester in %s: %#v", path, ledger.Registrations)
		}
	}
}

func TestInspectHarvesterClientCutoverNamesHealthyLegacyAndUnreadableStates(t *testing.T) {
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
	if len(reports) != 3 || reports[0].Client != "claude" || reports[0].State != MCPClientPFM ||
		reports[0].Error != nil {
		t.Fatalf("Claude cutover report=%#v, want healthy PFM route", reports)
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
