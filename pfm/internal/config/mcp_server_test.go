package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMCPServerKey(t *testing.T) {
	if got := MCPServerKey(MCPServerChat); got != "mcp.servers.chat.enabled" {
		t.Fatalf("MCPServerKey(chat) = %q", got)
	}
}

func TestMCPFamilyPaths(t *testing.T) {
	for family, want := range map[string]string{MCPServerChat: "/mcp/chat-professor"} {
		if got := MCPFamilyPath(family); got != want {
			t.Fatalf("MCPFamilyPath(%s) = %q, want %q", family, got, want)
		}
	}
}

func TestValidateThirdParty(t *testing.T) {
	const path = "fixture.config.json"
	objectError := "config " + path + ": mcp.thirdParty.x must be a JSON object (a Claude mcpServers entry)"
	reservedError := "config " + path + ": mcp.thirdParty.chat-professor: the name chat-professor is pfm's own server"
	for _, tc := range []struct {
		name    string
		entries map[string]json.RawMessage
		want    string
	}{
		{"none", nil, ""},
		{"empty", map[string]json.RawMessage{}, ""},
		{"objects", map[string]json.RawMessage{"x": json.RawMessage(` {"type":"stdio","command":"x"} `), "http": json.RawMessage(`{"type":"http","url":"https://example.invalid/mcp"}`), "empty": json.RawMessage(`{}`)}, ""},
		{"foreign professor", map[string]json.RawMessage{"professor": json.RawMessage(`{}`)}, ""},
		{"chat registration", map[string]json.RawMessage{"chat-professor": json.RawMessage(`{}`)}, reservedError},
		{"string", map[string]json.RawMessage{"x": json.RawMessage(`"y"`)}, objectError},
		{"array", map[string]json.RawMessage{"x": json.RawMessage(`[]`)}, objectError},
		{"null", map[string]json.RawMessage{"x": json.RawMessage(`null`)}, objectError},
		{"number", map[string]json.RawMessage{"x": json.RawMessage(`1`)}, objectError},
		{"boolean", map[string]json.RawMessage{"x": json.RawMessage(`true`)}, objectError},
		{"missing", map[string]json.RawMessage{"x": nil}, objectError},
		{"malformed", map[string]json.RawMessage{"x": json.RawMessage(`{`)}, objectError},
		{"sorted before professor", map[string]json.RawMessage{"professor": json.RawMessage(`{}`), "a": json.RawMessage(`null`)}, "config " + path + ": mcp.thirdParty.a must be a JSON object (a Claude mcpServers entry)"},
		{"sorted registration first", map[string]json.RawMessage{"chat-professor": json.RawMessage(`{}`), "z": json.RawMessage(`null`)}, reservedError},
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

func TestLoadPreservesUnregisteredMCPServer(t *testing.T) {
	registered := map[string]MCPServer{"chat": {Enabled: false}}
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(
		path,
		[]byte(`{"version":1,"mcp":{"servers":{"external":{"enabled":true,"extra":"kept"}}}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	got, err := loadWithMCPServers(path, home, nil, registered)
	if err != nil {
		t.Fatalf("Load(mcp) error = %v", err)
	}
	if got.Source("mcp.servers.chat.enabled") != SourceDefault {
		t.Fatalf("chat source = %q, want default", got.Source("mcp.servers.chat.enabled"))
	}
	if got.MCP.Servers["external"].Enabled {
		t.Fatal("unregistered server became active")
	}
	var gotServer map[string]any
	if err := json.Unmarshal(got.MCP.OtherServers["external"], &gotServer); err != nil ||
		gotServer["enabled"] != true || gotServer["extra"] != "kept" {
		t.Fatalf("unregistered server = %s, want original data", got.MCP.OtherServers["external"])
	}
	encoded, err := Marshal(got, false)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	var mcp struct {
		Servers map[string]json.RawMessage `json:"servers"`
	}
	if err := json.Unmarshal(roundTrip["mcp"], &mcp); err != nil {
		t.Fatal(err)
	}
	var roundTripServer map[string]any
	if err := json.Unmarshal(mcp.Servers["external"], &roundTripServer); err != nil ||
		roundTripServer["enabled"] != true || roundTripServer["extra"] != "kept" {
		t.Fatalf("marshaled unregistered server = %s", mcp.Servers["external"])
	}
}
