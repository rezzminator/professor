package harvestmcp

// RegisteredToolNames returns the harvester's registered MCP tool names, in
// the exact order register() (service.go:470-546) adds them to the server. `search` is
// included only when runtimeSearchEnabled(runtime) holds, mirroring the gate
// register() applies at service.go:493 — this is the one place outside a
// live tools/list call that learns the harvester's advertised surface (the
// MCP daemon's /status handler reads it so it never claims a tool the
// harvester did not register). Any tool register() adds, removes, or
// reorders must move here in the same edit, or the two drift apart again.
// toolFetch is the fetch tool's registered name, spelled once here so the
// registered list and register() (service.go) share one literal count.
const toolFetch = "fetch"

func RegisteredToolNames(runtime Runtime) []string {
	names := []string{toolFetch, "findWorks"}
	if runtimeSearchEnabled(runtime) {
		names = append(names, "search")
	}
	return append(names, "fetchImage", "archive", "searchCache")
}
