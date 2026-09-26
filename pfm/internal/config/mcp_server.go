package config

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
