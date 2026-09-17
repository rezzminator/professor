package config

import "testing"

func TestMCPServerKey(t *testing.T) {
	if got := MCPServerKey(MCPServerHarvester); got != "harvester.enabled" {
		t.Fatalf("MCPServerKey(harvester) = %q", got)
	}
	if got := MCPServerKey(MCPServerChat); got != "mcp.servers.chat.enabled" {
		t.Fatalf("MCPServerKey(chat) = %q", got)
	}
}
