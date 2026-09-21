package kill

import "github.com/rezzminator/professor/pfm/internal/codexmeta"

// Unindexed lineage uses the same bounded metadata reader as live identity.
// A failed read leaves the existing indexed fallback responsible for resolution.
func readCodexLineageParent(path string) string {
	header, err := codexmeta.ReadHeader(path)
	if err != nil {
		return ""
	}
	return header.LineageParent
}
