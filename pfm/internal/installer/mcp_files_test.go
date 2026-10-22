package installer

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

var harvesterRuntimeNamesFixture = []string{
	"HARVESTER_HOME_DIR", "HARVESTER_CONFIG", "HARVESTER_PYTHON_ROOT",
	"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME",
	"HARVESTER_LOG_LEVEL", "HARVESTER_LOG_COMPONENTS", "HARVESTER_LOG_MIRROR",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
}

func harvesterCodexEnvFixture(t *testing.T, home string, names []string) string {
	t.Helper()
	body := fmt.Sprintf("[mcp_servers.harvester-professor]\ncommand = %q\nargs = [\"mcp\"]\n",
		mcpFamilyBinary(home, mcpServerHarvester))
	if names != nil {
		var encoded bytes.Buffer
		if err := toml.NewEncoder(&encoded).Encode(map[string]any{"env_vars": names}); err != nil {
			t.Fatal(err)
		}
		body += encoded.String()
	}
	return body
}

func TestHarvesterCodexInheritsRuntimeNames(t *testing.T) {
	const sentinel = "fixture-runtime-value-must-not-be-serialized"
	for _, name := range harvesterRuntimeNamesFixture {
		t.Setenv(name, sentinel)
	}
	home := t.TempDir()
	inst := &engine{options: Options{
		Home: home, CodexHomes: []string{filepath.Join(home, "cx-one"), filepath.Join(home, "cx-two")},
		Stdout: io.Discard,
	}, apply: true, stamp: "fixture"}
	if err := inst.writeMCPCodeConfig([]string{"chat-professor", "harvester-professor"}); err != nil {
		t.Fatal(err)
	}
	for _, codexHome := range inst.codexHomes() {
		path := filepath.Join(codexHome, "config.toml")
		raw := readFixture(t, path)
		var document struct {
			Servers map[string]struct {
				EnvVars []string `toml:"env_vars"`
			} `toml:"mcp_servers"`
		}
		if _, err := toml.Decode(raw, &document); err != nil {
			t.Fatal(err)
		}
		if got := document.Servers["harvester-professor"].EnvVars; !reflect.DeepEqual(
			got,
			harvesterRuntimeNamesFixture,
		) {
			t.Fatalf("harvester runtime inheritance=%v, want %v", got, harvesterRuntimeNamesFixture)
		}
		if len(document.Servers["chat-professor"].EnvVars) != 0 || strings.Contains(raw, sentinel) ||
			strings.Contains(raw, "env =") {
			t.Fatalf("registration serialized runtime values or changed chat inheritance:\n%s", raw)
		}
		if err := inst.writeMCPCodeConfigAt(path, []string{"chat-professor", "harvester-professor"}); err != nil {
			t.Fatal(err)
		}
		if readFixture(t, path) != raw {
			t.Fatal("reinstall changed canonical environment registration")
		}
	}
}

func TestHarvesterCodexEnvironmentOwnership(t *testing.T) {
	for _, shape := range []string{"old orphan", "new orphan", "old fenced", "new fenced", "foreign edited"} {
		t.Run(shape, func(t *testing.T) {
			home := t.TempDir()
			path := filepath.Join(home, ".codex", "config.toml")
			var names []string
			if strings.HasPrefix(shape, "new") {
				names = harvesterRuntimeNamesFixture
			}
			if shape == "foreign edited" {
				names = append(append([]string(nil), harvesterRuntimeNamesFixture...), "PERSONAL_MCP_VARIABLE")
			}
			body := harvesterCodexEnvFixture(t, home, names)
			if strings.HasSuffix(shape, "fenced") {
				body = mcpFenceBegin + "\n" + body + mcpFenceEnd + "\n"
			}
			const personal = "model = \"personal-model\"\n"
			original := personal + body
			writeFixture(t, path, original)
			wantState := MCPClientPFM
			if shape == "foreign edited" {
				wantState = MCPClientForeignRegistration
			}
			if report := InspectCodexServers(path, home, 18377, "harvester-professor")[0]; report.State != wantState {
				t.Fatalf("inspect state=%s error=%v, want %s", report.State, report.Error, wantState)
			}
			inst := &engine{
				options: Options{Home: home, MCPPort: 18377, Stdout: io.Discard},
				apply:   true,
				stamp:   "fixture",
			}
			removed := personal
			if shape == "foreign edited" {
				removed = original
			}
			if err := inst.removeMCPCodeConfigAt(path); err != nil {
				t.Fatal(err)
			}
			if got := readFixture(t, path); got != removed {
				t.Fatalf("direct removal config=%q, want %q", got, removed)
			}
			writeFixture(t, path, original)
			if err := inst.writeMCPCodeConfigAt(path, []string{"harvester-professor"}); err != nil {
				t.Fatal(err)
			}
			got := readFixture(t, path)
			if shape == "foreign edited" {
				if got != original {
					t.Fatalf("reinstall changed foreign environment table:\n%s", got)
				}
			} else if strings.Count(got, "[mcp_servers.harvester-professor]") != 1 ||
				!strings.Contains(got, harvesterCodexEnvFixture(t, home, harvesterRuntimeNamesFixture)) {
				t.Fatalf("reinstall did not converge canonical inheritance:\n%s", got)
			}
			if err := inst.removeMCPCodeConfigAt(path); err != nil {
				t.Fatal(err)
			}
			want := personal
			if shape == "foreign edited" {
				want = original
			}
			if got := readFixture(t, path); got != want {
				t.Fatalf("uninstall config=%q, want %q", got, want)
			}
		})
	}
}
