package installer

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestMCPWiresActualClaudeRegistriesAndHonorsEmptyCodex(t *testing.T) {
	home := t.TempDir()
	primary := filepath.Join(home, ".claude")
	secondary := filepath.Join(home, "account-two")
	paths := []string{filepath.Join(home, ".claude.json"), filepath.Join(secondary, ".claude.json")}
	for _, path := range paths {
		writeFixture(
			t,
			path,
			`{"oauthAccount":{"accountUuid":"private"},"mcpServers":{"foreign":{"command":"custom"}}}`,
		)
	}
	options := Options{
		Home:       home,
		ConfigDir:  primary,
		ConfigDirs: []string{primary, secondary},
		CodexHomes: []string{},
		Mode:       ModeApply,
		Runner:     &fakeRunner{},
		Stdout:     io.Discard,
		MCPEnabled: map[string]bool{"chat": true},
		MCPPort:    8377,
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		var doc map[string]any
		if err := json.Unmarshal([]byte(readFixture(t, path)), &doc); err != nil {
			t.Fatal(err)
		}
		servers := doc["mcpServers"].(map[string]any)
		if servers[professorName] == nil || servers["foreign"] == nil || doc["oauthAccount"] == nil {
			t.Errorf("registry %s lost wiring or private state: %#v", path, doc)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".codex")); !os.IsNotExist(err) {
		t.Errorf("empty Codex roster wrote .codex: %v", err)
	}
	// A user replacement after installation is preserved on uninstall.
	replacement := `{"oauthAccount":{"accountUuid":"private"},"mcpServers":{"professor":{"command":"manual"},"foreign":{"command":"custom"}}}`
	writeFixture(t, paths[1], replacement)
	options.Mode = ModeUninstall
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readFixture(t, paths[0]), `"professor"`) {
		t.Error("owned primary registration survived uninstall")
	}
	if got := readFixture(t, paths[1]); got != replacement {
		t.Errorf("manual replacement was changed: %s", got)
	}
}

// TestClaudeUserRegistriesIncludeTheAmbientConfigDirTheLauncherPassesThrough
// pins issue #24 finding 5: a `claude` typed into a shell that exports
// CLAUDE_CONFIG_DIR reads THAT directory's .claude.json, not the implicit
// account's $HOME/.claude.json a bare account-driven fanout would assume —
// the launcher shim passes the ambient var straight through
// (internal_launch.go), so the registry resolver must list both files, each
// naming why it is a registry pfm cares about.
func TestClaudeUserRegistriesIncludeTheAmbientConfigDirTheLauncherPassesThrough(t *testing.T) {
	home := t.TempDir()
	ambient := filepath.Join(home, ".cc", "1")
	accounts := []pfmconfig.Account{{ID: 1, ConfigDir: ambient, Implicit: true}}

	registries := ClaudeUserRegistries(home, accounts, ambient)

	if len(registries) != 2 {
		t.Fatalf("registries=%#v, want exactly 2 (implicit account + ambient)", registries)
	}
	implicitPath := filepath.Join(home, ".claude.json")
	if registries[0].Path != implicitPath {
		t.Fatalf("registries[0].Path=%s, want the implicit account's %s", registries[0].Path, implicitPath)
	}
	if registries[0].Reason != "account 1 (pfm spawns it without CLAUDE_CONFIG_DIR)" {
		t.Fatalf("registries[0].Reason=%q, want the implicit-account reason", registries[0].Reason)
	}
	ambientPath := filepath.Join(ambient, ".claude.json")
	if registries[1].Path != ambientPath {
		t.Fatalf("registries[1].Path=%s, want the ambient CLAUDE_CONFIG_DIR file %s", registries[1].Path, ambientPath)
	}
	wantReason := "ambient CLAUDE_CONFIG_DIR=" + ambient + " (the claude launcher passes it through — internal_launch.go)"
	if registries[1].Reason != wantReason {
		t.Fatalf("registries[1].Reason=%q, want %q", registries[1].Reason, wantReason)
	}
}

// TestWriteMCPClientJSONRefusesAnUnreadableOwnershipLedger pins the honesty
// rule on the one ledger that says which MCP registrations are pfm's to
// remove: a ledger that cannot be decoded — including the zero-byte file a
// truncated write leaves — is an error naming it, never the empty ownership
// that would silently disown every registration pfm has to clean up. The
// inline reader this call site used to carry treated a zero-byte ledger as
// "nothing owned"; it now shares loadMCPOwnership with the rest of the
// package.
func TestWriteMCPClientJSONRefusesAnUnreadableOwnershipLedger(t *testing.T) {
	for name, ledger := range map[string]string{"empty": "", "malformed": "{not json"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			installer := &engine{
				options: Options{
					Home: home, ConfigDir: filepath.Join(home, ".claude"), CodexHomes: []string{},
					Mode: ModeApply, Stdout: io.Discard, MCPEnabled: map[string]bool{"chat": true}, MCPPort: 8377,
				},
				managedRoot: managedRootForHome(home),
				apply:       true,
			}
			writeFixture(t, installer.mcpOwnershipPath(), ledger)
			if _, err := installer.writeMCPClientJSON([]string{professorName}); err == nil {
				t.Fatal("an undecodable MCP ownership ledger read as an empty ownership")
			} else if !strings.Contains(err.Error(), "MCP ownership") {
				t.Fatalf("error did not name the MCP ownership ledger: %v", err)
			}
			if _, err := os.Stat(filepath.Join(home, ".claude.json")); !os.IsNotExist(err) {
				t.Fatalf("a registry was written past an unreadable ledger: %v", err)
			}
		})
	}
}

// TestMCPInstallTreatsClaudesEmptyEnvAsShapeNeutral pins the live-host
// defects: Claude Code adds `"env": {}` when it rewrites its config, so an
// empty env must never make pfm's own entry foreign — the legacy chat still
// goes, the owned professor is neither rewritten nor a conflict — while a
// non-empty env stays foreign; and $HOME/.claude.json, the registry a plain
// `claude` reads, is swept of pfm's legacy entries even when every account has
// its own ConfigDir, gaining no professor.
func TestMCPInstallTreatsClaudesEmptyEnvAsShapeNeutral(t *testing.T) {
	stdioChat := `"chat":{"type":"stdio","command":"BIN","args":["mcp","chat","serve"],"env":ENV}`
	professor := `"professor":{"type":"stdio","command":"BIN","args":["mcp","serve","--stdio"]}`
	for _, testCase := range []struct {
		name, configDir, servers, wantServers, wantLine, forbidLine string
		ownProfessor, unchanged                                     bool
		wantStates                                                  map[string]string
	}{
		{
			name: "legacy stdio chat with an empty env is removed", configDir: ".claude",
			servers:     strings.ReplaceAll(stdioChat, "ENV", "{}"),
			wantServers: professor,
			wantLine:    "remove pfm's legacy MCP clients chat",
			wantStates:  map[string]string{chatName: MCPClientAbsent, professorName: MCPClientPFM},
		},
		{
			name: "legacy chat with a non-empty env is preserved as a conflict", configDir: ".claude",
			servers:     strings.ReplaceAll(stdioChat, "ENV", `{"DEBUG":"1"}`),
			wantServers: strings.ReplaceAll(stdioChat, "ENV", `{"DEBUG":"1"}`) + "," + professor,
			forbidLine:  "remove pfm's legacy MCP clients",
			wantStates:  map[string]string{chatName: MCPClientForeignRegistration, professorName: MCPClientPFM},
		},
		{
			name: "owned professor with an empty env stays pfm's", configDir: ".claude",
			servers:      strings.TrimSuffix(professor, "}") + `,"env":{}}`,
			ownProfessor: true, unchanged: true,
			forbidLine: "preserve conflicting manual MCP client",
			wantStates: map[string]string{professorName: MCPClientPFM},
		},
		{
			name: "unwired home registry loses only pfm's legacy entries", configDir: "account-one",
			servers: strings.ReplaceAll(stdioChat, "ENV", "{}") +
				`,"harvester":{"type":"http","url":"http://127.0.0.1:8377/mcp/harvester"},"foreign":{"command":"custom"}`,
			wantServers: `"foreign":{"command":"custom"}`,
			wantLine:    "remove pfm's legacy MCP clients chat,harvester",
			wantStates: map[string]string{
				chatName: MCPClientAbsent, mcpServerHarvester: MCPClientAbsent, professorName: MCPClientAbsent,
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			home := t.TempDir()
			bin := filepath.Join(home, ".local", "bin", "pfm")
			registry := filepath.Join(home, ".claude.json")
			original := `{"theme":"dark","mcpServers":{` + strings.ReplaceAll(testCase.servers, "BIN", bin) + `}}`
			writeFixture(t, registry, original)
			if testCase.ownProfessor {
				ledger, _ := json.Marshal(mcpOwnership{Registrations: map[string]map[string]any{
					registry: {professorName: claudeProfessorShape(home)},
				}})
				writeFixture(
					t,
					filepath.Join(home, ".local", "share", "pfm", "install", mcpOwnershipName),
					string(ledger),
				)
			}
			configDir := filepath.Join(home, testCase.configDir)
			writeFixture(t, filepath.Join(configDir, "settings.json"), `{}`)
			var applied strings.Builder
			if _, err := Run(context.Background(), Options{
				Mode: ModeApply, Home: home, ConfigDir: configDir,
				ConfigDirs: []string{configDir}, MCPEnabled: map[string]bool{"chat": true},
				MCPPort: 8377, Runner: &fakeRunner{}, Stdout: &applied,
			}); err != nil {
				t.Fatalf("apply: %v\n%s", err, applied.String())
			}
			output := applied.String()
			if testCase.wantLine != "" && !strings.Contains(output, testCase.wantLine) {
				t.Errorf("install output does not name %q:\n%s", testCase.wantLine, output)
			}
			if testCase.forbidLine != "" && strings.Contains(output, testCase.forbidLine) {
				t.Errorf("install output names %q:\n%s", testCase.forbidLine, output)
			}
			got := readFixture(t, registry)
			if testCase.unchanged {
				if got != original {
					t.Errorf("registry rewritten to %s, want byte-identical %s", got, original)
				}
			} else {
				var want, document map[string]any
				wantJSON := `{"theme":"dark","mcpServers":{` + strings.ReplaceAll(
					testCase.wantServers,
					"BIN",
					bin,
				) + `}}`
				if err := json.Unmarshal([]byte(wantJSON), &want); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(got), &document); err != nil {
					t.Fatal(err)
				}
				if !sameJSONValue(document, want) {
					t.Errorf("registry=%s, want %s", got, wantJSON)
				}
			}
			for name, state := range testCase.wantStates {
				for _, report := range InspectClaudeServers(registry, home, 8377, name) {
					if report.State != state {
						t.Errorf("doctor classifies %s as %s, want %s", name, report.State, state)
					}
				}
			}
		})
	}
}
