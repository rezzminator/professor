package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

const (
	MCPServerHarvester = "harvester"
	MCPServerChat      = "chat"
	// MCPServerProfessor is the one MCP server every client registers: it
	// carries every enabled family's tools.
	MCPServerProfessor = "professor"
	// MCPPathProfessor is the daemon route of the combined professor server.
	MCPPathProfessor = "/mcp/" + MCPServerProfessor
)

// MCPServerKey names where a registered server's enabled flag is configured.
func MCPServerKey(name string) string {
	if name == MCPServerHarvester {
		return "harvester.enabled"
	}
	return "mcp.servers." + name + ".enabled"
}

// MCPFamilyPath is the daemon route of one family's view of the professor
// server.
func MCPFamilyPath(family string) string {
	return MCPPathProfessor + "/" + family
}

func validateThirdParty(path string, entries map[string]json.RawMessage) error {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name == MCPServerProfessor {
			return fmt.Errorf("config %s: mcp.thirdParty.professor: the name professor is pfm's own server", path)
		}
		entry := bytes.TrimSpace(entries[name])
		if !json.Valid(entry) || len(entry) == 0 || entry[0] != '{' {
			return fmt.Errorf(
				"config %s: mcp.thirdParty.%s must be a JSON object (a Claude mcpServers entry)",
				path,
				name,
			)
		}
	}
	return nil
}
