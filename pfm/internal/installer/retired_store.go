package installer

import "path/filepath"

// RetiredGeneratedStores names the two home stores Professor's earlier engine-sync
// policy rendered Claude and Codex agents into before they moved into the
// clone's templates/generated/agents. Nothing writes there now: a registry link
// into one is the STORE class of the registry-ownership scan, and the
// `retired-generated-store` stale detector names a store that still exists.
// This is the one copy of the two paths.
func RetiredGeneratedStores(home string) []string {
	generated := filepath.Join(home, ".local", "state", "pfm", "generated")
	return []string{
		filepath.Join(generated, "claude-agents"),
		filepath.Join(generated, "codex-agents"),
	}
}
