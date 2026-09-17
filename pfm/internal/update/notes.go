// Package update owns the self-update workflow and its release-note filtering.
package update

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"hostops/pfm/internal/semver"
)

// ReleaseNotes filters releaseFiles (the names `git ls-tree --name-only
// <target> releases/` lists at target) down to the releases/vX.Y.Z.md entries
// an update from previousTag to target moved past: version newer than
// previousTag and no newer than target, returned oldest first as paths
// relative to the repository root.
func ReleaseNotes(previousTag, target string, releaseFiles []string) ([]string, error) {
	previousVersion, ok := semver.ParseVersion(previousTag)
	if !ok {
		return nil, fmt.Errorf("previous release tag %q does not parse as vMAJOR.MINOR.PATCH", previousTag)
	}
	targetVersion, ok := semver.ParseVersion(target)
	if !ok {
		return nil, fmt.Errorf("target tag %q does not parse as vMAJOR.MINOR.PATCH", target)
	}
	type versionedNote struct {
		version semver.Version
		base    string
	}
	var notes []versionedNote
	for _, line := range releaseFiles {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		base := filepath.Base(line)
		if !strings.HasSuffix(base, ".md") {
			continue
		}
		stem := strings.TrimSuffix(base, ".md")
		version, ok := semver.ParseVersion(stem)
		if !ok {
			continue
		}
		if !previousVersion.Less(version) || targetVersion.Less(version) {
			continue
		}
		notes = append(notes, versionedNote{version: version, base: base})
	}
	sort.Slice(notes, func(i, j int) bool { return notes[i].version.Less(notes[j].version) })
	paths := make([]string, 0, len(notes))
	for _, note := range notes {
		paths = append(paths, filepath.Join("releases", note.base))
	}
	return paths, nil
}
