// Package harnessprompts owns the fleet's harness-prompt tree: the shared
// head and tail, one middle per engine, and Claude's drift baselines. The
// tree lives HERE and nowhere else. A prompt is composed from the source
// clone's copy of this directory when the clone holds one, so an edited part
// reaches `pfm generate` and `pfm install` without a rebuild; the binary
// embeds the tree for a binary-only install, which has no clone.
package harnessprompts

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// DirName is this directory's own name — the tree sits at pfm/<DirName> in
// the blueprint clone. Every caller spells it from here.
const DirName = "harness-prompts"

// tree is the embedded tree, rooted at this directory: "README.md",
// "share/head.md", "claude/professor.md",
// "claude/baselines/harness-original.sha256" and the rest. The entries are
// named one at a time rather than swept up by a wildcard, which would also
// match this package's own sources, and a directory pattern silently skips
// any name beginning with "." or "_" — so a new engine directory has to be
// added here too, or a binary-only install cannot compose its prompt.
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

// composeParts names the three files one engine's prompt is composed from,
// head first and tail last, as slash paths relative to the tree's root.
func composeParts(engineLongName string) [3]string {
	return [3]string{"share/head.md", engineLongName + "/professor.md", "share/tail.md"}
}

// source resolves the tree a prompt is composed from: the clone's
// pfm/harness-prompts directory when it holds source parts, else the embedded
// tree. Only an entry the embedded tree also holds at its root counts as a
// source part: a directory holding only generated/ (the outputs install writes
// even on a host with no source clone) or stray files such as .DS_Store
// composes from the embed. A clone tree
// that holds parts is the source even when one is missing: falling back to the
// embed there would compose a prompt the clone no longer holds. The label
// names the source in every error.
func source(clone string) (fs.FS, string, error) {
	if clone == "" {
		return nil, "", errors.New("resolve the harness prompt tree: no source clone path given")
	}
	dir := filepath.Join(clone, "pfm", DirName)
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return tree, "embedded", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("read harness prompt tree %s: %w", dir, err)
	}
	roots, err := tree.ReadDir(".")
	if err != nil {
		return nil, "", fmt.Errorf("read the embedded harness prompt tree's root: %w", err)
	}
	for _, entry := range entries {
		for _, root := range roots {
			if entry.Name() == root.Name() {
				return os.DirFS(dir), dir, nil
			}
		}
	}
	return tree, "embedded", nil
}

// Composed joins the shared head, one engine's middle and the shared tail,
// read from the clone's tree when it exists, else from the embedded one
// (source). Each part's trailing newlines are trimmed and the three are joined
// by a single blank line, so a part that gains or loses a trailing newline
// cannot move the seams — a composed prompt is the three parts and the two
// seams, and nothing else. Composition lives HERE because more than one caller
// needs the same bytes: the installer generates them per engine, writes
// Codex's copy into config.toml, and a Codex --agent-role seat carries Codex's
// copy ahead of its role (codexprompt).
func Composed(clone, engineLongName string) ([]byte, error) {
	parts, label, err := source(clone)
	if err != nil {
		return nil, err
	}
	var sections [3][]byte
	for index, name := range composeParts(engineLongName) {
		raw, err := fs.ReadFile(parts, name)
		if err != nil {
			return nil, fmt.Errorf("read %s harness prompt %s: %w", label, name, err)
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			return nil, fmt.Errorf("compose %s harness prompt: empty %s part %s", engineLongName, label, name)
		}
		sections[index] = bytes.TrimRight(raw, "\n")
	}
	return append(bytes.Join(sections[:], []byte("\n\n")), '\n'), nil
}
