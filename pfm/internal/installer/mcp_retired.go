package installer

import (
	"path/filepath"
	"slices"
	"strings"
)

const (
	mcpRetiredFamilyName = "harvester"
	mcpRetiredServerName = "harvester-professor"
)

// These are exact registrations older installers wrote, never active servers.
var mcpRetiredRegistrations = []struct {
	name     string
	binary   string
	args     []string
	envNames []string
}{
	{name: mcpRetiredFamilyName},
	{name: mcpRetiredServerName},
	{
		name:   mcpRetiredServerName,
		binary: "pfm",
		args:   []string{"mcp", "serve", "--stdio", "--family", mcpRetiredFamilyName},
	},
	{
		name: mcpRetiredServerName, binary: mcpRetiredFamilyName, args: []string{"mcp"},
		envNames: []string{
			"HARVESTER_HOME_DIR", "HARVESTER_CONFIG", "HARVESTER_PYTHON_ROOT",
			"XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_DATA_HOME",
			"HARVESTER_LOG_LEVEL", "HARVESTER_LOG_COMPONENTS", "HARVESTER_LOG_MIRROR",
			"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
		},
	},
}

// LegacyMCPNames lists the retired registration keys installers can remove.
func LegacyMCPNames() []string {
	names := []string{chatName, professorName}
	for _, retired := range mcpRetiredRegistrations {
		if !slices.Contains(names, retired.name) {
			names = append(names, retired.name)
		}
	}
	return names
}

func isRetiredMCPShape(name string, registration map[string]any, bin string, port int) bool {
	for _, retired := range mcpRetiredRegistrations {
		if retired.name != name {
			continue
		}
		if retired.binary == "" {
			if isPFMHTTPShape(registration, legacyMCPURL(port, name)) {
				return true
			}
		} else if isExactStdioShape(registration, filepath.Join(filepath.Dir(bin), retired.binary), retired.args) {
			return true
		}
	}
	return false
}

func stripRetiredCodexTables(lines []string, removed []bool, port int, professorBody []string) {
	for _, retired := range mcpRetiredRegistrations {
		if retired.binary == "" {
			removeExactCodexTable(
				lines,
				removed,
				retired.name,
				[]string{"url = \"" + legacyMCPURL(port, retired.name) + "\""},
			)
			continue
		}
		body := append([]string(nil), professorBody...)
		for i, line := range body {
			if strings.HasPrefix(line, "command = ") {
				body[i] = strings.TrimSuffix(line, `pfm"`) + retired.binary + `"`
			} else if strings.HasPrefix(line, "args = ") {
				body[i] = `args = ["` + strings.Join(retired.args, `", "`) + `"]`
			}
		}
		removeExactCodexTable(lines, removed, retired.name, body)
		if len(retired.envNames) != 0 {
			body = append(body, `env_vars = ["`+strings.Join(retired.envNames, `", "`)+`"]`)
			removeExactCodexTable(lines, removed, retired.name, body)
		}
	}
}
