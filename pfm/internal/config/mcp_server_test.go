package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMCPServerKey(t *testing.T) {
	if got := MCPServerKey(MCPServerHarvester); got != "harvester.enabled" {
		t.Fatalf("MCPServerKey(harvester) = %q", got)
	}
	if got := MCPServerKey(MCPServerChat); got != "mcp.servers.chat.enabled" {
		t.Fatalf("MCPServerKey(chat) = %q", got)
	}
}

func TestMCPProfessorPaths(t *testing.T) {
	if MCPServerProfessor != "professor" || MCPPathProfessor != "/mcp/professor" {
		t.Fatalf("professor server = %q at %q", MCPServerProfessor, MCPPathProfessor)
	}
	for family, want := range map[string]string{
		MCPServerChat:      "/mcp/professor/chat",
		MCPServerHarvester: "/mcp/professor/harvester",
	} {
		if got := MCPFamilyPath(family); got != want {
			t.Fatalf("MCPFamilyPath(%s) = %q, want %q", family, got, want)
		}
	}
}

func TestValidateThirdParty(t *testing.T) {
	const path = "fixture.config.json"
	objectError := "config " + path + ": mcp.thirdParty.x must be a JSON object (a Claude mcpServers entry)"
	professorError := "config " + path + ": mcp.thirdParty.professor: the name professor is pfm's own server"
	for _, tc := range []struct {
		name    string
		entries map[string]json.RawMessage
		want    string
	}{
		{"none", nil, ""},
		{"empty", map[string]json.RawMessage{}, ""},
		{"objects", map[string]json.RawMessage{"x": json.RawMessage(` {"type":"stdio","command":"x"} `), "http": json.RawMessage(`{"type":"http","url":"https://example.invalid/mcp"}`), "empty": json.RawMessage(`{}`)}, ""},
		{"professor", map[string]json.RawMessage{"professor": json.RawMessage(`{}`)}, professorError},
		{"string", map[string]json.RawMessage{"x": json.RawMessage(`"y"`)}, objectError},
		{"array", map[string]json.RawMessage{"x": json.RawMessage(`[]`)}, objectError},
		{"null", map[string]json.RawMessage{"x": json.RawMessage(`null`)}, objectError},
		{"number", map[string]json.RawMessage{"x": json.RawMessage(`1`)}, objectError},
		{"boolean", map[string]json.RawMessage{"x": json.RawMessage(`true`)}, objectError},
		{"missing", map[string]json.RawMessage{"x": nil}, objectError},
		{"malformed", map[string]json.RawMessage{"x": json.RawMessage(`{`)}, objectError},
		{"sorted before professor", map[string]json.RawMessage{"professor": json.RawMessage(`{}`), "a": json.RawMessage(`null`)}, "config " + path + ": mcp.thirdParty.a must be a JSON object (a Claude mcpServers entry)"},
		{"sorted professor first", map[string]json.RawMessage{"professor": json.RawMessage(`{}`), "z": json.RawMessage(`null`)}, professorError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateThirdParty(path, tc.entries)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != tc.want {
				t.Fatalf("validateThirdParty error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestDefaultsRegisterHarvesterDisabledByDefault(t *testing.T) {
	got := Defaults(filepath.Join(t.TempDir(), "home"), nil)
	server, ok := got.MCPServers["harvester"]
	if !ok {
		t.Fatal("harvester MCP server is not registered")
	}
	if server.Enabled {
		t.Fatal("harvester MCP server is enabled by default")
	}
	if got.Source("mcp.servers.harvester.enabled") != SourceDefault {
		t.Fatalf("harvester source = %q, want default", got.Source("mcp.servers.harvester.enabled"))
	}
}

func TestLoadMCPServersHaveIndependentDefaultsAndSources(t *testing.T) {
	registered := map[string]MCPServer{
		"chat":      {Enabled: false},
		"harvester": {Enabled: false},
	}
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(
		path,
		[]byte(`{"version":1,"mcp":{"servers":{"harvester":{"enabled":true}}}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	got, err := loadWithMCPServers(path, home, nil, registered)
	if err != nil {
		t.Fatalf("Load(mcp) error = %v", err)
	}
	if got.MCPServers["chat"].Enabled {
		t.Fatal("chat became enabled when only harvester was configured")
	}
	if !got.MCPServers["harvester"].Enabled {
		t.Fatal("harvester did not take its configured enabled value")
	}
	if got.Source("mcp.servers.chat.enabled") != SourceDefault {
		t.Fatalf("chat source = %q, want default", got.Source("mcp.servers.chat.enabled"))
	}
	// A pre-split file's mcp.servers.harvester is honored, but reported as
	// legacy so `pfm config show` / doctor point at the migration.
	if got.MCPServerSource("harvester") != SourceLegacy {
		t.Fatalf("harvester source = %q, want %q", got.MCPServerSource("harvester"), SourceLegacy)
	}
}
