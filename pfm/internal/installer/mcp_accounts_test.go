package installer

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestMCPWiresCodexAndOpenCodeWithoutClaudeRegistry(t *testing.T) {
	home := t.TempDir()
	registry := filepath.Join(home, ".claude.json")
	original := `{"mcpServers":{"other":{"command":"operator"}}}`
	writeFixture(t, registry, original)
	codex := filepath.Join(home, ".codex")
	opencode := OpenCodeConfigPath(home)
	e := engine{
		options: Options{
			Home: home, CodexHomes: []string{codex}, OpenCodeConfigPath: opencode,
			MCPEnabled: map[string]bool{"chat": true}, MCPPort: 8377, Stdout: io.Discard,
		},
		apply: true, managedRoot: managedRootForHome(home), stamp: "fixture",
	}
	if err := e.wireMCP(); err != nil {
		t.Fatal(err)
	}
	if got := readFixture(t, registry); got != original {
		t.Fatalf("Claude registry changed: %s", got)
	}
	for _, path := range []string{filepath.Join(codex, "config.toml"), opencode} {
		content, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(content), "chat-professor") {
			t.Fatalf("%s lacks the professor MCP server: %s err=%v", path, content, err)
		}
	}
}

// One unparseable Codex home costs only itself: every other Codex home is
// still wired.
func TestWireMCPFailingCodexHomeCostsOnlyItself(t *testing.T) {
	home := t.TempDir()
	broken, wired := filepath.Join(home, "codex-broken"), filepath.Join(home, "codex-wired")
	writeFixture(t, filepath.Join(broken, "config.toml"), "broken = [")
	var stdout bytes.Buffer
	e := engine{
		options: Options{
			Home: home, CodexHomes: []string{broken, wired}, MCPEnabled: map[string]bool{"chat": true},
			MCPPort: 8377, Stdout: &stdout,
		},
		apply: true, managedRoot: managedRootForHome(home), stamp: "fixture",
	}
	err := e.wireMCP()
	wantErr := "read Codex MCP config " + filepath.Join(broken, "config.toml") +
		": parse unmanaged Codex MCP config: toml: line 1 (last key \"broken\"): unexpected EOF; expected value"
	if err == nil || err.Error() != wantErr {
		t.Fatalf("wireMCP = %v, want %q", err, wantErr)
	}
	want := mcpFenceBegin + "\n[mcp_servers.chat-professor]\ncommand = \"" + home + "/.local/bin/pfm\"\n" +
		"args = [\"mcp\", \"serve\", \"--stdio\", \"--family\", \"chat\"]\n" + mcpFenceEnd + "\n"
	if got := readFixture(t, filepath.Join(wired, "config.toml")); got != want {
		t.Fatalf("%s was not wired past the failure:\n%s\nwant\n%s", wired, got, want)
	}
}

func TestClaudeUserRegistriesIncludeTheAmbientConfigDirTheLauncherPassesThrough(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	accountDir := pfmconfig.DefaultAccountDir(home, 1)
	accounts := []pfmconfig.Account{{ID: 1, ConfigDir: accountDir}}
	for _, ambient := range []string{accountDir, filepath.Join(home, "ambient")} {
		registries := ClaudeUserRegistries(home, accounts, ambient)
		count := 1
		if ambient != accountDir {
			count = 2
		}
		if len(registries) != count {
			t.Fatalf("registries=%#v, want %d", registries, count)
		}
		wantPath := filepath.Join(accountDir, ".claude.json")
		wantReason := "account 1 (CLAUDE_CONFIG_DIR=" + accountDir + " when pfm spawns it)"
		if registries[0].Path != wantPath || registries[0].Reason != wantReason || registries[0].Account != 1 {
			t.Fatalf("account registry=%#v, want path=%s reason=%q account=1", registries[0], wantPath, wantReason)
		}
		if ambient != accountDir {
			wantReason = "ambient CLAUDE_CONFIG_DIR=" + ambient + " (the claude launcher passes it through — internal_launch.go)"
			if registries[1].Path != filepath.Join(ambient, ".claude.json") || registries[1].Reason != wantReason ||
				registries[1].Account != 0 {
				t.Fatalf("ambient registry=%#v, want dir=%s reason=%q account=0", registries[1], ambient, wantReason)
			}
		}
	}
}

func TestMCPFamilyRegistrationsPreserveForeignEntries(t *testing.T) {
	for _, chat := range []bool{false, true} {
		t.Run(fmt.Sprintf("chat=%t", chat), func(t *testing.T) {
			home := t.TempDir()
			codex := filepath.Join(home, ".codex")
			openCode := OpenCodeConfigPath(home)
			bin := filepath.Join(home, ".local", "bin", "pfm")
			writeFixture(t, filepath.Join(codex, "config.toml"), "[mcp_servers.other]\ncommand = \"operator\"\n")
			writeFixture(t, openCode, `{"mcp":{"other":{"type":"local","command":["operator"],"enabled":true}}}`)
			e := engine{
				options: Options{
					Home:               home,
					CodexHomes:         []string{codex},
					OpenCodeConfigPath: openCode,
					MCPEnabled:         map[string]bool{"chat": chat},
					MCPPort:            8377,
					Stdout:             io.Discard,
				},
				apply:       true,
				managedRoot: managedRootForHome(home),
				stamp:       "fixture",
			}
			if err := e.wireMCP(); err != nil {
				t.Fatal(err)
			}
			var cx struct {
				Servers map[string]struct {
					Command string
					Args    []string
				} `toml:"mcp_servers"`
			}
			if _, err := toml.DecodeFile(filepath.Join(codex, "config.toml"), &cx); err != nil {
				t.Fatal(err)
			}
			oc, err := decodeJSONCObject([]byte(readFixture(t, openCode)))
			if err != nil {
				t.Fatal(err)
			}
			servers, _ := oc["mcp"].(map[string]any)
			expected := 1
			for family, on := range map[string]bool{"chat": chat} {
				if !on {
					continue
				}
				expected++
				name := family + "-professor"
				want := []string{"mcp", "serve", "--stdio", "--family", family}
				wantBin := bin
				if cx.Servers[name].Command != wantBin || !reflect.DeepEqual(cx.Servers[name].Args, want) {
					t.Fatalf("Codex %s = %+v", name, cx.Servers[name])
				}
				registration, ok := servers[name].(map[string]any)
				wantCommand := []any{wantBin}
				for _, arg := range want {
					wantCommand = append(wantCommand, arg)
				}
				if !ok || !reflect.DeepEqual(registration["command"], wantCommand) {
					t.Fatalf("OpenCode %s = %+v", name, registration)
				}
			}
			if len(cx.Servers) != expected || len(servers) != expected {
				t.Fatalf("installed servers Codex=%+v OpenCode=%+v, want %d", cx.Servers, servers, expected)
			}
			if cx.Servers["other"].Command != "operator" ||
				!reflect.DeepEqual(servers["other"], map[string]any{
					"type": "local", "command": []any{"operator"}, "enabled": true,
				}) {
				t.Fatalf("foreign entry changed: Codex=%+v OpenCode=%+v", cx.Servers["other"], servers["other"])
			}
		})
	}
}
