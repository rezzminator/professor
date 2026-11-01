package codexmeta

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
)

// Rollout subtrees of a Codex home. The home itself also holds
// rollout-backup-*.jsonl copies, so a walk never starts at the home.
const (
	SessionsDir         = "sessions"
	ArchivedSessionsDir = "archived_sessions"
)

// A rollout file is rollout-*.jsonl while Codex writes it; Codex compresses a
// finished one in place to rollout-*.jsonl.zst and decompresses it back to
// plain before it writes to that thread again.
const (
	rolloutPrefix  = "rollout-"
	rolloutExt     = ".jsonl"
	compressedExt  = ".zst"
	compressedName = rolloutExt + compressedExt
)

// RolloutStem is name without its rollout extension, .jsonl or .jsonl.zst; ok
// is false for a name carrying neither.
func RolloutStem(name string) (stem string, ok bool) {
	return strings.CutSuffix(strings.TrimSuffix(name, compressedExt), rolloutExt)
}

// IsRolloutName reports whether name is a rollout file's base name, plain or
// compressed.
func IsRolloutName(name string) bool {
	_, ok := RolloutStem(name)
	return ok && strings.HasPrefix(name, rolloutPrefix)
}

// Compressed reports whether path names a compressed rollout.
func Compressed(path string) bool { return strings.HasSuffix(path, compressedName) }

// PlainTwin is the plain rollout path a compressed one decompresses to, its
// lines at the same byte offsets; any other path is itself.
func PlainTwin(path string) string {
	if !Compressed(path) {
		return path
	}
	return strings.TrimSuffix(path, compressedExt)
}

// PreferPlain drops from paths each compressed rollout whose plain twin is
// listed too: Codex writes only the plain file, so a pair means the plain one
// is current.
func PreferPlain(paths []string) []string {
	listed := make(map[string]bool, len(paths))
	for _, path := range paths {
		listed[path] = true
	}
	return slices.DeleteFunc(slices.Clone(paths), func(path string) bool { return shadowed(path, listed) })
}

// shadowed reports whether path is a compressed rollout whose plain twin is
// in listed.
func shadowed(path string, listed map[string]bool) bool {
	return Compressed(path) && listed[PlainTwin(path)]
}

// OpenRollout opens path to read its lines from the first byte: a compressed
// rollout is decompressed as it is read, any other file is the *os.File itself.
func OpenRollout(path string) (io.ReadCloser, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if !Compressed(path) {
		return file, nil
	}
	decoder, err := zstd.NewReader(file, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open compressed rollout %s: %w", path, err), file.Close())
	}
	return compressedRollout{decoder: decoder, file: file}, nil
}

// OpenRolloutAt is OpenRollout positioned at byte offset of the rollout's
// lines: a plain file seeks there, a compressed one decodes past it. An offset
// past the end reads nothing, as a plain file seeked past its end does.
func OpenRolloutAt(path string, offset int64) (io.ReadCloser, error) {
	rollout, err := OpenRollout(path)
	if err != nil {
		return nil, err
	}
	if seeker, ok := rollout.(io.Seeker); ok {
		_, err = seeker.Seek(offset, io.SeekStart)
	} else if _, err = io.CopyN(io.Discard, rollout, offset); errors.Is(err, io.EOF) {
		err = nil
	}
	if err != nil {
		return nil, errors.Join(fmt.Errorf("seek %s to %d: %w", path, offset, err), rollout.Close())
	}
	return rollout, nil
}

// compressedRollout is a compressed rollout open for reading; Close releases
// the decoder and the file.
type compressedRollout struct {
	decoder *zstd.Decoder
	file    *os.File
}

func (rollout compressedRollout) Read(p []byte) (int, error) { return rollout.decoder.Read(p) }

func (rollout compressedRollout) Close() error {
	rollout.decoder.Close()
	return rollout.file.Close()
}

// CompressRollout does to the rollout at path what Codex does to a finished
// one: writes it compressed to path.zst, then removes path. It returns the
// compressed path; the fake engine and tests use it to stand in for Codex.
func CompressRollout(path string) (string, error) {
	plain, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read rollout %s: %w", path, err)
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		return "", fmt.Errorf("compress rollout %s: %w", path, err)
	}
	compressed := path + compressedExt
	if err := os.WriteFile(compressed, encoder.EncodeAll(plain, nil), 0o600); err != nil {
		return "", fmt.Errorf("write compressed rollout %s: %w", compressed, err)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("remove compressed rollout's plain file %s: %w", path, err)
	}
	return compressed, nil
}

// DecompressRollout does what Codex does before it writes to a compressed
// rollout: writes it decompressed to its plain path, never over an existing
// file, then removes the compressed one. It returns the plain path.
func DecompressRollout(path string) (plain string, returnErr error) {
	if !Compressed(path) {
		return "", fmt.Errorf("decompress rollout %s: not a compressed rollout", path)
	}
	source, err := OpenRollout(path)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := source.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close compressed rollout %s: %w", path, err))
		}
	}()
	plain = PlainTwin(path)
	target, err := os.OpenFile(plain, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", fmt.Errorf("create decompressed rollout %s: %w", plain, err)
	}
	_, copyErr := io.Copy(target, source)
	if err := errors.Join(copyErr, target.Close()); err != nil {
		return "", fmt.Errorf("decompress rollout %s to %s: %w", path, plain, err)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("remove decompressed rollout's compressed file %s: %w", path, err)
	}
	return plain, nil
}

// Rollout is one rollout file, plain or compressed, a walk found.
type Rollout struct {
	Path    string
	Size    int64
	ModTime time.Time
}

// WalkRollouts lists every rollout file under each named subtree of codexHome,
// subtree by subtree in the order given, each sorted by path, a compressed
// rollout left out when its plain twin is there (PreferPlain). A
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
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !IsRolloutName(name) {
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
		listed := make(map[string]bool, len(found))
		for _, rollout := range found {
			listed[rollout.Path] = true
		}
		rollouts = append(rollouts, slices.DeleteFunc(found, func(rollout Rollout) bool {
			return shadowed(rollout.Path, listed)
		})...)
		if err != nil {
			return rollouts, append(errs, err)
		}
	}
	return rollouts, errs
}

// rolloutName is Codex's rollout file name, rollout-<local date>T<HH-MM-SS>-<thread id>.jsonl
// with .zst once compressed. The id class excludes JavaScript's line terminators, the Node engine's `.`.
var rolloutName = regexp.MustCompile(
	`^rollout-(\d{4}-\d{2}-\d{2})T(\d{2})-(\d{2})-(\d{2})-([^\n\r\x{2028}\x{2029}]+)\.jsonl(?:\.zst)?$`,
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
	if stem, ok := RolloutStem(name); ok {
		name = stem
	}
	return strings.TrimPrefix(name, rolloutPrefix)
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
