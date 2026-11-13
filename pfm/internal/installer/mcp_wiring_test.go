package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/servicemanager"
)

const chatRegistrationName = "chat-professor"

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
	wantErr := "config for Codex is a dangling symlink: " + codexConfig + "\nMCP config is a dangling symlink: " + codexConfig
	if err := errors.Join(
		append([]error{installer.install(context.Background())}, installer.deferred...)...); err == nil ||
		err.Error() != wantErr {
		t.Fatalf("install error = %v, want the retained deferred wireMCP failure %q", err, wantErr)
	}
	wantCalls := []string{
		"systemctl --user show-environment",
		"systemctl --user show-environment",
		"systemctl --user daemon-reload",
		"systemctl --user enable --now pfm-name-sync.path pfm-name-sync.timer pfm-schedule.timer",
		"systemctl --user enable --now pfm-mcp.service",
		"systemctl --user show --property=ActiveState --value pfm-schedule.timer",
		"systemctl --user show-environment",
		"systemctl --user restart pfm-mcp.service",
		"systemctl --user show --property=ActiveState --value pfm-mcp.service",
	}
	if !slices.Equal(runner.calls, wantCalls) {
		t.Fatalf("wireMCP failure calls =\n%q\nwant the running daemon restarted\n%q", runner.calls, wantCalls)
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
	ownership := filepath.Join(managedRootForHome(home), "mcp-ownership.json")
	want := "  change  write " + ownership + "\n  change  create " + physicalSettingsPath(path) +
		"\n  change  write " + ownership + "\n"
	if output.String() != want {
		t.Fatalf("fresh OpenCode registration output = %q, want %q", output.String(), want)
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
	want := []string{
		mcpFenceBegin,
		"[mcp_servers.chat-professor]",
		`command = "` + home + `/.local/bin/pfm"`,
		`args = ["mcp", "serve", "--stdio", "--family", "chat"]`,
		mcpFenceEnd,
	}
	lines := strings.Split(strings.TrimSuffix(codexConfig, "\n"), "\n")
	if !slices.Equal(lines[max(len(lines)-len(want), 0):], want) {
		t.Fatalf("Codex config does not end in the professor fence %q:\n%s", want, codexConfig)
	}
	wantOutputLines(t, codexConfig, want, "pfm mcp_servers", "chat-professor", "/.local/bin/pfm", "args = ", "url = ")
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
