package config

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
