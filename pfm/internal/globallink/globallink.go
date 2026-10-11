// Package globallink classifies and repairs the symlinks pfm install wires
// from the Professor clone and the skill store into the Claude registries and
// ~/.agents/skills: one vocabulary for every registry, so a drift never reads
// differently depending on which registry found it. engine-sync keeps its own
// copy for the Codex and Claude agent links it writes.
package globallink

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// State is the on-disk shape check found at one desired global
// registry target, classified against its desired symlink source. Every
// caller — agents, commands, skills — shares this one vocabulary so a drift
// never reads differently depending on which registry found it.
type State string

const (
	// Missing: nothing exists at the target yet.
	Missing State = "missing"
	// Correct: a symlink already resolves to the desired source.
	Correct State = "correct"
	// Copy: a plain file or directory sits at the target and is PROVEN a copy
	// of the source — a file byte-equal to it, or a directory whose every
	// file is byte-equal and which holds no extra entry — the shape the old
	// copy-based installer left behind. Ours to replace with a link.
	Copy State = "copy"
	// WrongTarget: a symlink exists but resolves somewhere else
	// INSIDE the source repository — stale (a rename, a re-rostered file),
	// still ours, safe to repoint.
	WrongTarget State = "wrong-target"
	// Conflict: a symlink resolving outside the source repository, a
	// directory sitting where a file link belongs (or vice versa), or a plain
	// entry that cannot be proven a copy of the source (a user's own file or
	// directory under a shipped name).
	// Never ours — reported, never overwritten, never deleted.
	Conflict State = "conflict"
	// Dangling: a symlink already points at the desired source
	// path, but nothing exists there anymore — the pfm-owned generated
	// directory a compiled Codex agent twin lives in was removed (partial
	// uninstall, a wiped state dir) while the registry link survived. Ours,
	// but a build cannot silently call this "correct": the link resolving to
	// the right NAME is not the same as an operator actually having the
	// agent. Apply repoints it exactly like Missing/WrongTarget —
	// the recompile step that runs first in RunGlobalAgents' build phase
	// recreates the missing file before this link is re-applied.
	Dangling State = "dangling"
)

// Kind selects whether the desired target is a single file link or
// a whole-directory link. It changes exactly one thing: whether an existing
// plain entry of the OTHER shape counts as "ours" (a copy) or as a conflict.
type Kind int

const (
	File Kind = iota
	Dir
)

// ClassifyLink inspects target without mutating the filesystem. A
// genuine stat/read failure (permission denied, a symlink loop — anything
// other than "nothing there") comes back as a non-nil error: an unreadable
// target is "we failed to look", never "0 entries", and must never be read
// as Missing.
func ClassifyLink(target, source, sourceRepoRoot string, kind Kind) (State, string, error) {
	info, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return Missing, "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("inspect %s: %w", target, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		raw, readErr := os.Readlink(target)
		if readErr != nil {
			return "", "", fmt.Errorf("read symlink %s: %w", target, readErr)
		}
		resolved := resolveLink(target, raw)
		if resolved == filepath.Clean(source) {
			if _, statErr := os.Stat(resolved); statErr != nil {
				if errors.Is(statErr, fs.ErrNotExist) {
					return Dangling, resolved, nil
				}
				return "", "", fmt.Errorf("inspect global link target %s: %w", resolved, statErr)
			}
			return Correct, resolved, nil
		}
		if withinRoot(resolved, sourceRepoRoot) {
			return WrongTarget, resolved, nil
		}
		return Conflict, resolved, nil
	}
	wantDir := kind == Dir
	if info.IsDir() != wantDir {
		return Conflict, "", nil
	}
	proven, err := provenCopy(target, source, wantDir)
	if err != nil {
		return "", "", err
	}
	if proven {
		return Copy, "", nil
	}
	return Conflict, "", nil
}

// provenCopy reports whether the plain entry at target is a copy of source:
// a file byte-equal to it, or a directory whose every file is byte-equal and
// which holds no entry the source lacks. A missing source proves nothing; a
// read failure is an error, never "not a copy".
func provenCopy(target, source string, isDir bool) (bool, error) {
	if !isDir {
		return equalFileBytes(target, source)
	}
	want, err := listTree(source)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	have, err := listTree(target)
	if err != nil {
		return false, err
	}
	if len(want) != len(have) {
		return false, nil
	}
	for relative, wantDir := range want {
		haveDir, found := have[relative]
		if !found || haveDir != wantDir {
			return false, nil
		}
		if wantDir {
			continue
		}
		same, err := equalFileBytes(filepath.Join(target, relative), filepath.Join(source, relative))
		if err != nil || !same {
			return false, err
		}
	}
	return true, nil
}

// listTree maps every entry below root (relative path) to whether it is a
// directory. A symlink inside the tree is listed as a non-directory entry
// whose bytes are compared through it by equalFileBytes; the source's own symlinks
// are followed, the target's are not trusted (see equalFileBytes).
func listTree(root string) (map[string]bool, error) {
	entries := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		entries[relative] = entry.IsDir()
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", root, err)
	}
	return entries, nil
}

// equalFileBytes reports whether target is a regular file (not a link) with the
// bytes of source.
func equalFileBytes(target, source string) (bool, error) {
	info, err := os.Lstat(target)
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", target, err)
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	want, err := os.ReadFile(source)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", source, err)
	}
	have, err := os.ReadFile(target)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", target, err)
	}
	return bytes.Equal(want, have), nil
}

// Apply keeps Correct and Conflict untouched and replaces proven copies and
// stale or dangling links, always by making the link beside the target and
// renaming it over (a directory copy: exchanging it), so readers see either
// the previous entry or the desired link and never an absent target.
func Apply(target, source string, state State) error {
	switch state {
	case Correct, Conflict:
		return nil
	case Missing, Copy, WrongTarget, Dangling:
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(target), err)
		}
		if state == Copy {
			if info, err := os.Lstat(target); err == nil && info.IsDir() {
				return atomicfile.SymlinkOverDir(target, source)
			}
		}
		return atomicfile.Symlink(target, source)
	default:
		return fmt.Errorf("apply global link %s: unknown state %q", target, state)
	}
}

// DescribeState renders the one operator-facing line every caller
// shows for a classified state, so install's transcript and its check
// preview never describe the same drift differently.
func DescribeState(state State, target, source, found string) string {
	switch state {
	case Missing:
		return "link " + target + " -> " + source
	case Correct:
		return target
	case Copy:
		return target + " is a copy where a link belongs — rerun pfm install (want -> " + source + ")"
	case WrongTarget:
		return target + " -> " + found + " (want -> " + source + ")"
	case Dangling:
		return "DANGLING " + target + " -> " + found + " (target does not exist — rerun pfm install)"
	case Conflict:
		if found == "" {
			return "CONFLICT " + target + ": not ours"
		}
		return "CONFLICT " + target + ": not ours (points to " + found + ")"
	default:
		return target
	}
}

func resolveLink(path, raw string) string {
	target := raw
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	return filepath.Clean(target)
}

func withinRoot(candidate, root string) bool {
	candidate = paths.PhysicalPath(candidate)
	root = paths.PhysicalPath(root)
	if candidate == root {
		return true
	}
	return strings.HasPrefix(candidate, root+string(filepath.Separator))
}
