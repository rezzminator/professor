package statusline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/usagehook"
)

// pacedDir is the directory under the cache dir holding each chat's paced
// render files, suffixed with the uid that owns it; it must be that uid's own
// real directory (usagehook.EnsurePrivateDir), else the chat renders inline.
const pacedDir = "cc-sl-paced"

const (
	// pacedVersion names the cached line's format; any other reads as no cache.
	pacedVersion = 1
	// pacedGap is the wait from one render's end to the next render's start.
	// Claude Code aborts a render still running when its next tick or input
	// change comes, so a render it starts itself never lands once it is slow.
	pacedGap = 5 * time.Second
	// pacedIdle ends a worker whose chat stopped calling the front: closed,
	// unmounted or gone. The chat's next front starts another.
	pacedIdle = 30 * time.Second
	// pacedMaxAge is how long a chat's files outlive its last front call.
	pacedMaxAge = 24 * time.Hour
)

// PacedPending is what a front prints while its chat's first render runs in
// the worker, so a render still on its way never reads as an empty line.
const PacedPending = "pfm statusline: rendering…\n"

// Paced splits a chat's main statusline into a cheap front, run on every
// Claude Code tick, and one detached worker per chat that renders at most once
// per pacedGap after its last render finished. Its files live under
// {CacheDir}/cc-sl-paced-{uid}, named by Key: {Key}.input (the newest stdin,
// its mtime the chat's liveness), {Key}.out.json (the cached line) and
// {Key}.lock (held by the worker for its whole life).
type Paced struct {
	CacheDir string
	// Key names the chat (PacedKey); empty renders inline and keeps no file.
	Key string
	// Render is one whole render of a stdin payload.
	Render func(raw []byte) (string, error)
	// Spawn starts the detached worker for a key.
	Spawn func(key string) error
	// Watch names files whose replacement or edit ends the worker (the pfm
	// executable, the machine config): the next front starts one that reads
	// them afresh, so a resident worker never renders with an old binary.
	Watch []string
	Now   func() time.Time
	Sleep func(time.Duration) error
	// Warn takes the diagnostic lines; nil is os.Stderr.
	Warn io.Writer
}

// pacedLine is {Key}.out.json: the last render's output, the moment it
// finished, and its error, if any — a failed render keeps the previous output.
type pacedLine struct {
	V          int    `json:"v"`
	Output     string `json:"output"`
	FinishedAt int64  `json:"finished_at"`
	Error      string `json:"error,omitempty"`
}

// PacedKey names a chat for Paced: the first 16 bytes of the SHA-256 of its
// session_id (else its transcript_path) and the terminal width the render
// reads, hex. A payload naming neither, or not JSON, has no key: it renders
// inline as before the pacer.
func PacedKey(raw []byte, columns string) string {
	var chat struct {
		SessionID      string `json:"session_id"`
		TranscriptPath string `json:"transcript_path"`
	}
	if json.Unmarshal(raw, &chat) != nil {
		return ""
	}
	id := chat.SessionID
	if id == "" {
		id = chat.TranscriptPath
	}
	if id == "" {
		return ""
	}
	// The width is the one render input the worker cannot read from stdin: it
	// keeps its spawner's COLUMNS, so a resized terminal is another chat key.
	digest := sha256.Sum256([]byte(id + "\x00" + columns))
	return hex.EncodeToString(digest[:16])
}

// Front answers one Claude Code tick: it saves raw as the chat's newest input,
// keeps the chat's worker running and prints its cached line. A chat with no
// cached line yet prints PacedPending: its worker renders at once, and the
// front never renders inline while the pacer works, since Claude Code aborts a
// run slower than its tick. A pacer that cannot serve (no key, a refused
// directory, a failed write or spawn) renders inline, so the line never goes
// blank because the pacer broke; every failure is told on Warn.
func (paced Paced) Front(raw []byte, stdout io.Writer) {
	paced = paced.normalized()
	var line pacedLine
	if paced.Key == "" {
		line = paced.render(raw)
	} else {
		var err error
		if line, err = paced.serve(raw); err != nil {
			fmt.Fprintf(paced.Warn, "pfm statusline: paced render (rendering inline): %v\n", err)
			line = paced.render(raw)
		}
	}
	if line.Error != "" {
		fmt.Fprintf(paced.Warn, "pfm statusline: render (fail-open): %s\n", line.Error)
	}
	if _, err := io.WriteString(stdout, line.Output); err != nil {
		fmt.Fprintf(paced.Warn, "pfm statusline: write output (fail-open): %v\n", err)
	}
}

// serve is the front's paced path: it starts the chat's worker before it reads
// anything, then answers the cached line, or PacedPending while the chat has
// none. It never renders: a front Claude Code aborts mid-render would leave no
// worker and no line. An error means the pacer cannot serve this tick.
func (paced Paced) serve(raw []byte) (pacedLine, error) {
	dir, err := paced.dir()
	if err != nil {
		return pacedLine{}, err
	}
	pruneStaleFiles(dir, paced.Now(), pacedMaxAge, math.MaxInt64, paced.Warn)
	base := filepath.Join(dir, paced.Key)
	if err := atomicfile.Write(base+".input", raw, 0o600); err != nil {
		return pacedLine{}, fmt.Errorf("write paced input %s: %w", base+".input", err)
	}
	if err := paced.ensureWorker(base + ".lock"); err != nil {
		return pacedLine{}, err // no worker renders or refreshes the line
	}
	line, found, err := readPacedLine(base + ".out.json")
	if err != nil {
		fmt.Fprintf(paced.Warn, "pfm statusline: paced render (the worker writes the next): %v\n", err)
	}
	if !found {
		return pacedLine{Output: PacedPending}, nil
	}
	return line, nil
}

// ensureWorker spawns the chat's worker unless one holds its lock. Two fronts
// racing both spawn; the worker that loses the lock exits.
func (paced Paced) ensureWorker(lockPath string) error {
	lock, held, err := takePacedLock(lockPath)
	if err != nil || !held {
		return err
	}
	if err := lock.Close(); err != nil {
		return fmt.Errorf("release paced lock %s: %w", lockPath, err)
	}
	if err := paced.Spawn(paced.Key); err != nil {
		return fmt.Errorf("spawn paced worker %s: %w", paced.Key, err)
	}
	return nil
}

// Work is the chat's worker: it holds the chat's lock for its whole life and,
// each turn, waits until pacedGap after the last render finished, then renders
// the newest input and caches the line. It returns nil when another worker
// holds the lock, when the chat's input went quiet for pacedIdle, and when a
// Watch file changed. A failed render keeps the previous output and records
// the error, so the front never shows an error as an empty line.
func (paced Paced) Work() (returnErr error) {
	paced = paced.normalized()
	if digest, err := hex.DecodeString(paced.Key); err != nil || len(digest) != 16 {
		return fmt.Errorf("paced key %q is not 32 hex digits", paced.Key)
	}
	dir, err := paced.dir()
	if err != nil {
		return err
	}
	base := filepath.Join(dir, paced.Key)
	lock, held, err := takePacedLock(base + ".lock")
	if err != nil || !held {
		return err
	}
	defer func() {
		if err := lock.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("release paced lock %s: %w", base+".lock", err))
		}
	}()
	watched, err := stampFiles(paced.Watch)
	if err != nil {
		return err
	}
	var last time.Time
	for {
		previous, _, err := readPacedLine(base + ".out.json")
		if err != nil {
			fmt.Fprintf(paced.Warn, "pfm statusline: paced worker: %v\n", err)
		}
		if finished := time.Unix(0, previous.FinishedAt); finished.After(last) {
			last = finished
		}
		// A finished_at ahead of the clock never parks the worker past one gap.
		if wait := min(last.Add(pacedGap).Sub(paced.Now()), pacedGap); wait > 0 {
			if err := paced.Sleep(wait); err != nil {
				return fmt.Errorf("wait for the next paced render: %w", err)
			}
		}
		now := paced.Now()
		// The lock is the one file no front rewrites: keep it fresh for the pruner.
		if err := os.Chtimes(base+".lock", now, now); err != nil {
			return fmt.Errorf("stamp paced lock %s: %w", base+".lock", err)
		}
		input, err := os.Stat(base + ".input")
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("stat paced input %s: %w", base+".input", err)
		}
		if now.Sub(input.ModTime()) > pacedIdle {
			return nil
		}
		current, err := stampFiles(paced.Watch)
		if err != nil {
			return err
		}
		if !slices.Equal(current, watched) {
			return nil
		}
		raw, err := os.ReadFile(base + ".input")
		if err != nil {
			return fmt.Errorf("read paced input %s: %w", base+".input", err)
		}
		line := paced.render(raw)
		last = time.Unix(0, line.FinishedAt)
		if line.Error != "" {
			line.Output = previous.Output
		}
		if err := writePacedLine(base+".out.json", line); err != nil {
			return err
		}
	}
}

func (paced Paced) normalized() Paced {
	if paced.Now == nil {
		paced.Now = clock.Real.Now
	}
	if paced.Sleep == nil {
		paced.Sleep = func(wait time.Duration) error { return clock.Real.Sleep(context.Background(), wait) }
	}
	if paced.Warn == nil {
		paced.Warn = os.Stderr
	}
	return paced
}

// dir is the chat files' private directory, made when absent.
func (paced Paced) dir() (string, error) {
	dir := filepath.Join(paced.CacheDir, pacedDir+"-"+strconv.Itoa(os.Getuid()))
	if err := usagehook.EnsurePrivateDir(dir); err != nil {
		return "", fmt.Errorf("paced directory refused: %w", err)
	}
	return dir, nil
}

// render runs one render, stamped the moment it returned.
func (paced Paced) render(raw []byte) pacedLine {
	output, err := paced.Render(raw)
	line := pacedLine{V: pacedVersion, Output: output, FinishedAt: paced.Now().UnixNano()}
	if err != nil {
		line.Error = err.Error()
	}
	return line
}

// takePacedLock takes path's flock without waiting; held=false with a nil
// error means a live worker holds it. Closing the file releases it.
func takePacedLock(path string) (*os.File, bool, error) {
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open paced lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		closeErr := lock.Close()
		if closeErr != nil {
			closeErr = fmt.Errorf("close paced lock %s: %w", path, closeErr)
		}
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, closeErr
		}
		return nil, false, errors.Join(fmt.Errorf("lock paced lock %s: %w", path, err), closeErr)
	}
	return lock, true, nil
}

// readPacedLine is the cached line; found=false when there is none of this
// version. An unreadable or corrupt file is an error and no line.
func readPacedLine(path string) (pacedLine, bool, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return pacedLine{}, false, nil
	}
	if err != nil {
		return pacedLine{}, false, fmt.Errorf("read paced line %s: %w", path, err)
	}
	var line pacedLine
	if err := json.Unmarshal(body, &line); err != nil {
		return pacedLine{}, false, fmt.Errorf("decode paced line %s: %w", path, err)
	}
	if line.V != pacedVersion {
		return pacedLine{}, false, nil
	}
	return line, true, nil
}

func writePacedLine(path string, line pacedLine) error {
	body, err := json.Marshal(line)
	if err != nil {
		return fmt.Errorf("encode paced line %s: %w", path, err)
	}
	if err := atomicfile.Write(path, body, 0o600); err != nil {
		return fmt.Errorf("write paced line %s: %w", path, err)
	}
	return nil
}

// fileStamp is what tells a file replaced (device, inode) or rewritten in
// place (size, mtime); an absent file is the zero stamp.
type fileStamp struct {
	id          gather.FileID
	size, mtime int64
}

func stampFiles(names []string) ([]fileStamp, error) {
	stamps := make([]fileStamp, len(names))
	for index, name := range names {
		info, err := os.Stat(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("stat watched %s: %w", name, err)
		}
		id, err := gather.FileIDOfInfo(info)
		if err != nil {
			return nil, fmt.Errorf("identify watched %s: %w", name, err)
		}
		stamps[index] = fileStamp{id: id, size: info.Size(), mtime: info.ModTime().UnixNano()}
	}
	return stamps, nil
}
