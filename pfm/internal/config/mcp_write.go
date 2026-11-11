package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

func RegisteredMCPServers() []string {
	registered := productionMCPServers()
	names := make([]string, 0, len(registered))
	for name := range registered {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// SetMCPServer atomically changes one registered server in the machine file.
// Repeating an already-effective setting is a no-op.
func SetMCPServer(config Config, name string, enabled bool) (bool, error) {
	return writeMCPServer(config, name, enabled, false)
}

// SetMCPServerExplicit records an enabled flag even when its default already matches.
func SetMCPServerExplicit(config Config, name string, enabled bool) (bool, error) {
	return writeMCPServer(config, name, enabled, true)
}

func writeMCPServer(config Config, name string, enabled, explicit bool) (bool, error) {
	server, registered := config.MCPServers[name]
	if !registered {
		return false, fmt.Errorf("unknown MCP server %q", name)
	}
	if config.Path == "" {
		return false, NoConfigPathError(nil)
	}
	if server.Enabled == enabled && (!explicit || config.MCPServerSource(name) == SourceFile) {
		return false, nil
	}

	top := make(map[string]json.RawMessage)
	if config.Exists {
		content, err := os.ReadFile(config.Path)
		if err != nil {
			return false, fmt.Errorf("read config %s for update: %w", config.Path, err)
		}
		if err := json.Unmarshal(content, &top); err != nil {
			return false, configJSONError(config.Path, err)
		}
	}
	version, _ := json.Marshal(Version)
	top[keyVersion] = version

	mcpObject := make(map[string]json.RawMessage)
	if content := top["mcp"]; len(content) != 0 {
		if err := json.Unmarshal(content, &mcpObject); err != nil {
			return false, fmt.Errorf("decode config %s mcp for update: %w", config.Path, err)
		}
	}
	servers := make(map[string]json.RawMessage)
	if content := mcpObject["servers"]; len(content) != 0 {
		if err := json.Unmarshal(content, &servers); err != nil {
			return false, fmt.Errorf("decode config %s mcp.servers for update: %w", config.Path, err)
		}
	}
	serverObject := map[string]bool{jsonKeyEnabled: enabled}
	serverContent, _ := json.Marshal(serverObject)
	servers[name] = serverContent
	serversContent, _ := json.Marshal(servers)
	mcpObject["servers"] = serversContent
	mcpContent, _ := json.Marshal(mcpObject)
	top["mcp"] = mcpContent

	content, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return false, fmt.Errorf("encode config %s: %w", config.Path, err)
	}
	content = append(content, '\n')
	if err := atomicfile.Write(config.Path, content, 0o600); err != nil {
		return false, err
	}
	return true, nil
}
