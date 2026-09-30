package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func TestInstallYieldsProfessorFenceToManualCodexTable(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "config.toml")
	manual := "[mcp_servers.professor]\nurl = \"http://127.0.0.1:1/lane-m-foreign\"\n"
	writeFixture(t, path, codexProfessorFence(home)+manual)
	options := Options{
		MCPConfigPath: testConfigPath(t), MCPEnabled: map[string]bool{chatName: true},
		Mode: ModeDryRun, Home: home, Runner: &fakeRunner{nameSyncIdle: true},
	}
	var output bytes.Buffer
	options.Stdout = &output
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatalf("dry run: %v\n%s", err, output.String())
	}
	if got := readFixture(t, path); got != codexProfessorFence(home)+manual {
		t.Fatalf("dry run changed config: %q", got)
	}
	options.Mode = ModeApply
	output.Reset()
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatalf("apply: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "preserve conflicting manual MCP client professor in "+path) {
		t.Fatalf("missing preserve report:\n%s", output.String())
	}
	got := readFixture(t, path)
	if !strings.Contains(got, manual) || strings.Contains(got, mcpFenceBegin) || strings.Contains(got, mcpFenceEnd) {
		t.Fatalf("manual professor table was not kept without pfm's fence:\n%s", got)
	}
	var document map[string]any
	if _, err := toml.Decode(got, &document); err != nil {
		t.Fatalf("repaired config is not TOML: %v", err)
	}
	output.Reset()
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatalf("second apply: %v\n%s", err, output.String())
	}
	for _, line := range strings.Split(output.String(), "\n") {
		if strings.Contains(line, "change") && strings.Contains(line, path) {
			t.Fatalf("second apply rewrote Codex config:\n%s", output.String())
		}
	}
}

func TestInstallRejectsUnrelatedCodexTOMLDuplicate(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "config.toml")
	broken := "model = 'first'\nmodel = 'second'\n" + codexProfessorFence(home)
	writeFixture(t, path, broken)
	_, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t), MCPEnabled: map[string]bool{chatName: true},
		Mode: ModeDryRun, Home: home, Runner: &fakeRunner{nameSyncIdle: true},
	})
	if err == nil || !strings.Contains(err.Error(), "toml:") || !strings.Contains(err.Error(), "model") {
		t.Fatalf("error=%v, want unrelated TOML parse refusal", err)
	}
	if got := readFixture(t, path); got != broken {
		t.Fatalf("refused install changed config: %q", got)
	}
}

func TestMCPPreservesManualSecondaryCodexClient(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

// TestMCPReclaimsAnOrphanedCodexProfessorBody pins the fence whose END line
// was deleted by hand: the professor body pfm wrote is still byte-exact pfm
// shape, so install removes it and writes one fenced table (no manual-conflict
// skip), and uninstall over the same orphan leaves no professor table.
func TestMCPReclaimsAnOrphanedCodexProfessorBody(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	account := filepath.Join(home, ".codex")
	path := filepath.Join(account, "config.toml")
	head := "model = \"personal\"\n"
	tail := "[desktop]\ndock-icon-preference = \"app-default\"\n"
	orphan := head + strings.TrimSuffix(codexProfessorFence(home), mcpFenceEnd+"\n") + "\n" + tail
	writeFixture(t, path, orphan)
	var out strings.Builder
	e := codexMCPEngine(home, account)
	e.options.Stdout = &out
	if err := e.writeMCPCodeConfig([]string{professorName}); err != nil {
		t.Fatal(err)
	}
	if want := head + tail + codexProfessorFence(home); readFixture(t, path) != want {
		t.Fatalf("installed Codex config:\n%s\nwant:\n%s", readFixture(t, path), want)
	}
	if strings.Contains(out.String(), "preserve conflicting manual MCP client") {
		t.Fatalf("install called pfm's orphaned professor body a manual conflict:\n%s", out.String())
	}
	writeFixture(t, path, orphan)
	if err := e.removeMCPCodeConfig(); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, path); got != head+tail {
		t.Fatalf("uninstall left:\n%s\nwant:\n%s", got, head+tail)
	}
}

// TestMCPKeepsALegacyCodexTableWithAnExtraKey pins that a [mcp_servers.harvester]
// table differing from pfm's legacy shape by one key is not pfm's and stays.
func TestMCPKeepsALegacyCodexTableWithAnExtraKey(t *testing.T) {
	t.Parallel()
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
	if report := InspectCodexServers(path, home, 18377, mcpServerHarvester)[0]; report.State != MCPClientForeignRegistration {
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
	t.Parallel()
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
	if report := InspectCodexServers(path, home, 18377, mcpServerHarvester)[0]; report.State != MCPClientForeignRegistration {
		t.Fatalf(
			"doctor classifies the headed harvester table as %s, want %s",
			report.State,
			MCPClientForeignRegistration,
		)
	}
}

// TestMCPRemovalClearsProfessorAndPFMLegacyEntriesEverywhere pins both ways
// pfm takes its registrations back — every family disabled, and uninstall:
// the owned professor and every legacy shape leave Codex and OpenCode, the
// fence and orphan markers included, and nothing else moves. Claude has no
// install-time registration to take back (docs/design/engines/host-migration.md).
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
				Runner: &fakeRunner{}, Stdout: io.Discard, MCPConfigPath: testConfigPath(t),
			}
			if _, err := Run(context.Background(), options); err != nil {
				t.Fatal(err)
			}
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

func TestRetirementPreservesSecondaryPersonalAgentLink(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
