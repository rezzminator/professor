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
