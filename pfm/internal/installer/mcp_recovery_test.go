package installer

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

type registryWriteHook func([]byte) (int, error)

func (f registryWriteHook) Write(p []byte) (int, error) { return f(p) }

func TestMCPPreservesManualSecondaryCodexClient(t *testing.T) {
	home := t.TempDir()
	account := filepath.Join(home, "secondary-codex")
	path := filepath.Join(account, "config.toml")
	original := "model = \"personal\"\n[mcp_servers.harvester]\ncommand = \"manual-harvester\"\n"
	writeFixture(t, path, original)
	e := engine{
		options: Options{Home: home, CodexHomes: []string{account}, MCPPort: 8377, Stdout: io.Discard},
		apply:   true,
		stamp:   "fixture",
	}
	if err := e.writeMCPCodeConfig([]string{professorName}); err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if _, err := toml.Decode(readFixture(t, path), &document); err != nil {
		t.Fatalf("install corrupted manual secondary Codex config: %v", err)
	}
	if got := readFixture(t, path); !strings.HasPrefix(got, original) {
		t.Fatalf("manual harvester table was not kept byte-identical:\n%s", got)
	}
}

// codexHostShape is the broken host config the one-fence repair starts from:
// pfm's legacy loopback tables, a hand-written remote gateway, an orphan END
// and an empty fence at the end.
const codexHostShape = `[mcp_servers.chat]
url = "http://127.0.0.1:18377/mcp/chat"

[mcp_servers.harvester]
url = "http://127.0.0.1:18377/mcp/harvester"

[mcp_servers.harvester-remote]
url = "https://gateway.example.invalid/mcp"

[desktop]
dock-icon-preference = "app-default"

[hooks.state]
# END pfm mcp_servers — installer-owned

[features.code_mode]
default_exec_yield_time_ms = 3600000
# BEGIN pfm mcp_servers — installer-owned
# END pfm mcp_servers — installer-owned
`

const codexHostShapeKept = `[mcp_servers.harvester-remote]
url = "https://gateway.example.invalid/mcp"

[desktop]
dock-icon-preference = "app-default"

[hooks.state]

[features.code_mode]
default_exec_yield_time_ms = 3600000
`

func codexMCPEngine(home, account string) engine {
	return engine{
		options: Options{Home: home, CodexHomes: []string{account}, MCPPort: 18377, Stdout: io.Discard},
		apply:   true,
		stamp:   "fixture",
	}
}

func codexProfessorFence(home string) string {
	return mcpFenceBegin + "\n[mcp_servers.professor]\ncommand = \"" + home + "/.local/bin/pfm\"\n" +
		"args = [\"mcp\", \"serve\", \"--stdio\"]\n" + mcpFenceEnd + "\n"
}

// TestMCPRepairsABrokenCodexFence pins the host repair: the legacy loopback
// tables, the orphan END and the empty fence go, every other line stays
// byte-identical, and one professor fence lands at the end. A second pass
// changes nothing and reports the wiring ok.
func TestMCPRepairsABrokenCodexFence(t *testing.T) {
	home := t.TempDir()
	account := filepath.Join(home, ".codex")
	path := filepath.Join(account, "config.toml")
	writeFixture(t, path, codexHostShape)
	e := codexMCPEngine(home, account)
	if err := e.writeMCPCodeConfig([]string{professorName}); err != nil {
		t.Fatal(err)
	}
	want := codexHostShapeKept + codexProfessorFence(home)
	if got := readFixture(t, path); got != want {
		t.Fatalf("repaired Codex config:\n%s\nwant:\n%s", got, want)
	}
	var second strings.Builder
	e.options.Stdout = &second
	if err := e.writeMCPCodeConfig([]string{professorName}); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, path); got != want {
		t.Fatalf("second install changed the Codex config:\n%s", got)
	}
	if !strings.Contains(second.String(), "ok      "+path+" wiring") {
		t.Fatalf("second install did not report %s wiring ok:\n%s", path, second.String())
	}
}

// TestMCPKeepsALegacyCodexTableWithAnExtraKey pins that a [mcp_servers.harvester]
// table differing from pfm's legacy shape by one key is not pfm's and stays.
func TestMCPKeepsALegacyCodexTableWithAnExtraKey(t *testing.T) {
	home := t.TempDir()
	account := filepath.Join(home, ".codex")
	path := filepath.Join(account, "config.toml")
	original := "[mcp_servers.harvester]\nurl = \"http://127.0.0.1:18377/mcp/harvester\"\nstartup_timeout_sec = 30\n"
	writeFixture(t, path, original)
	e := codexMCPEngine(home, account)
	if err := e.writeMCPCodeConfig([]string{professorName}); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, path); got != original+codexProfessorFence(home) {
		t.Fatalf("Codex config:\n%s\nwant the extended harvester table kept and the professor fence appended", got)
	}
	if report := inspectCodexHarvester(path, 18377); report.State != MCPClientForeignRegistration {
		t.Fatalf("doctor classifies the extended harvester table as %s, want %s: install keeps it",
			report.State, MCPClientForeignRegistration)
	}
}

// TestMCPKeepsALegacyCodexTableWithASubTable pins that a [mcp_servers.harvester]
// table whose url line is followed by its own [mcp_servers.harvester.*]
// sub-table (the headers Codex sends to a streamable-HTTP server) is not pfm's
// single-line shape: install keeps it whole rather than stripping the parent
// and orphaning the sub-table, as doctor classifies a headed table foreign.
func TestMCPKeepsALegacyCodexTableWithASubTable(t *testing.T) {
	home := t.TempDir()
	account := filepath.Join(home, ".codex")
	path := filepath.Join(account, "config.toml")
	original := "[mcp_servers.harvester]\nurl = \"http://127.0.0.1:18377/mcp/harvester\"\n\n" +
		"[mcp_servers.harvester.http_headers]\nX-Team = \"research\"\n"
	writeFixture(t, path, original)
	e := codexMCPEngine(home, account)
	if err := e.writeMCPCodeConfig([]string{professorName}); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, path); got != original+codexProfessorFence(home) {
		t.Fatalf("Codex config:\n%s\nwant the headed harvester table kept and the professor fence appended", got)
	}
	if report := inspectCodexHarvester(path, 18377); report.State != MCPClientForeignRegistration {
		t.Fatalf(
			"doctor classifies the headed harvester table as %s, want %s",
			report.State,
			MCPClientForeignRegistration,
		)
	}
}

// TestMCPRemovalClearsProfessorAndPFMLegacyEntriesEverywhere pins both ways
// pfm takes its registrations back — every family disabled, and uninstall:
// the owned professor and every legacy shape leave Claude, Codex and
// OpenCode, the fence and orphan markers included, and nothing else moves.
func TestMCPRemovalClearsProfessorAndPFMLegacyEntriesEverywhere(t *testing.T) {
	for name, mode := range map[string]Mode{"both disabled": ModeApply, "uninstall": ModeUninstall} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("CLAUDE_CONFIG_DIR", "")
			home := t.TempDir()
			canonical := filepath.Join(home, ".claude")
			writeFixture(t, filepath.Join(canonical, "settings.json"), `{}`)
			openCodePath := OpenCodeConfigPath(home)
			options := Options{
				Mode: ModeApply, Home: home, ConfigDir: canonical, ConfigDirs: []string{canonical},
				OpenCodeConfigPath: openCodePath, MCPEnabled: map[string]bool{"chat": true}, MCPPort: 18377,
				Runner: &fakeRunner{}, Stdout: io.Discard,
			}
			if _, err := Run(context.Background(), options); err != nil {
				t.Fatal(err)
			}
			claudePath := filepath.Join(home, ".claude.json")
			var claude map[string]any
			if err := json.Unmarshal([]byte(readFixture(t, claudePath)), &claude); err != nil {
				t.Fatal(err)
			}
			servers := claude["mcpServers"].(map[string]any)
			servers["chat"] = map[string]any{"type": "http", "url": "http://127.0.0.1:18377/mcp/chat"}
			servers["harvester"] = map[string]any{"type": "http", "url": "http://127.0.0.1:18377/mcp/harvester"}
			servers["manual"] = map[string]any{"command": "custom"}
			encoded, _ := json.Marshal(claude)
			writeFixture(t, claudePath, string(encoded))
			codexPath := filepath.Join(home, ".codex", "config.toml")
			remote := "[mcp_servers.harvester-remote]\nurl = \"https://gateway.example.invalid/mcp\"\n"
			writeFixture(t, codexPath, readFixture(t, codexPath)+"\n[mcp_servers.chat]\n"+
				"url = \"http://127.0.0.1:18377/mcp/chat\"\n\n"+remote+mcpFenceEnd+"\n")
			professor, _ := json.Marshal(openCodeProfessorShape(home))
			writeFixture(t, openCodePath, `{"mcp":{"professor":`+string(professor)+
				`,"chat":{"type":"remote","url":"http://127.0.0.1:18377/mcp/chat","enabled":true}}}`)

			options.Mode = mode
			options.MCPEnabled = map[string]bool{"chat": false, "harvester": false}
			if _, err := Run(context.Background(), options); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(readFixture(t, claudePath)), &claude); err != nil {
				t.Fatal(err)
			}
			if got := claude["mcpServers"]; !sameJSONValue(
				got,
				map[string]any{"manual": map[string]any{"command": "custom"}},
			) {
				t.Fatalf("Claude servers=%#v, want the manual entry alone", got)
			}
			codex := readFixture(t, codexPath)
			if !strings.Contains(codex, remote) || strings.Contains(codex, "pfm mcp_servers") ||
				strings.Contains(codex, "mcp_servers.professor") || strings.Contains(codex, "/mcp/chat") {
				t.Fatalf("Codex config kept pfm lines:\n%s", codex)
			}
			document, err := decodeJSONCObject([]byte(readFixture(t, openCodePath)))
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := document["mcp"].(map[string]any); len(got) != 0 {
				t.Fatalf("OpenCode servers=%#v, want none", got)
			}
		})
	}
}

func TestMCPFailedRemovalKeepsOwnershipForRetry(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	managed := filepath.Join(home, "managed")
	e := engine{
		options:     Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), Stdout: io.Discard},
		managedRoot: managed,
		apply:       true,
		stamp:       "fixture",
	}
	if _, err := e.writeMCPClientJSON([]string{professorName}); err != nil {
		t.Fatal(err)
	}
	original := readFixture(t, path)
	injected := false
	e.options.Stdout = registryWriteHook(func(p []byte) (int, error) {
		if !injected && strings.Contains(string(p), "change  rewrite "+physicalSettingsPath(path)+" ") {
			injected = true
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		return len(p), nil
	})
	if _, err := e.writeMCPClientJSON(nil); err == nil {
		t.Fatal("failure injection not exercised")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, path, original)
	e.options.Stdout = io.Discard
	if _, err := e.writeMCPClientJSON(nil); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(readFixture(t, path)), &doc); err != nil {
		t.Fatal(err)
	}
	if _, exists := doc["mcpServers"].(map[string]any)[professorName]; exists {
		t.Fatal("retry could not remove owned registration: receipt was deleted before failing registry mutation")
	}
}

func TestRetirementPreservesSecondaryPersonalAgentLink(t *testing.T) {
	home := t.TempDir()
	second := filepath.Join(home, "secondary")
	personal := filepath.Join(home, "personal-agent.md")
	writeFixture(t, personal, "---\nname: personal\n---\nPrivate agent\n")
	path := filepath.Join(second, "agents", "frr.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(personal, path); err != nil {
		t.Fatal(err)
	}
	e := engine{
		options: Options{Home: home, ConfigDirs: []string{second}, CodexHomes: []string{}, Stdout: io.Discard},
		apply:   true,
	}
	if err := e.retireRenamedGlobalAgents(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("unrelated secondary personal agent link removed: %v", err)
	}
}

func TestMCPConfigSymlinkSurvivesInstallAndRemoval(t *testing.T) {
	home := t.TempDir()
	config := filepath.Join(home, "secondary", "config.toml")
	target := filepath.Join(home, "personal.toml")
	writeFixture(t, target, "model = \"personal\"\n")
	if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, config); err != nil {
		t.Fatal(err)
	}
	e := engine{
		options: Options{Home: home, CodexHomes: []string{filepath.Dir(config)}, MCPPort: 8377, Stdout: io.Discard},
		apply:   true,
		stamp:   "fixture",
	}
	if err := e.writeMCPCodeConfig([]string{professorName}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFixture(t, target), "mcp_servers.professor") {
		t.Fatal("physical target was not wired")
	}
	if err := e.removeMCPCodeConfig(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(config); err != nil || got != target {
		t.Fatalf("link=%q err=%v", got, err)
	}
	if strings.Contains(readFixture(t, target), "mcp_servers.professor") {
		t.Fatal("owned registration survived removal")
	}
}

func TestMCPRefusesConcurrentNativeRegistryUpdate(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude.json")
	writeFixture(t, path, `{"oauthAccount":{"accountUuid":"original"}}`)
	latest := `{"oauthAccount":{"accountUuid":"latest-native-login"}}`
	e := engine{
		options:     Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), Stdout: io.Discard},
		managedRoot: filepath.Join(home, "managed"),
		apply:       true,
		stamp:       "fixture",
	}
	injected := false
	e.options.Stdout = registryWriteHook(func(p []byte) (int, error) {
		if !injected && strings.Contains(string(p), "change  rewrite "+physicalSettingsPath(path)+" ") {
			injected = true
			writeFixture(t, path, latest)
		}
		return len(p), nil
	})
	if _, err := e.writeMCPClientJSON(
		[]string{professorName},
	); err == nil ||
		!strings.Contains(err.Error(), "changed while planning") {
		t.Fatalf("concurrent update error=%v", err)
	}
	if got := readFixture(t, path); got != latest {
		t.Fatalf("native state overwritten: %s", got)
	}
	e.options.Stdout = io.Discard
	if _, err := e.writeMCPClientJSON([]string{professorName}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFixture(t, path), "latest-native-login") {
		t.Fatal("retry lost native login")
	}
}
