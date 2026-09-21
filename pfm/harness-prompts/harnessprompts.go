// Package harnessprompts owns the fleet's harness-prompt tree and embeds it
// into the binary: the shared head and tail, one middle per engine, and
// Claude's drift baselines. The tree lives HERE and nowhere else — the
// installer composes and stages from this package, and `pfm doctor` hashes
// what a binary carries against this same directory in the blueprint clone,
// so a binary older than the templates is NAMED rather than left quietly
// staging last month's prompt.
package harnessprompts

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
)

// DirName is this directory's own name — the tree sits at pfm/<DirName> in
// the blueprint clone, and the composed prompts stage into <DirName> under
// the managed root. Every caller spells it from here.
const DirName = "harness-prompts"

// tree is the embedded tree, rooted at this directory: "README.md",
// "share/head.md", "claude/professor.md",
// "claude/baselines/harness-original.sha256" and the rest. The entries are
// named one at a time rather than swept up by a wildcard, which would also
// match this package's own sources, and a directory pattern silently skips
// any name beginning with "." or "_" — so a new engine directory has to be
// added here too, and until it is, doctor's embed row reports the clone as
// ahead of the binary. The README is embedded like every other file, which is
// what lets doctor compare the two trees whole; staging is where it is held
// back (harnessPromptAssetFiles).
//
//go:embed README.md claude codex opencode share
var tree embed.FS

// FS is the embedded tree as a read-only filesystem, for callers that walk it.
func FS() fs.FS {
	return tree
}

// ReadPart returns one part by its tree-relative slash path, e.g.
// "share/head.md". The error names the part, so a caller's failure says which
// file it could not read rather than that something was missing.
func ReadPart(name string) ([]byte, error) {
	content, err := tree.ReadFile(name)
	if err != nil {
		return nil, fmt.Errorf("read embedded harness prompt %s: %w", name, err)
	}
	return content, nil
}

// composeParts names the three embedded files one engine's prompt is composed
// from, head first and tail last. Embedded paths are always slash paths.
func composeParts(engineLongName string) [3]string {
	return [3]string{"share/head.md", engineLongName + "/professor.md", "share/tail.md"}
}

// Composed joins the shared head, one engine's middle and the shared tail.
// Each part's trailing newlines are trimmed and the three are joined by a
// single blank line, so a part that gains or loses a trailing newline cannot
// move the seams — a composed prompt is the three parts and the two seams,
// and nothing else. Composition lives HERE because more than one caller needs
// the same bytes: the installer stages them per engine, writes Codex's copy
// into config.toml, and codexgen prepends Codex's copy to every compiled role.
func Composed(engineLongName string) ([]byte, error) {
	var sections [3][]byte
	for index, name := range composeParts(engineLongName) {
		raw, err := ReadPart(name)
		if err != nil {
			return nil, err
		}
		sections[index] = bytes.TrimRight(raw, "\n")
	}
	return append(bytes.Join(sections[:], []byte("\n\n")), '\n'), nil
}
