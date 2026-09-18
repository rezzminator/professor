package harvestmcp

// RegisteredToolNames returns the harvester's registered MCP tool names, in
// the exact order (*Service).register adds them to the server. `search` is
// included only when runtimeSearchEnabled(runtime) holds, mirroring the gate
// (*Service).register applies around its own "search" mcp.AddTool call —
// this is the one place outside a live tools/list call that learns the
// harvester's advertised surface (the MCP daemon's /status handler reads it
// so it never claims a tool the harvester did not register). Any tool
// (*Service).register adds, removes, or reorders must move here in the same
// edit, or the two drift apart again.
// toolFetch is the fetch tool's registered name, spelled once here so the
// registered list and (*Service).register share one literal count.
const toolFetch = "fetch"

func RegisteredToolNames(runtime Runtime) []string {
	names := []string{toolFetch, "findWorks"}
	if runtimeSearchEnabled(runtime) {
		names = append(names, "search")
	}
	return append(names, "fetchImage", "archive", "searchCache")
}
