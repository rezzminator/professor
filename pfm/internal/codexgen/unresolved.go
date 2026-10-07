package codexgen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/sourcelink"
)

const skillFileName = "SKILL.md"

// sourceEntry is one discovered Claude source. A non-empty target marks a
// symlink that does not resolve right now — an adopter's
// .claude/agents/labber.md pointing into an uninitialised submodule — and
// carries the link's own target text for the warning.
type sourceEntry struct {
	path     string
	rel      string
	skillDir bool
	dirLink  bool
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

// markdownSources is discoverMarkdown plus every unresolvable .md or directory link
// that walk recorded, each marked by its target. A source that is truly gone
// (no file and no link) appears in neither list, so its twin stays an orphan.
func markdownSources(dir string, excludes []string, result *Result) []sourceEntry {
	before := len(result.leafLinks)
	entries := discoverMarkdown(dir, excludes, result)
	for _, leaf := range result.leafLinks[before:] {
		name := filepath.Base(leaf.path)
		if name == skillFileName {
			skill := leaf
			skill.rel, skill.dirLink, skill.skillDir = filepath.Dir(leaf.rel), true, true
			entries = append(entries, skill)
		}
		if strings.HasSuffix(name, ".md") {
			if name == "README.md" || name == skillFileName {
				leaf.dirLink = true
			}
			entries = append(entries, leaf)
		} else {
			leaf.dirLink = true
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
	return keptTwin(
		filepath.Join(root, ".codex", "skills", flatName(filepath.ToSlash(entry.rel)), skillFileName),
		entry,
	)
}

func keptDirTwins(root string, entry sourceEntry, problem func(string)) []generatedFile {
	dir := filepath.Join(root, ".codex", "skills")
	twins, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		problem(fmt.Sprintf("read %s: %v", dir, err))
		return nil
	}
	name := flatName(filepath.ToSlash(entry.rel))
	prefix := strings.ReplaceAll(filepath.ToSlash(entry.rel), "/", "-")
	var outputs []generatedFile
	for _, twin := range twins {
		if twin.Name() == name || (!entry.skillDir && strings.HasPrefix(twin.Name(), prefix+"-")) {
			outputs = append(outputs, keptTwin(filepath.Join(dir, twin.Name()), entry))
		}
	}
	return outputs
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

// keptGlobalCommandTwins preserves both global surfaces of an unresolved command.
func keptGlobalCommandTwins(home string, entry sourceEntry, problem func(string)) []generatedFile {
	outputs := keptDirTwins(home, entry, problem)
	if entry.skillDir {
		return outputs
	}
	dir := filepath.Join(home, ".codex", "prompts")
	prompts, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return outputs
	}
	if err != nil {
		problem(fmt.Sprintf("read %s: %v", dir, err))
		return outputs
	}
	name := flatName(filepath.ToSlash(entry.rel))
	prefix := strings.ReplaceAll(filepath.ToSlash(entry.rel), "/", "-")
	for _, prompt := range prompts {
		if prompt.Name() == name+".md" || strings.HasPrefix(prompt.Name(), prefix+"-") {
			outputs = append(outputs, keptTwin(filepath.Join(dir, prompt.Name()), entry))
		}
	}
	return outputs
}
