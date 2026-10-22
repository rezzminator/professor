package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestInstallHarvesterWithoutClaude(t *testing.T) {
	for _, version := range []string{"1.0.0-alpha", "1.0.0"} {
		for _, client := range []string{"codex", "opencode"} {
			for _, legacy := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/legacy=%t", version, client, legacy), func(t *testing.T) {
					home := t.TempDir()
					clone := writeMarketplaceClone(t, []string{"harvester"}, []string{"harvester"})
					checkout := filepath.Join(clone, "plugins", "harvester", "plugins", "harvester")
					writeFixture(t, filepath.Join(checkout, ".claude-plugin", "plugin.json"),
						`{"name":"harvester","version":"0.1.0"}`)
					config := filepath.Join(t.TempDir(), "pfm.config.json")
					writeFixture(t, config, `{"version":2,"claude":{"plugins":["buddy"]}}`)
					if legacy {
						writeFixture(t, filepath.Join(filepath.Dir(config), harvesterConfigFileName),
							`{"enabled":true,"external":{"enabled":true,"passphrase":"secret"}}`)
					}
					runner := &goRunner{output: "standalone plugin"}
					fixture := &harvesterReleaseFixture{binary: []byte("standalone plugin")}
					var output bytes.Buffer
					inst := &engine{options: Options{
						Home: home, SourceRepo: clone, Version: version,
						Env: &paths.MapEnv{}, Stdout: &output,
						ProcessRunner: runner, Runner: &fakeRunner{manager: true, mcpState: "inactive"},
						MCPEnabled: map[string]bool{"harvester": true}, ClaudePlugins: bareList("buddy"),
						MCPConfigPath: config, GoPluginHTTPClient: &http.Client{Transport: fixture},
						CodexHomes: []string{},
						ClaudeEnv:  map[string]string{"HARVESTER_HOME": filepath.Join(home, "unrelated-claude-home")},
					}, managedRoot: filepath.Join(home, ".local", "share", "pfm", "install"), apply: true}
					if !strings.Contains(version, "alpha") {
						inst.options.SourceRepo = ""
					}
					if client == "codex" {
						inst.options.CodexHomes = []string{filepath.Join(home, "cx-one"), filepath.Join(home, "cx-two")}
					} else {
						inst.options.OpenCodeConfigPath = OpenCodeConfigPath(home)
					}
					runner.onRun = func(_, args string) {
						if args != "setup" {
							t.Fatalf("unexpected plugin command %q", args)
						}
						if !callmeterExecutable(mcpFamilyBinary(home, "harvester")) {
							t.Fatal("setup ran before PATH binary was ready")
						}
						if legacy &&
							!strings.Contains(readFixture(t, harvesterConfigPath(&paths.MapEnv{}, home)), "secret") {
							t.Fatal("setup ran before config migration")
						}
						for _, codexHome := range inst.options.CodexHomes {
							if _, err := os.Stat(filepath.Join(codexHome, "config.toml")); !os.IsNotExist(err) {
								t.Fatal("Codex registration existed before setup")
							}
						}
						if client == "opencode" {
							if _, err := os.Stat(inst.options.OpenCodeConfigPath); !os.IsNotExist(err) {
								t.Fatal("OpenCode registration existed before setup")
							}
						}
					}
					if err := inst.installHarvester(context.Background()); err != nil {
						t.Fatalf(
							"enabled harvester must install without any Claude account, plugin install or cache: %v",
							err,
						)
					}
					if !callmeterExecutable(mcpFamilyBinary(home, "harvester")) {
						t.Fatal("harvester PATH binary is missing")
					}
					if err := inst.wireMCP(); err != nil {
						t.Fatal(err)
					}
					for _, codexHome := range inst.options.CodexHomes {
						var document struct {
							Servers map[string]struct {
								Command string
								Args    []string
							} `toml:"mcp_servers"`
						}
						if _, err := toml.DecodeFile(filepath.Join(codexHome, "config.toml"), &document); err != nil {
							t.Fatal(err)
						}
						registration := document.Servers["harvester-professor"]
						if registration.Command != mcpFamilyBinary(home, "harvester") ||
							!reflect.DeepEqual(registration.Args, []string{"mcp"}) {
							t.Fatalf("Codex registration=%+v", registration)
						}
					}
					if client == "opencode" {
						var document struct {
							MCP map[string]struct{ Command []string }
						}
						if err := json.Unmarshal(
							[]byte(readFixture(t, inst.options.OpenCodeConfigPath)),
							&document,
						); err != nil {
							t.Fatal(err)
						}
						if !reflect.DeepEqual(document.MCP["harvester-professor"].Command,
							[]string{mcpFamilyBinary(home, "harvester"), "mcp"}) {
							t.Fatalf("OpenCode registration=%+v", document.MCP)
						}
					}
					if len(runner.calls) != 1 || runner.calls[0].argv != "setup" {
						t.Fatalf("plugin calls=%+v", runner.calls)
					}
					if strings.Contains(version, "alpha") {
						if len(runner.builds) != 1 || len(fixture.calls) != 0 {
							t.Fatalf("alpha builds=%d HTTP=%v", len(runner.builds), fixture.calls)
						}
					} else if len(runner.builds) != 0 || len(fixture.calls) != 3 {
						t.Fatalf("stable builds=%d HTTP=%v", len(runner.builds), fixture.calls)
					}
					for _, absent := range []string{".claude", ".claude.json"} {
						if _, err := os.Lstat(filepath.Join(home, absent)); !os.IsNotExist(err) {
							t.Fatalf("no-Claude install created %s: %v", absent, err)
						}
					}
				})
			}
		}
	}
}
