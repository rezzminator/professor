package main

import (
	"github.com/rezzminator/professor/pfm/internal/harvestmcp"
)

// harvestRuntime resolves this machine's harvester config into the MCP
// adapter's runtime (harvestmcp.RuntimeFromConfig).
func harvestRuntime(runtime commandRuntime) harvestmcp.Runtime {
	return harvestmcp.RuntimeFromConfig(runtime.Paths.Home, runtime.Config.Harvester)
}
