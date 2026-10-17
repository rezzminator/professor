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
)

// MCPServerKey names where a registered server's enabled flag is configured.
func MCPServerKey(name string) string {
	if name == MCPServerHarvester {
		return "harvester.enabled"
	}
	return "mcp.servers." + name + ".enabled"
}

// MCPRegistrationName names the client registration for a configured family.
func MCPRegistrationName(family string) string {
	return family + "-professor"
}

// MCPFamilyPath is the daemon route of one configured family.
func MCPFamilyPath(family string) string {
	return "/mcp/" + MCPRegistrationName(family)
}

func validateThirdParty(path string, entries map[string]json.RawMessage) error {
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name == MCPRegistrationName(MCPServerChat) || name == MCPRegistrationName(MCPServerHarvester) {
			return fmt.Errorf("config %s: mcp.thirdParty.%s: the name %s is pfm's own server", path, name, name)
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
