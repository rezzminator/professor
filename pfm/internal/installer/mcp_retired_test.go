package installer

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"testing"
)

func TestRetiredOpenCodeRegistrationSweep(t *testing.T) {
	for _, binary := range []string{"pfm", mcpRetiredFamilyName} {
		t.Run(binary, func(t *testing.T) {
			home := t.TempDir()
			args := []string{filepath.Join(home, ".local", "bin", binary), "mcp"}
			if binary == "pfm" {
				args = append(args, "serve", "--stdio", "--family", mcpRetiredFamilyName)
			}
			entry := map[string]any{"type": "local", "command": args, "enabled": true}
			raw, err := json.Marshal(
				map[string]any{
					"mcp": map[string]any{
						mcpRetiredServerName: entry,
						mcpRetiredFamilyName: map[string]any{
							"type":    "local",
							"command": []string{"operator", "mcp"},
							"enabled": true,
						},
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			path := OpenCodeConfigPath(home)
			writeFixture(t, path, string(raw))
			inst := engine{
				options:     Options{Home: home, OpenCodeConfigPath: path, MCPPort: 18377, Stdout: io.Discard},
				managedRoot: managedRootForHome(home),
				apply:       true,
				stamp:       "fixture",
			}
			if err := inst.writeMCPOpenCodeJSON(nil); err != nil {
				t.Fatal(err)
			}
			document, err := decodeJSONCObject([]byte(readFixture(t, path)))
			if err != nil {
				t.Fatal(err)
			}
			servers := document["mcp"].(map[string]any)
			if len(servers) != 1 || servers[mcpRetiredFamilyName] == nil {
				t.Fatalf("retired registration survived or native registration changed: %#v", servers)
			}
		})
	}
}

func TestRetiredRegistrationShapesPreserveForeignEntries(t *testing.T) {
	if len(mcpRetiredRegistrations) != 4 {
		t.Fatalf("retired shape inventory changed: %d", len(mcpRetiredRegistrations))
	}
	for _, retired := range mcpRetiredRegistrations {
		t.Run(retired.name+"/"+retired.binary, func(t *testing.T) {
			home := t.TempDir()
			bin := filepath.Join(home, ".local", "bin", "pfm")
			entry := map[string]any{"type": "http", "url": legacyMCPURL(18377, retired.name)}
			if retired.binary != "" {
				args := make([]any, len(retired.args))
				for i, arg := range retired.args {
					args[i] = arg
				}
				entry = map[string]any{
					"type":    "stdio",
					"command": filepath.Join(filepath.Dir(bin), retired.binary),
					"args":    args,
				}
			}
			if !isPFMLegacyClaudeShape(retired.name, entry, bin, 18377) {
				t.Fatal("exact retired Claude shape unrecognized")
			}
			entry["timeout"] = 30
			if isPFMLegacyClaudeShape(retired.name, entry, bin, 18377) {
				t.Fatal("foreign extra property was owned")
			}
			delete(entry, "timeout")
			if retired.binary != "" {
				entry["command"] = filepath.Join(home, "operator", retired.binary)
				if isPFMLegacyClaudeShape(retired.name, entry, bin, 18377) {
					t.Fatal("foreign binary was owned")
				}
			}
		})
	}
}

func TestRetiredCodexRegistrationSweep(t *testing.T) {
	for _, retired := range mcpRetiredRegistrations {
		for _, foreign := range []bool{false, true} {
			t.Run(retired.name+"/"+retired.binary+"/"+fmt.Sprint(foreign), func(t *testing.T) {
				home := t.TempDir()
				entry := map[string]any{"url": legacyMCPURL(18377, retired.name)}
				if retired.binary != "" {
					entry = map[string]any{
						"command": filepath.Join(home, ".local", "bin", retired.binary),
						"args":    retired.args,
					}
				}
				if foreign {
					entry["startup_timeout_sec"] = 30
				}
				body := ""
				for _, key := range []string{"command", "args", "url", "startup_timeout_sec"} {
					if value, present := entry[key]; present {
						encoded, err := encodeCodexValues(map[string]any{key: value})
						if err != nil {
							t.Fatal(err)
						}
						body += encoded
					}
				}
				original := "model = \"personal\"\n[mcp_servers." + retired.name + "]\n" + body
				path := filepath.Join(home, ".codex", "config.toml")
				writeFixture(t, path, original)
				inst := engine{
					options: Options{Home: home, MCPPort: 18377, Stdout: io.Discard},
					apply:   true,
					stamp:   "fixture",
				}
				if err := inst.removeMCPCodeConfigAt(path); err != nil {
					t.Fatal(err)
				}
				want := "model = \"personal\"\n"
				if foreign {
					want = original
				}
				if got := readFixture(t, path); got != want {
					t.Fatalf("config=%q want=%q", got, want)
				}
			})
		}
	}
}
