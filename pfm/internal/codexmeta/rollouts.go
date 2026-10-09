package codexmeta

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Rollout subtrees of a Codex home. The home itself also holds
// rollout-backup-*.jsonl copies, so a walk never starts at the home.
const (
	SessionsDir         = "sessions"
	ArchivedSessionsDir = "archived_sessions"
)

// Rollout is one rollout-*.jsonl file a walk found.
type Rollout struct {
	Path    string
	Size    int64
	ModTime time.Time
}

// WalkRollouts lists every rollout-*.jsonl under each named subtree of
// codexHome, subtree by subtree in the order given, each sorted by path. A
// missing subtree holds nothing. A directory or file that cannot be read is
// one entry of errs and the walk goes on, so the caller holds every rollout it
// could reach and every place it failed to look; a cancelled ctx ends the walk
// with ctx's error last. Symlinked files are skipped: a link is a second name
// for a rollout the walk already counts.
func WalkRollouts(ctx context.Context, codexHome string, subtrees ...string) (rollouts []Rollout, errs []error) {
	for _, subtree := range subtrees {
		root := filepath.Join(codexHome, subtree)
		absolute, err := filepath.Abs(root)
		if err != nil {
			errs = append(errs, fmt.Errorf("make Codex rollout root %q absolute: %w", root, err))
			continue
		}
		resolved, err := filepath.EvalSymlinks(absolute)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("resolve Codex rollout root %q: %w", root, err))
			continue
		}
		var found []Rollout
		err = filepath.WalkDir(resolved, func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				errs = append(errs, walkErr)
				return nil
			}
			name := entry.Name()
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 ||
				!strings.HasPrefix(name, "rollout-") || filepath.Ext(name) != ".jsonl" {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				errs = append(errs, fmt.Errorf("stat %q: %w", path, err))
				return nil
			}
			found = append(found, Rollout{Path: path, Size: info.Size(), ModTime: info.ModTime()})
			return nil
		})
		sort.Slice(found, func(left, right int) bool { return found[left].Path < found[right].Path })
		rollouts = append(rollouts, found...)
		if err != nil {
			return rollouts, append(errs, err)
		}
	}
	return rollouts, errs
}

// rolloutName is Codex's rollout file name, rollout-<local date>T<HH-MM-SS>-<thread id>.jsonl.
// The id class excludes JavaScript's line terminators, the Node engine's `.`.
var rolloutName = regexp.MustCompile(
	`^rollout-(\d{4}-\d{2}-\d{2})T(\d{2})-(\d{2})-(\d{2})-([^\n\r\x{2028}\x{2029}]+)\.jsonl$`,
)

// ParseRolloutName reads a rollout file's base name: the thread id, and the
// start stamp, which Codex writes in the host's local time (loc). ok is false
// for a name off the pattern; stamped is false when the stamp is no real
// instant.
func ParseRolloutName(name string, loc *time.Location) (id string, started time.Time, stamped, ok bool) {
	match := rolloutName.FindStringSubmatch(name)
	if match == nil {
		return "", time.Time{}, false, false
	}
	started, err := time.ParseInLocation("2006-01-02T15:04:05", match[1]+"T"+match[2]+":"+match[3]+":"+match[4], loc)
	return match[5], started, err == nil, true
}

// RolloutID is the thread id a rollout's file name carries, or the name's
// stem without its rollout- prefix when the name is off the pattern.
func RolloutID(name string) string {
	if id, _, _, ok := ParseRolloutName(name, time.UTC); ok {
		return id
	}
	return strings.TrimPrefix(strings.TrimSuffix(name, ".jsonl"), "rollout-")
}

// Unreadable is a rollout whose header could not be read.
type Unreadable struct {
	Path string
	Err  error
}

// AgentPathRollouts lists the rollouts whose session_meta names agentPath (an
// orchestrator's /root/{name} for its sub-agent), over each Codex home's
// sessions and archived_sessions and each extra directory walked whole, in
// walk order. A rollout whose header cannot be read is unreadable, never a
// miss; a place the walk failed to look is one entry of errs.
func AgentPathRollouts(
	ctx context.Context,
	homes, extra []string,
	agentPath string,
) (hits []string, unreadable []Unreadable, errs []error) {
	var rollouts []Rollout
	for _, home := range homes {
		found, walkErrs := WalkRollouts(ctx, home, SessionsDir, ArchivedSessionsDir)
		rollouts, errs = append(rollouts, found...), append(errs, walkErrs...)
	}
	for _, dir := range extra {
		found, walkErrs := WalkRollouts(ctx, dir, ".")
		rollouts, errs = append(rollouts, found...), append(errs, walkErrs...)
	}
	seen := map[string]bool{}
	for _, rollout := range rollouts {
		if seen[rollout.Path] {
			continue
		}
		seen[rollout.Path] = true
		header, err := ReadHeader(rollout.Path)
		switch {
		case errors.Is(err, ErrNoSessionMeta):
		case err != nil:
			unreadable = append(unreadable, Unreadable{Path: rollout.Path, Err: err})
		case header.AgentPath == agentPath:
			hits = append(hits, rollout.Path)
		}
	}
	return hits, unreadable, errs
}
