package statusline

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/transcript"
	"github.com/rezzminator/professor/pfm/internal/usagehook"
)

// scanCheckpointVersion names the checkpoint format; a file of any other
// version is read as no checkpoint, so the fold starts from the top.
const scanCheckpointVersion = 1

// scanBudget is the wall time one render spends folding transcript bytes it
// has not folded before, every transcript of the render together, checked
// before each line (a line begun is read whole). Claude Code kills a status
// line command at 5 s; a render that folds nothing takes under a second, the
// checkpoint loads and writes included, so 2 s of folding leaves a loaded host
// room before the kill. At roughly 100 MB/s a first render of a 1 GB session
// folds a fifth of it and each later one resumes where it stopped.
const scanBudget = 2 * time.Second

// scanCheckpointDir is the directory under the cache dir holding one
// checkpoint per transcript and fold, suffixed with the uid that owns it; it
// must be that uid's own real directory (usagehook.EnsurePrivateDir), else no
// checkpoint is read or kept.
const scanCheckpointDir = "cc-sl-scan"

const (
	// scanCheckpointMaxAge is how long a checkpoint no render rewrote is kept:
	// its transcript ended or went, or a render was killed mid-write.
	scanCheckpointMaxAge = 24 * time.Hour
	// scanCheckpointMaxBytes caps the checkpoints together (the cache dir is
	// often tmpfs, so they cost memory); past it the oldest go first.
	scanCheckpointMaxBytes = 256 << 20
	// scanPruneEvery spaces the prunes: the stamp file's mtime is the last.
	scanPruneEvery = time.Hour
	scanPruneStamp = ".pruned"
)

// transcriptScans resumes the render's whole-transcript folds from their
// checkpoints under dir and spends one shared budget on the bytes past them.
// The zero value keeps no checkpoint and has no deadline: every fold reads its
// whole file.
type transcriptScans struct {
	dir      string
	deadline time.Time
	warn     io.Writer
}

// newTranscriptScans keeps its checkpoints under cacheDir, pruning them, and
// spends scanBudget from now, the wall clock: a fixed render clock must not
// stop a fold. A checkpoint directory this uid does not own privately is told
// on warn and keeps no checkpoint: every fold reads from the top, still within
// the budget.
func newTranscriptScans(cacheDir string, warn io.Writer) transcriptScans {
	if warn == nil {
		warn = os.Stderr
	}
	if cacheDir == "" {
		return transcriptScans{warn: warn}
	}
	now := clock.Real.Now()
	scans := transcriptScans{deadline: now.Add(scanBudget), warn: warn}
	dir := filepath.Join(cacheDir, scanCheckpointDir+"-"+strconv.Itoa(os.Getuid()))
	if err := usagehook.EnsurePrivateDir(dir); err != nil {
		fmt.Fprintf(warn, "statusline: scan checkpoint directory refused (reading transcripts from the top): %v\n", err)
		return scans
	}
	scans.dir = dir
	pruneScanCheckpoints(dir, now, scanCheckpointMaxAge, scanCheckpointMaxBytes, warn)
	return scans
}

// pruneScanCheckpoints removes, at most once per scanPruneEvery, every file in
// dir not written for maxAge, then the oldest of the rest until they fit in
// maxBytes. A failure is told on warn and leaves the file for the next prune.
func pruneScanCheckpoints(dir string, now time.Time, maxAge time.Duration, maxBytes int64, warn io.Writer) {
	stamp := filepath.Join(dir, scanPruneStamp)
	info, err := os.Stat(stamp)
	switch {
	case err == nil && now.Sub(info.ModTime()) < scanPruneEvery:
		return
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		fmt.Fprintf(warn, "statusline: stat scan checkpoint prune stamp %s: %v\n", stamp, err)
		return
	}
	if err := os.WriteFile(stamp, nil, 0o600); err != nil {
		fmt.Fprintf(warn, "statusline: write scan checkpoint prune stamp %s: %v\n", stamp, err)
		return
	}
	if err := os.Chtimes(stamp, now, now); err != nil {
		fmt.Fprintf(warn, "statusline: stamp scan checkpoint prune %s: %v\n", stamp, err)
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(warn, "statusline: list scan checkpoints %s: %v\n", dir, err)
		return
	}
	type held struct {
		path     string
		size     int64
		modified time.Time
	}
	var kept []held
	total := int64(0)
	remove := func(path string) bool {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(warn, "statusline: prune scan checkpoint %s: %v\n", path, err)
			return false
		}
		return true
	}
	for _, entry := range entries {
		if entry.Name() == scanPruneStamp {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				fmt.Fprintf(warn, "statusline: stat scan checkpoint %s: %v\n", path, err)
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if now.Sub(info.ModTime()) > maxAge {
			remove(path)
			continue
		}
		kept = append(kept, held{path: path, size: info.Size(), modified: info.ModTime()})
		total += info.Size()
	}
	sort.Slice(kept, func(a, b int) bool { return kept[a].modified.Before(kept[b].modified) })
	for index := 0; total > maxBytes && index < len(kept); index++ {
		if remove(kept[index].path) {
			total -= kept[index].size
		}
	}
}

// foldState is a fold over a transcript's lines whose exported fields are its
// whole state: a checkpoint keeps them as JSON between renders.
type foldState interface {
	// reset makes the fold one that has seen no line.
	reset()
	// line folds one line, its newline cut.
	line(line []byte)
	// loaded checks a fold just decoded from a checkpoint; an error discards it.
	loaded() error
}

// scanCheckpoint is one fold's state at the end of the last whole line it
// folded, and where the next render resumes reading.
type scanCheckpoint struct {
	Version int               `json:"version"`
	Kind    string            `json:"kind"`
	Path    string            `json:"path"`
	From    transcript.Resume `json:"from"`
	State   json.RawMessage   `json:"state"`
}

// fold brings state to path's end: it loads the checkpoint kind keeps for
// path, folds only the whole lines past it (every line when the file is no
// longer the one the checkpoint read, grown by appending), keeps the new
// checkpoint, then folds a torn final line, which the checkpoint never holds:
// its writer may be mid-append, so it is read again with the rest of its line.
// The result is the fold of a fresh whole read. done is false when the budget
// ran out first: state then holds a whole-line prefix of the file, a floor.
// The error is ResumeLines', a *transcript.OpenError when the file would not
// open. A checkpoint that cannot be read or kept is told on warn and costs
// only a read from the top, never a wrong fold.
func (scans transcriptScans) fold(path, kind string, state foldState) (done bool, err error) {
	var from transcript.Resume
	checkpointPath := ""
	if scans.dir != "" {
		digest := sha256.Sum256([]byte(path))
		checkpointPath = filepath.Join(scans.dir, kind+"-"+hex.EncodeToString(digest[:16])+".json")
		from = scans.load(checkpointPath, path, kind, state)
	}
	var torn []byte
	next, done, err := transcript.ResumeLines(path, from, scans.deadline, func(start int64) {
		if start == 0 {
			state.reset()
		}
	}, func(line []byte, complete bool) {
		if complete {
			state.line(line)
			return
		}
		torn = append(torn[:0], line...)
	})
	if err != nil {
		var opened *transcript.OpenError
		if errors.As(err, &opened) {
			state.reset() // nothing of the file was read: the checkpoint's fold is not its fold
		}
		return false, err
	}
	// A fold that kept nothing (offset 0) has nothing to resume from: writing
	// it would cost a cold render a write per transcript it never reached.
	moved := next.Offset != from.Offset || next.Device != from.Device || next.Inode != from.Inode ||
		!bytes.Equal(next.Anchor, from.Anchor)
	if checkpointPath != "" && next.Offset > 0 && moved {
		scans.keep(checkpointPath, path, kind, next, state)
	}
	if torn != nil {
		state.line(torn)
	}
	return done, nil
}

// load is the checkpoint kind keeps for path, decoded into state, or the zero
// Resume with state reset when there is none to trust.
func (scans transcriptScans) load(checkpointPath, path, kind string, state foldState) transcript.Resume {
	state.reset()
	body, err := os.ReadFile(checkpointPath)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			fmt.Fprintf(
				scans.warn,
				"statusline: read scan checkpoint %s (reading %s whole): %v\n",
				checkpointPath,
				path,
				err,
			)
		}
		return transcript.Resume{}
	}
	var checkpoint scanCheckpoint
	err = json.Unmarshal(body, &checkpoint)
	switch {
	case err != nil:
		err = fmt.Errorf("decode: %w", err)
	case checkpoint.Version != scanCheckpointVersion || checkpoint.Kind != kind || checkpoint.Path != path:
		return transcript.Resume{} // another format or another file: start over, nothing is wrong
	default:
		if err = json.Unmarshal(checkpoint.State, state); err != nil {
			err = fmt.Errorf("decode state: %w", err)
		} else {
			err = state.loaded()
		}
	}
	if err != nil {
		fmt.Fprintf(scans.warn, "statusline: scan checkpoint %s (reading %s whole): %v\n", checkpointPath, path, err)
		state.reset()
		return transcript.Resume{}
	}
	return checkpoint.From
}

// keep writes the checkpoint; a failure is told on warn and leaves the next
// render the older checkpoint, or none, to read further from.
func (scans transcriptScans) keep(
	checkpointPath, path, kind string,
	next transcript.Resume,
	state foldState,
) {
	raw, err := json.Marshal(state)
	if err == nil {
		raw, err = json.Marshal(scanCheckpoint{
			Version: scanCheckpointVersion, Kind: kind, Path: path, From: next, State: raw,
		})
	}
	if err != nil {
		fmt.Fprintf(scans.warn, "statusline: encode scan checkpoint %s: %v\n", checkpointPath, err)
		return
	}
	if err := atomicfile.Write(checkpointPath, raw, 0o600); err != nil {
		fmt.Fprintf(scans.warn, "statusline: write scan checkpoint %s: %v\n", checkpointPath, err)
	}
}
