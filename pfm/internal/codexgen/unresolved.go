package codexgen

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/sourcelink"
)

// sourceEntry is one discovered Claude source. A non-empty target marks a
// leaf symlink that does not resolve right now — an adopter's
// .claude/agents/labber.md pointing into an uninitialised submodule — and
// carries the link's own target text for the warning.
type sourceEntry struct {
	path     string
	rel      string
	skillDir bool
	target   string
}

// unresolvedLeaf records a leaf source link discoverMarkdown cannot resolve:
// the typed dangling entry Check gates on, plus the link itself so a caller
// that can name the link's twin keeps that twin instead of sweeping it as an
// orphan (sourcelink.LinkTarget names an unreadable link's read failure).
func (result *Result) unresolvedLeaf(path, rel string, err error) {
	result.danglingSource(path, err)
	result.leafLinks = append(result.leafLinks, sourceEntry{
		path:   path,
		rel:    rel,
		target: sourcelink.LinkTarget(path),
	})
}

// markdownSources is discoverMarkdown plus every unresolvable leaf .md link
// that walk recorded, each marked by its target. A source that is truly gone
// (no file and no link) appears in neither list, so its twin stays an orphan.
func markdownSources(dir string, excludes []string, result *Result) []sourceEntry {
	before := len(result.leafLinks)
	entries := discoverMarkdown(dir, excludes, result)
	for _, leaf := range result.leafLinks[before:] {
		name := filepath.Base(leaf.path)
		if strings.HasSuffix(name, ".md") && name != "README.md" && name != "SKILL.md" {
			entries = append(entries, leaf)
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	return entries
}

// keptTwin is the output standing for an unresolvable source's twin at path.
func keptTwin(path string, source sourceEntry) generatedFile {
	return generatedFile{Path: path, Kept: &source}
}

// keptCommandTwin is keptTwin at the repo command skill compileCommandFile
// writes for entry.
func keptCommandTwin(root string, entry sourceEntry) generatedFile {
	return keptTwin(filepath.Join(root, ".codex", "skills", flatName(filepath.ToSlash(entry.rel)), "SKILL.md"), entry)
}

// keepTwin leaves a kept twin exactly as it is and says so when one exists
// (sourcelink.KeepTwin).
func (r *reconcileResult) keepTwin(output generatedFile) {
	warning, problem := sourcelink.KeepTwin(output.Path, output.Kept.path, output.Kept.target)
	if warning != "" {
		r.Warnings = append(r.Warnings, warning)
	}
	if problem != "" {
		r.Problems = append(r.Problems, problem)
	}
}
