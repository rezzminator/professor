package config

import (
	"encoding/json"
	"errors"
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
	server, registered := config.MCPServers[name]
	if !registered {
		return false, fmt.Errorf("unknown MCP server %q", name)
	}
	if config.Path == "" {
		return false, NoConfigPathError(nil)
	}
	if name == MCPServerHarvester {
		return SetHarvesterEnabled(config, enabled)
	}
	if server.Enabled == enabled {
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

// RemoveMCPAuthToken removes the retired installer-owned daemon credential
// while preserving every unrelated field. The strict loader still accepts the
// legacy key so an existing host can reach this cleanup path.
func RemoveMCPAuthToken(config Config) (bool, error) {
	content, changed, err := configWithoutMCPAuthToken(config)
	if err != nil || !changed {
		return changed, err
	}
	if err := atomicfile.Write(config.Path, content, 0o600); err != nil {
		return false, err
	}
	return true, nil
}

// MCPAuthTokenPresent reports whether the retired installer credential is
// present without modifying the config. Install preview uses the same parser
// as apply so the cleanup appears in the plan before any host mutation.
func MCPAuthTokenPresent(config Config) (bool, error) {
	_, changed, err := configWithoutMCPAuthToken(config)
	return changed, err
}

func configWithoutMCPAuthToken(config Config) ([]byte, bool, error) {
	if config.Path == "" {
		return nil, false, errors.New("MCP auth token cleanup has no config path")
	}
	top := make(map[string]json.RawMessage)
	if !config.Exists {
		return nil, false, nil
	}
	content, err := os.ReadFile(config.Path)
	if err != nil {
		return nil, false, fmt.Errorf("read config %s for MCP auth cleanup: %w", config.Path, err)
	}
	if err := json.Unmarshal(content, &top); err != nil {
		return nil, false, configJSONError(config.Path, err)
	}
	mcpObject := make(map[string]json.RawMessage)
	if content := top["mcp"]; len(content) != 0 {
		if err := json.Unmarshal(content, &mcpObject); err != nil {
			return nil, false, fmt.Errorf("decode config %s mcp for auth cleanup: %w", config.Path, err)
		}
	}
	if _, present := mcpObject["authToken"]; !present {
		return nil, false, nil
	}
	delete(mcpObject, "authToken")
	mcpContent, _ := json.Marshal(mcpObject)
	top["mcp"] = mcpContent
	content, err = json.MarshalIndent(top, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("encode config %s: %w", config.Path, err)
	}
	return append(content, '\n'), true, nil
}
