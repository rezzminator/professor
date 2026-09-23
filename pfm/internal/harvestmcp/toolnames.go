package harvestmcp

// The registered tool names, spelled once so register() and
// RegisteredToolNames share one literal each.
const (
	toolReadPage   = "readPage"
	toolParseLocal = "parseLocalDocuments"
	toolDownload   = "download"
	toolFindWorks  = "findWorks"
	toolReadWork   = "readWork"
	toolWebSearch  = "webSearch"
)

// RegisteredToolNames returns the harvester's registered MCP tool names, in
// the exact order (*Service).register adds them: parseLocalDocuments only on
// a local server (never the remote gateway), webSearch only when
// runtimeSearchEnabled(runtime) holds. It is the one place outside a live
// tools/list call that learns the advertised surface (the MCP daemon's
// /status handler reads it so it never claims a tool the harvester did not
// register). Any tool register adds, removes, or reorders moves here in the
// same edit.
func RegisteredToolNames(runtime Runtime) []string {
	names := []string{toolReadPage}
	if !runtime.Remote {
		names = append(names, toolParseLocal)
	}
	names = append(names, toolDownload, toolFindWorks, toolReadWork)
	if runtimeSearchEnabled(runtime) {
		names = append(names, toolWebSearch)
	}
	return names
}
