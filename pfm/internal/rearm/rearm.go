// Package rearm remembers which registered role a `--role` seat was born
// with, and re-arms that binding after every pfm-initiated context reset:
// `pfm chat reload` and the chat_self_compact MCP tool.
//
// Birth (agentrole.Resolve, folded into the launch prompt by
// cmd/pfm/chat_new_command.go's composeRolePrompt) reads the FULL constitution
// text exactly once, at the cheapest moment — before any goal. That binding
// lives inside the session's first user turn, and a user turn is
// compactable. A forensic pass over this host's real Codex rollout corpus —
// 512 rollout files carrying a "type":"compacted" record, 1808 such records
// total, 185 examined — found the session's FIRST long user turn surviving
// into payload.replacement_history verbatim ZERO times: 92 came back
// partial or paraphrased, 88 were gone outright. Birth's binding provably
// does not survive the reset re-arm exists to survive.
//
// Every re-arm after birth is a SHORT POINTER, never the full text again,
// above a size threshold — full text at or under it. Five compaction
// generations of a 10KB role constitution is the cost a pointer avoids; the
// pointer also has the seat re-read the CURRENT artifact, so a role edited
// mid-life re-arms to the live text with no second copy on disk to drift.
//
// Storage is a crumb file, not a store migration: internal/store migrations
// are one-way (no downgrade path), and a schema bump for state whose whole
// lifetime is one tmux session would be irreversible for nothing. The crumb
// is named, and read, the way pfm's existing SID crumbs already are (see
// internal/resolve/resolve.go's sameChatWinner): a pane-scoped
// "<socket>.<paneID>" name is tried first, falling back to the bare
// "<socket>" name.
package rearm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"hostops/pfm/internal/agentrole"
	"hostops/pfm/internal/atomicfile"
)

// crumbPrefix distinguishes a role re-arm crumb from the other files pfm
// already keeps in the same SID directory: the CC session-id crumb chat.sh's
// own hooks write there (a bare "<socket>" file whose content is a
// transcript path) and pfm's own "reload-<socket>.log" worker logs
// (cmd/pfm/chat_reload_command.go). Without a distinct prefix a role crumb would
// collide with, or be mistaken for, either of those.
const crumbPrefix = "role-"

// DefaultThresholdBytes is the day-one re-arm size threshold: a
// constitution whose resolved text is at or under this many bytes re-arms
// with its FULL text; above it, with a short pointer. It is ruled as a
// PARAMETER, never hardcoded inside Pointer, so a later config layer can
// override it without changing that function's shape. Measured examples
// that set the default: dev (2.6KB) and rr (3.3KB) re-arm full-text;
// reviewer (10.5KB) re-arms by pointer.
const DefaultThresholdBytes = 4096

// Crumb is everything a re-arm needs to remember about one --role seat: the
// role name, the exact artifact agentrole.Resolve read at birth, and
// whether that artifact's constitution is a TOML value
// (agentrole.DeveloperInstructionsKey) or the whole file. Re-arm points at
// the SAME rung birth used — re-resolving independently could land on a
// different rung if the ladder changed under it.
type Crumb struct {
	Role         string
	ArtifactPath string
	TOMLKey      bool
}

// crumbFile is Crumb's on-disk shape.
type crumbFile struct {
	Role         string `json:"role"`
	ArtifactPath string `json:"artifact_path"`
	TOMLKey      bool   `json:"toml_key"`
}

// crumbPaths returns the pane-scoped path first, then the bare-socket
// fallback — the same two-rung shape resolve.go's sameChatWinner reads SID
// crumbs with. WriteCrumb only ever writes the bare-socket form (a --role
// seat is born owning its own fresh tmux socket, not sharing a pane on one
// pfm already has open), but a future writer that DOES know the pane can
// drop a pane-scoped crumb into the same directory and this reader picks it
// up first with no change here.
func crumbPaths(sidDir, socket, paneID string) []string {
	paths := make([]string, 0, 2)
	if paneID != "" {
		paths = append(paths, filepath.Join(sidDir, crumbPrefix+socket+"."+paneID))
	}
	paths = append(paths, filepath.Join(sidDir, crumbPrefix+socket))
	return paths
}

// WriteCrumb persists crumb for socket, keyed the bare-socket way — see
// crumbPaths. Called once, at `chat new --role` time, once the seat is
// confirmed live.
func WriteCrumb(sidDir, socket string, crumb Crumb) error {
	if strings.TrimSpace(sidDir) == "" {
		return errors.New("role re-arm: sid directory is empty")
	}
	if strings.TrimSpace(socket) == "" {
		return errors.New("role re-arm: socket is empty")
	}
	if strings.TrimSpace(crumb.Role) == "" {
		return errors.New("role re-arm: crumb role is empty")
	}
	if !filepath.IsAbs(crumb.ArtifactPath) {
		return fmt.Errorf("role re-arm: crumb artifact path %q is not absolute", crumb.ArtifactPath)
	}
	raw, err := json.Marshal(crumbFile(crumb))
	if err != nil {
		return fmt.Errorf("role re-arm: encode crumb: %w", err)
	}
	path := filepath.Join(sidDir, crumbPrefix+socket)
	if err := atomicfile.Write(path, raw, 0o600); err != nil {
		return fmt.Errorf("role re-arm: write crumb %s: %w", path, err)
	}
	return nil
}

// ReadCrumb reads socket's (and, if paneID is known, its pane-scoped) role
// crumb. The three return states are never collapsed into each other:
//
//   - (Crumb{}, false, nil): no crumb file exists at either path — this seat
//     never had --role, or never had it remembered. Callers behave exactly
//     as they did before this feature existed: no pointer, no warning.
//   - (Crumb{}, false, err): a crumb file IS there and could not be read or
//     parsed (permissions, I/O, corruption) — a real error naming the path,
//     never silently folded into the no-crumb case above.
//   - (crumb, true, nil): a live remembered role.
func ReadCrumb(sidDir, socket, paneID string) (Crumb, bool, error) {
	if strings.TrimSpace(sidDir) == "" {
		return Crumb{}, false, errors.New("role re-arm: sid directory is empty")
	}
	if strings.TrimSpace(socket) == "" {
		return Crumb{}, false, errors.New("role re-arm: socket is empty")
	}
	for _, path := range crumbPaths(sidDir, socket, paneID) {
		raw, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Crumb{}, false, fmt.Errorf("role re-arm: read crumb %s: %w", path, err)
		}
		var decoded crumbFile
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return Crumb{}, false, fmt.Errorf("role re-arm: parse crumb %s: %w", path, err)
		}
		if strings.TrimSpace(decoded.Role) == "" || strings.TrimSpace(decoded.ArtifactPath) == "" {
			return Crumb{}, false, fmt.Errorf("role re-arm: crumb %s is missing its role or artifact path", path)
		}
		return Crumb(decoded), true, nil
	}
	return Crumb{}, false, nil
}

// RemoveCrumb best-effort deletes socket's role crumb — both the
// pane-scoped and bare-socket forms crumbPaths can produce — on the
// canonical kill path (cmd/pfm/chat_command.go's runChatEnd, at its tmux
// kill-server call). A dead socket's crumb is never read again by anything;
// left in place it is pure litter, one file per --role seat ever launched,
// accumulating forever in SIDDir. Only WriteCrumb's bare-socket form exists
// today, but this removes both shapes on the same footing ReadCrumb reads
// them on, so a future pane-scoped writer needs no matching change here.
//
// A path that was never written (fs.ErrNotExist) is not an error — that
// crumb form simply doesn't exist, the ordinary case for the pane-scoped
// form. Errors from the two removal attempts are joined so a caller sees
// everything that went wrong, not just the first; callers on the kill path
// are expected to log this as a WARNING and never fail the kill over it —
// the chat is dead either way.
func RemoveCrumb(sidDir, socket, paneID string) error {
	var errs []error
	for _, path := range crumbPaths(sidDir, socket, paneID) {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("role re-arm: remove crumb %s: %w", path, err))
		}
	}
	return errors.Join(errs...)
}

// Pointer composes the re-arm text for crumb — the single writer both reset
// paths (cmd/pfm/chat_reload_command.go and internal/inject/engine.go's
// ScheduleAfterCurrentTurn) compose their steer through, mirroring the
// existing inject.SelfCompactStopNotice precedent of one constant text
// every caller inherits.
//
// It re-reads the artifact's CURRENT text (never a cached copy from birth)
// through agentrole.ReadArtifact, using the exact path and TOMLKey bit the
// crumb recorded, so re-arm points at the same rung birth used. At or under
// thresholdBytes of resolved text it returns that text in full; above it, a
// short pointer naming the artifact (and, for a TOML seat, the
// developer_instructions key inside it) instead. It never returns an empty
// string: an artifact that no longer exists, or can no longer be read,
// renders as a VISIBLE failure the seat can act on — a seat that believes
// it is still bound and is not is worse than one told plainly its role file
// is gone.
func Pointer(crumb Crumb, thresholdBytes int) string {
	text, err := agentrole.ReadArtifact(crumb.ArtifactPath, crumb.TOMLKey)
	if err != nil {
		return fmt.Sprintf(
			"ROLE RE-ARM FAILED for %s: %v — say plainly that this seat's role binding may be "+
				"stale rather than continuing to act as %s unverified.",
			crumb.Role, err, crumb.Role,
		)
	}
	if len(text) <= thresholdBytes {
		return fmt.Sprintf(
			"you are still %s — re-armed with your full constitution:\n\n%s",
			crumb.Role, text,
		)
	}
	reference := crumb.ArtifactPath
	if crumb.TOMLKey {
		reference = fmt.Sprintf("the %s value in %s", agentrole.DeveloperInstructionsKey, crumb.ArtifactPath)
	}
	return fmt.Sprintf(
		"you are still %s — read %s in full and follow it as your standing constitution; acknowledge by rule count.",
		crumb.Role, reference,
	)
}
