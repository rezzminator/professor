// Package archive moves chats out of both engines' sight, reversibly.
//
// A kill is a row in a list; an archive is the file itself leaving the tree.
// That distinction is the point: the killed list is rewritten by every writer
// that touches it, so two pickers racing each other lose each other's kills —
// the "my killed chats came back" bug. A chat that is not on disk cannot come
// back, and a manifest row puts it back exactly where it was when you ask.
//
// Nothing here ever deletes. Every move is recorded, and every recorded move
// reverses.
package archive

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"hostops/pfm/internal/atomicfile"
	"hostops/pfm/internal/clock"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/paths"
)

// sidechainMarker is the first-record field that tells a subagent's transcript
// from a chat's. It is read from the head of the file — the marker is in the
// first record — so classifying a gigabyte of transcripts costs one read each.
const sidechainMarker = `"isSidechain":true`

// headBytes is how much of a transcript is read to classify it.
const headBytes = 4096

func firstEngineRoot(roots []string) string {
	if len(roots) == 0 {
		return ""
	}
	return roots[0]
}

// KillStore is the killed set and the one way to leave it. Archive never
// rewrites the killed list itself.
type KillStore interface {
	Killed(ctx context.Context) ([]KilledChat, error)
	Unkill(ctx context.Context, id string) error
}

// KilledChat is one killed chat as the store reports it.
type KilledChat struct {
	ID     string
	Engine pfmengine.ID
}

// Options are one archive run's knobs.
type Options struct {
	// Apply performs the moves. Without it the run prints its plan and
	// touches nothing.
	Apply bool
	// Subagents archives sidechain transcripts instead of killed chats.
	Subagents bool
	// OlderThan is the age a sidechain transcript must reach before it is
	// archived. A subagent writes its transcript for as long as it runs, so
	// this age gate IS the liveness guard in subagent mode.
	OlderThan time.Duration
}

// Move is one planned or performed relocation.
type Move struct {
	ID      string
	Engine  string
	Source  string
	Target  string
	Bytes   int64
	Failed  string
	Applied bool
}

// Report is one run's outcome.
type Report struct {
	Moves []Move
	// Live are killed chats skipped because they are running. They leave the
	// killed list too: a chat the operator is still using belongs in the
	// picker, not filed away.
	Live []string
	// Orphans are killed ids whose file is already gone. Nothing to move, but
	// the kill is pruned — it points at nothing.
	Orphans []string
	// Unsupported are killed chats whose engine has durable storage that this
	// file-move archive cannot isolate. Their kill rows are deliberately kept:
	// retiring one would make the chat reappear, while moving OpenCode's shared
	// database would archive every OpenCode session at once.
	Unsupported []string
	// Young counts sidechain transcripts left alone as too new.
	Young int
	// Bytes is the total planned or moved.
	Bytes int64
	// Unkilled counts kill rows retired by this run.
	Unkilled int
	// SidecarBackups are the copies taken before any sidecar was rewritten.
	SidecarBackups []string
	// HistoryPruned and IndexPruned are the line counts dropped from
	// history.jsonl and the codex session index.
	HistoryPruned int
	IndexPruned   int
}

// Dependencies are the runner's collaborators.
type Dependencies struct {
	Paths            paths.Values
	Kills            KillStore
	Proc             gather.ProcFS
	Now              func() time.Time
	CodexBinary      string
	ExactClaudeRoots bool
}

// Runner performs one archive pass.
type Runner struct {
	paths            paths.Values
	kills            KillStore
	proc             gather.ProcFS
	now              func() time.Time
	codexBinary      string
	exactClaudeRoots bool
}

// New fills real implementations for anything omitted.
func New(dependencies Dependencies) (*Runner, error) {
	resolved := dependencies.Paths
	if resolved.Home == "" {
		var err error
		resolved, err = paths.Resolve()
		if err != nil {
			return nil, fmt.Errorf("resolve archive paths: %w", err)
		}
	}
	if dependencies.Kills == nil {
		return nil, errors.New("archive needs a killed-chat source")
	}
	proc := dependencies.Proc
	if proc == nil {
		proc = gather.NewProcFS(resolved.ProcRoot)
	}
	now := dependencies.Now
	if now == nil {
		now = clock.Real.Now
	}
	return &Runner{
		paths:            resolved,
		kills:            dependencies.Kills,
		proc:             proc,
		now:              now,
		codexBinary:      dependencies.CodexBinary,
		exactClaudeRoots: dependencies.ExactClaudeRoots,
	}, nil
}

// Run plans the archive and, with Apply, performs it.
func (runner *Runner) Run(
	ctx context.Context,
	options Options,
) (Report, error) {
	live := LiveSessions(
		runner.proc,
		firstEngineRoot(runner.paths.Roots[pfmengine.Codex]),
		runner.paths.SIDDir,
		runner.codexBinary,
	)
	if options.Subagents {
		return runner.runSubagents(options, live)
	}
	return runner.runKilled(ctx, options, live)
}

func (runner *Runner) runKilled(
	ctx context.Context,
	options Options,
	live map[string]struct{},
) (Report, error) {
	var report Report
	killed, err := runner.kills.Killed(ctx)
	if err != nil {
		return Report{}, fmt.Errorf("read the killed set: %w", err)
	}
	// decided is every id this run resolved one way or another — moved,
	// orphaned, or skipped as live. Those three leave the killed list;
	// unsupported engines remain killed and are reported separately.
	decided := make([]string, 0, len(killed))
	for _, chat := range killed {
		if chat.Engine == pfmengine.OpenCode {
			report.Unsupported = append(report.Unsupported, chat.ID)
			continue
		}
		decided = append(decided, chat.ID)
		if _, running := live[strings.ToLower(chat.ID)]; running {
			report.Live = append(report.Live, chat.ID)
			continue
		}
		source, engine := runner.findTranscript(chat)
		if source == "" {
			report.Orphans = append(report.Orphans, chat.ID)
			continue
		}
		info, err := os.Stat(source)
		if err != nil {
			report.Orphans = append(report.Orphans, chat.ID)
			continue
		}
		move := Move{
			ID:     chat.ID,
			Engine: string(engine),
			Source: source,
			Target: runner.targetFor(engine, source),
			Bytes:  info.Size(),
		}
		report.Moves = append(report.Moves, move)
		report.Bytes += move.Bytes
	}
	if !options.Apply {
		return report, nil
	}

	backups, err := runner.backupSidecars()
	if err != nil {
		return Report{}, err
	}
	report.SidecarBackups = backups

	moved := runner.performMoves(report.Moves)
	report.Moves = moved

	archived := make([]string, 0, len(moved))
	for _, move := range moved {
		if move.Applied {
			archived = append(archived, move.ID)
		}
	}
	pruned, err := runner.pruneLines(
		filepath.Join(runner.paths.Home, ".claude", "history.jsonl"),
		archived,
	)
	if err != nil {
		return Report{}, err
	}
	report.HistoryPruned = pruned
	pruned, err = runner.pruneLines(
		filepath.Join(firstEngineRoot(runner.paths.Roots[pfmengine.Codex]), "session_index.jsonl"),
		archived,
	)
	if err != nil {
		return Report{}, err
	}
	report.IndexPruned = pruned

	for _, id := range decided {
		if err := runner.kills.Unkill(ctx, id); err != nil {
			return Report{}, fmt.Errorf("retire the kill for %s: %w", id, err)
		}
		report.Unkilled++
	}
	return report, nil
}

func (runner *Runner) runSubagents(
	options Options,
	live map[string]struct{},
) (Report, error) {
	var report Report
	cutoff := runner.now().Add(-options.OlderThan)
	for _, root := range runner.paths.Roots[pfmengine.Claude] {
		err := filepath.WalkDir(root, func(
			path string,
			entry fs.DirEntry,
			err error,
		) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return nil
			}
			if info.ModTime().After(cutoff) {
				report.Young++
				return nil
			}
			if !isSidechain(path) {
				return nil
			}
			id := strings.TrimSuffix(entry.Name(), ".jsonl")
			if _, running := live[strings.ToLower(id)]; running {
				report.Live = append(report.Live, id)
				return nil
			}
			move := Move{
				ID:     id,
				Engine: "subagent",
				Source: path,
				Target: filepath.Join(
					runner.paths.ArchiveDir,
					"subagents",
					filepath.Base(filepath.Dir(path)),
					entry.Name(),
				),
				Bytes: info.Size(),
			}
			report.Moves = append(report.Moves, move)
			report.Bytes += move.Bytes
			return nil
		})
		if err != nil {
			return Report{}, fmt.Errorf("scan %s for sidechains: %w", root, err)
		}
	}
	sort.Slice(report.Moves, func(left, right int) bool {
		return report.Moves[left].Source < report.Moves[right].Source
	})
	if !options.Apply {
		return report, nil
	}
	// Subagent mode touches no sidecar: a sidechain transcript has no kill
	// row, no history prompt (nobody typed into it) and no codex index row,
	// so there is nothing to prune and nothing to back up.
	report.Moves = runner.performMoves(report.Moves)
	return report, nil
}

// findTranscript resolves a killed chat to the file that holds it. The store's
// engine is the hint, never the authority: a kill written before the lineage
// was indexed carries no engine at all.
func (runner *Runner) findTranscript(chat KilledChat) (string, pfmengine.ID) {
	if chat.Engine != pfmengine.Codex {
		if path := runner.findClaudeTranscript(chat.ID); path != "" {
			return path, pfmengine.Claude
		}
	}
	if path := runner.findCodexRollout(chat.ID); path != "" {
		return path, pfmengine.Codex
	}
	if chat.Engine == pfmengine.Codex {
		if path := runner.findClaudeTranscript(chat.ID); path != "" {
			return path, pfmengine.Claude
		}
	}
	return "", ""
}

func (runner *Runner) findClaudeTranscript(id string) string {
	for _, root := range runner.claudeRoots() {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			candidate := filepath.Join(root, entry.Name(), id+".jsonl")
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	return ""
}

func (runner *Runner) findCodexRollout(id string) string {
	found := ""
	root := filepath.Join(firstEngineRoot(runner.paths.Roots[pfmengine.Codex]), "sessions")
	_ = filepath.WalkDir(root, func(
		path string,
		entry fs.DirEntry,
		err error,
	) error {
		if err != nil || entry.IsDir() || found != "" {
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".jsonl") &&
			strings.Contains(entry.Name(), id) {
			found = path
		}
		return nil
	})
	return found
}

// claudeRoots is every account's projects directory, plus the default account
// spelled its other way: ~/.cc/1 is a symlink to ~/.claude on this machine,
// and a kill written through one spelling must resolve through the other.
func (runner *Runner) claudeRoots() []string {
	roots := append(
		[]string(nil),
		runner.paths.Roots[pfmengine.Claude]...,
	)
	if !runner.exactClaudeRoots {
		roots = append(roots, filepath.Join(runner.paths.Home, ".claude", "projects"))
	}
	return roots
}

// targetFor mirrors the source layout under the archive, so a restore is an
// exact reverse rather than a guess about where a file came from.
func (runner *Runner) targetFor(engine pfmengine.ID, source string) string {
	if engine == pfmengine.Codex {
		return filepath.Join(
			runner.paths.ArchiveDir,
			pfmengine.MustLookup(pfmengine.Codex).LongName,
			filepath.Base(source),
		)
	}
	return filepath.Join(
		runner.paths.ArchiveDir,
		pfmengine.MustLookup(pfmengine.Claude).LongName,
		filepath.Base(filepath.Dir(source)),
		filepath.Base(source),
	)
}

// performMoves relocates each planned file and records it in the manifest as
// it goes, so an interrupted run leaves a manifest describing exactly the
// moves it finished.
func (runner *Runner) performMoves(moves []Move) []Move {
	stamp := runner.now().UTC().Format("20060102-150405")
	applied := make([]Move, 0, len(moves))
	for _, move := range moves {
		if err := os.MkdirAll(filepath.Dir(move.Target), 0o700); err != nil {
			move.Failed = err.Error()
			applied = append(applied, move)
			continue
		}
		if _, err := os.Stat(move.Target); err == nil {
			move.Failed = "a file is already archived under that name"
			applied = append(applied, move)
			continue
		}
		if err := moveFile(move.Source, move.Target); err != nil {
			move.Failed = err.Error()
			applied = append(applied, move)
			continue
		}
		move.Applied = true
		if err := AppendManifest(runner.paths.ArchiveDir, move, stamp); err != nil {
			// The file moved; the record of it did not. Say so — a move with
			// no manifest row is the one move that cannot be undone.
			move.Failed = "moved, but the manifest row failed: " + err.Error()
		}
		applied = append(applied, move)
	}
	return applied
}

// backupSidecars copies every file this run may rewrite, before it rewrites
// any of them.
func (runner *Runner) backupSidecars() ([]string, error) {
	directory := filepath.Join(runner.paths.ArchiveDir, "_sidecar-backups")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("create the sidecar backup directory: %w", err)
	}
	stamp := runner.now().UTC().Format("20060102-150405")
	sources := []string{
		filepath.Join(runner.paths.Home, ".claude", "history.jsonl"),
		filepath.Join(firstEngineRoot(runner.paths.Roots[pfmengine.Codex]), "session_index.jsonl"),
	}
	backups := make([]string, 0, len(sources))
	for _, source := range sources {
		content, err := os.ReadFile(source)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read %s for backup: %w", source, err)
		}
		target := filepath.Join(
			directory,
			filepath.Base(source)+"."+stamp,
		)
		if err := atomicfile.Write(target, content, 0o600); err != nil {
			return nil, fmt.Errorf("write %s: %w", target, err)
		}
		backups = append(backups, target)
	}
	return backups, nil
}

// pruneLines drops every line naming an archived id. The file is rewritten
// through a temporary beside it, so an interrupted prune leaves the original.
func (runner *Runner) pruneLines(path string, ids []string) (dropped int, returnErr error) {
	if len(ids) == 0 {
		return 0, nil
	}
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close %s: %w", path, err))
		}
	}()

	wanted := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		wanted[strings.ToLower(id)] = struct{}{}
	}
	dropped = 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var content bytes.Buffer
	for scanner.Scan() {
		line := scanner.Text()
		archived := false
		for _, id := range uuidPattern.FindAllString(line, -1) {
			if _, found := wanted[strings.ToLower(id)]; found {
				archived = true
				break
			}
		}
		if archived {
			dropped++
			continue
		}
		if _, err := content.WriteString(line + "\n"); err != nil {
			return 0, fmt.Errorf("buffer %s: %w", path, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	if dropped == 0 {
		return 0, nil
	}
	if err := atomicfile.Write(path, content.Bytes(), 0o600); err != nil {
		return 0, fmt.Errorf("replace %s: %w", path, err)
	}
	return dropped, nil
}

// isSidechain classifies a transcript from its head alone.
func isSidechain(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() {
		if err := file.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "archive: close sidechain transcript %s: %v\n", path, err)
		}
	}()
	head := make([]byte, headBytes)
	read, err := file.Read(head)
	if read <= 0 && err != nil {
		return false
	}
	return strings.Contains(string(head[:read]), sidechainMarker)
}

// moveFile relocates a file, falling back to copy-and-remove when the archive
// sits on another filesystem.
func moveFile(source, target string) error {
	if err := os.Rename(source, target); err == nil {
		return nil
	}
	content, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read %s: %w", source, err)
	}
	if err := atomicfile.Write(target, content, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", target, err)
	}
	if err := os.Remove(source); err != nil {
		return fmt.Errorf("remove %s after copying it: %w", source, err)
	}
	return nil
}

// FormatBytes renders a size for the report.
func FormatBytes(bytes int64) string {
	return strconv.FormatFloat(float64(bytes)/(1024*1024), 'f', 1, 64) + " MB"
}
