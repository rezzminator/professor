package harvestmcp

// The registered tool names, spelled once so register() and
// RegisteredToolNames share one literal each.
const (
	toolRead             = "harvester_read"
	toolDownloadFile     = "harvester_download_file"
	toolSearchLiterature = "harvester_search_literature"
	toolSearchWeb        = "harvester_search_web"
)

// RegisteredToolNames returns the harvester's registered MCP tool names, in
// the exact order (*Service).register adds them: harvester_search_web only
// when runtimeSearchEnabled(runtime) holds (harvester_read is registered on
// the remote gateway too, its schema without `files`). It is the one place outside a
// live tools/list call that learns the advertised surface (the MCP daemon's
// /status handler reads it so it never claims a tool the harvester did not
// register). Any tool register adds, removes, or reorders moves here in the
// same edit.
func RegisteredToolNames(runtime Runtime) []string {
	names := []string{toolRead, toolDownloadFile, toolSearchLiterature}
	if runtimeSearchEnabled(runtime) {
		names = append(names, toolSearchWeb)
	}
	return names
}
