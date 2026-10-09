// Package globallink classifies and repairs the symlinks pfm install wires
// from the Professor clone and the skill store into the Claude registries and
// ~/.agents/skills: one vocabulary for every registry, so a drift never reads
// differently depending on which registry found it. engine-sync keeps its own
// copy for the Codex and Claude agent links it writes.
package globallink

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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
	// Copy: a plain file or directory sits at the target — the
	// shape the old copy-based installer left behind, or one just like it.
	// Its basename is the roster entry itself (that is what put a target
	// here at all), so it is ours to replace with a link.
	Copy State = "copy"
	// WrongTarget: a symlink exists but resolves somewhere else
	// INSIDE the source repository — stale (a rename, a re-rostered file),
	// still ours, safe to repoint.
	WrongTarget State = "wrong-target"
	// Conflict: a symlink resolving outside the source repository,
	// or a directory sitting where a file link belongs (or vice versa).
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
	if info.IsDir() == wantDir {
		return Copy, "", nil
	}
	return Conflict, "", nil
}

// Apply performs the filesystem change ClassifyLink's state
// recommends. Correct and Conflict are both no-ops — the first because
// nothing is wrong, the second because a conflict is never touched. Missing,
// Copy, and WrongTarget all end the same way: whatever is there (nothing, a
// plain copy, or a stale symlink) is cleared and the desired symlink takes
// its place. RemoveAll is safe here specifically because Copy only ever
// classifies an entry whose TYPE matches the desired link's kind (a
// same-shape copy), and WrongTarget only ever classifies a symlink — never a
// foreign directory, which conflict leaves alone before this is reached.
func Apply(target, source string, state State) error {
	switch state {
	case Correct, Conflict:
		return nil
	case Missing, Copy, WrongTarget, Dangling:
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(target), err)
		}
		if state != Missing {
			if err := os.RemoveAll(target); err != nil {
				return fmt.Errorf("remove %s: %w", target, err)
			}
		}
		if err := os.Symlink(source, target); err != nil {
			return fmt.Errorf("link %s -> %s: %w", target, source, err)
		}
		return nil
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
