package codexgen

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// A machine-global Codex role is a REGULAR FILE at {codex home}/agents/<name>.toml,
// never a symlink. Codex loads a role through read_sensitive_file_to_string
// (codex-rs/exec-server/src/regular_file.rs), which opens with O_NOFOLLOW and
// rejects anything whose final component is a symlink; codex-rs/core/src/agent/role.rs
// maps that rejection to the single vague line "agent type is currently not
// available". A symlinked role is therefore not an installed role at all — it
// is a role every spawn refuses — so this file owns the real-file install and
// the ownership rule that makes it safe.
//
// Ownership is the generated marker, not the path: pfm writes every role file
// with globalRoleHeader as its first line, so a regular file WITHOUT that line
// is an operator's own role and is never overwritten, whatever its name. This
// is the same marker retireRenamedCodexAgents already tests for.

// globalRoleMarkerPrefix is what the first line of every pfm-written role file
// starts with. It is a prefix, not the whole line, because the line also names
// the source the role was compiled from.
const globalRoleMarkerPrefix = "# " + generatedMarker + " from "

// GeneratedGlobalRole reports whether content's FIRST line is a pfm generated
// marker — today's role header or the one the retired build-codex.mjs wrote.
// The check is anchored to the first line on purpose: a role body that merely
// mentions the marker phrase is not a file pfm owns, and treating it as one
// would hand an operator's own role to the retire path.
func GeneratedGlobalRole(content []byte) bool {
	first, _, _ := strings.Cut(string(content), "\n")
	return strings.HasPrefix(first, globalRoleMarkerPrefix) ||
		strings.HasPrefix(first, "# "+legacyGeneratedMarker)
}

// globalRoleHeader is the ownership proof pfm stamps on a role file. TOML
// treats it as a comment, so Codex parses the file exactly as it would without
// it.
func globalRoleHeader(source string) string {
	return "# " + generatedLine(source) + "\n"
}

// ErrGlobalRoleNotRegular is what ReadGlobalRoleFile returns for a path Codex
// itself would refuse: a symlink, a directory, a device. It is distinct from
// fs.ErrNotExist on purpose — "Codex will not load this" and "there is nothing
// here" are different findings and must never render as one.
var ErrGlobalRoleNotRegular = errors.New("not a regular file — Codex refuses to load it")

// ReadGlobalRoleFile opens path exactly the way Codex's role loader does:
// O_NOFOLLOW on the final component, then a regular-file check on the open
// descriptor. A caller that used os.ReadFile instead would follow the symlink,
// read the bytes Codex never sees, and certify a role that cannot spawn.
func ReadGlobalRoleFile(path string) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		// O_NOFOLLOW on a symlink is ELOOP on both supported platforms;
		// surface it as the refusal it is rather than a raw errno.
		if errors.Is(err, syscall.ELOOP) {
			return nil, fmt.Errorf("open %s: %w", path, ErrGlobalRoleNotRegular)
		}
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// The read is the whole purpose; a close error on a read-only descriptor
	// tells the caller nothing it can act on, so it is deliberately dropped.
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("inspect %s: %w", path, ErrGlobalRoleNotRegular)
	}
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return content, nil
}

// GlobalRoleState is the on-disk shape found at one desired role file,
// classified against the bytes this binary compiles for it.
type GlobalRoleState string

const (
	// GlobalRoleMissing: nothing exists at the target yet.
	GlobalRoleMissing GlobalRoleState = "missing"
	// GlobalRoleCurrent: a pfm-written regular file whose bytes already match.
	GlobalRoleCurrent GlobalRoleState = "current"
	// GlobalRoleStale: a pfm-written regular file whose bytes differ — ours to
	// rewrite.
	GlobalRoleStale GlobalRoleState = "stale"
	// GlobalRoleOwnedLink: a symlink pfm itself installed, back when roles
	// were linked into the registry. Ours to replace with the real file; this
	// is the migration every host that ran the symlinking installer needs.
	GlobalRoleOwnedLink GlobalRoleState = "owned-link"
	// GlobalRoleForeign: a regular file with no generated marker, a symlink
	// pointing somewhere pfm never wrote, or an entry of another type
	// entirely. Never ours — reported, never overwritten, never deleted.
	GlobalRoleForeign GlobalRoleState = "foreign"
)

// ClassifyGlobalRole inspects target without mutating the filesystem. found
// carries a symlink's resolved target and is empty otherwise. A stat or read
// failure other than "nothing there" comes back as a non-nil error: an
// unreadable target is "we failed to look", never GlobalRoleMissing.
func ClassifyGlobalRole(target string, want []byte, ownedLinkDirs []string) (GlobalRoleState, string, error) {
	info, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return GlobalRoleMissing, "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("inspect %s: %w", target, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		raw, readErr := os.Readlink(target)
		if readErr != nil {
			return "", "", fmt.Errorf("read symlink %s: %w", target, readErr)
		}
		resolved := resolveGlobalLink(target, raw)
		for _, dir := range ownedLinkDirs {
			if withinGlobalLinkRoot(resolved, dir) {
				return GlobalRoleOwnedLink, resolved, nil
			}
		}
		return GlobalRoleForeign, resolved, nil
	}
	if !info.Mode().IsRegular() {
		return GlobalRoleForeign, "", nil
	}
	found, err := os.ReadFile(target)
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", target, err)
	}
	if !GeneratedGlobalRole(found) {
		return GlobalRoleForeign, "", nil
	}
	if bytes.Equal(found, want) {
		return GlobalRoleCurrent, "", nil
	}
	return GlobalRoleStale, "", nil
}

// ApplyGlobalRole performs the filesystem change ClassifyGlobalRole's state
// recommends. Current and Foreign are both no-ops — the first because nothing
// is wrong, the second because a role pfm does not own is never touched.
// Missing, Stale and OwnedLink all end the same way: whatever is there is
// cleared and the compiled bytes are written as a regular file.
func ApplyGlobalRole(target string, content []byte, state GlobalRoleState) error {
	switch state {
	case GlobalRoleCurrent, GlobalRoleForeign:
		return nil
	case GlobalRoleMissing, GlobalRoleStale, GlobalRoleOwnedLink:
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(target), err)
		}
		// Remove first: a symlink left in place would be followed by
		// os.WriteFile, writing through to the link's target and leaving the
		// registry entry a symlink Codex still refuses.
		if state != GlobalRoleMissing {
			if err := os.Remove(target); err != nil {
				return fmt.Errorf("remove %s: %w", target, err)
			}
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
		return nil
	default:
		return fmt.Errorf("apply global role %s: unknown state %q", target, state)
	}
}

// Describe renders the one operator-facing line every caller shows for a
// classified role, so install's transcript and the CLI can never word the same
// drift two ways.
func (role GlobalRoleInstalled) Describe() string {
	state, target, found := role.State, role.Path, role.Found
	switch state {
	case GlobalRoleMissing, GlobalRoleStale:
		return "write " + target
	case GlobalRoleCurrent:
		return target
	case GlobalRoleOwnedLink:
		return "replace symlink " + target + " -> " + found + " with the role file Codex can load"
	case GlobalRoleForeign:
		if found == "" {
			return "CONFLICT " + target + ": not ours (no pfm generated marker); preserved"
		}
		return "CONFLICT " + target + ": not ours (points to " + found + "); preserved"
	default:
		return target
	}
}
