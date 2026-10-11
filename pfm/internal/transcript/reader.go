package transcript

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// Reading is what a watch reads of a transcript at each poll: the run's facts
// and its newest entry, exactly as ReadMeta and Tail(path, engine, 1, maxBytes)
// give them.
type Reading struct {
	Meta    Meta
	Last    Entry
	HasLast bool
}

// betweenStatAndRead runs after a read has taken its stat and before it reads
// the bytes: where a live engine's append lands in the race the readers close.
// Tests append there.
var betweenStatAndRead = func(string) {}

// betweenReadAndRestat runs after the incremental read reached the end of the
// file and before its closing stat. Tests append there.
var betweenReadAndRestat = func(string) {}

// ReadNewest reads a transcript whole: ReadMeta, then Tail's newest entry.
func ReadNewest(ctx context.Context, path, engine string, maxBytes int) (Reading, error) {
	meta, err := ReadMeta(path, engine)
	if err != nil {
		return Reading{}, err
	}
	betweenStatAndRead(path)
	entries, _, err := Tail(ctx, path, engine, 1, maxBytes)
	if err != nil {
		return Reading{}, err
	}
	// Tail may read records appended after ReadMeta's stat: the reading is
	// dated by a stat taken after both reads, never before its newest record.
	after, err := os.Stat(path)
	if err != nil {
		return Reading{}, fmt.Errorf("restat transcript: %w", err)
	}
	if modNS := after.ModTime().UnixNano(); modNS > meta.ModifiedUnixNS {
		meta.ModifiedUnixNS = modNS
	}
	reading := Reading{Meta: meta}
	if len(entries) > 0 {
		reading.Last, reading.HasLast = entries[len(entries)-1], true
	}
	return reading, nil
}

// anchorBytes is how much of the folded transcript a cursor keeps to know,
// before it resumes, that the file still holds what it folded: the bytes just
// before its offset, the end of the newest whole record.
const anchorBytes = 512

// foldMaxLine is the longest line a cursor folds itself. A longer one goes to
// ReadNewest, whose own limits (ReadMeta's metaMaxLine) then decide it, so the
// incremental read never answers where the whole read would not.
const foldMaxLine = metaMaxLine / 2

// Reader reads transcripts for a watch, carrying each one's fold from one poll
// to the next, so a poll costs what was appended since the last, not the
// whole file twice. The answer is ReadNewest's:
//   - an unchanged file (same file, size and modification time) is not opened;
//   - a grown one is read from the end of the last whole record it folded,
//     once the bytes just before that point are found unchanged;
//   - anything else — shrunk, replaced, rewritten — is read from the top.
//
// A partial trailing record is never folded: it is read into this answer
// only, and read again with the rest of its record next time.
//
// Transcripts are append-only; a rewrite to the very same size inside one
// tick of the filesystem's clock is the one change a stat cannot see, and the
// next write shows it. A Reader is not safe for concurrent use; the zero
// Reader is ready to use, and the nil Reader reads whole every time.
type Reader struct {
	cursors map[cursorKey]*cursor
}

type cursorKey struct {
	path     string
	engine   string
	maxBytes int
}

// cursor is one transcript's fold: through offset, the end of the newest
// whole line, and the answer at the file's last seen size and time.
type cursor struct {
	file    os.FileInfo
	size    int64
	modNS   int64
	offset  int64
	meta    Meta
	state   metaScanState
	last    Entry
	hasLast bool
	anchor  []byte
	reading Reading
	read    bool
}

// Read returns path's Reading. Its errors are ReadNewest's: a transcript
// that cannot be read is an error, never an empty reading.
func (reader *Reader) Read(ctx context.Context, path, engine string, maxBytes int) (Reading, error) {
	if reader == nil {
		return ReadNewest(ctx, path, engine, maxBytes)
	}
	key := cursorKey{path: path, engine: engine, maxBytes: maxBytes}
	info, err := os.Stat(path)
	if err != nil {
		delete(reader.cursors, key)
		return Reading{}, err
	}
	known := reader.cursors[key]
	if known != nil && os.SameFile(known.file, info) && known.size == info.Size() &&
		known.modNS == info.ModTime().UnixNano() {
		known.read = true
		return known.reading, nil
	}
	next, whole, err := advance(ctx, path, engine, maxBytes, known)
	if err != nil {
		delete(reader.cursors, key)
		return Reading{}, err
	}
	if whole {
		delete(reader.cursors, key)
		return ReadNewest(ctx, path, engine, maxBytes)
	}
	next.read = true
	if reader.cursors == nil {
		reader.cursors = map[cursorKey]*cursor{}
	}
	reader.cursors[key] = next
	return next.reading, nil
}

// Sweep forgets every transcript not read since the last Sweep, so a watch
// holds only the seats it still watches.
func (reader *Reader) Sweep() {
	if reader == nil {
		return
	}
	for key, known := range reader.cursors {
		if !known.read {
			delete(reader.cursors, key)
			continue
		}
		known.read = false
	}
}

// advance folds what path holds past known's offset, or the whole file when
// known cannot be resumed. whole is true when a line is too long to fold here.
func advance(
	ctx context.Context,
	path, engine string,
	maxBytes int,
	known *cursor,
) (next *cursor, whole bool, returnErr error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close transcript %s: %w", path, err))
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("stat transcript %s: %w", path, err)
	}
	betweenStatAndRead(path)
	resume, err := resumable(file, info, known)
	if err != nil {
		return nil, false, err
	}
	next = &cursor{file: info, size: info.Size(), modNS: info.ModTime().UnixNano()}
	if resume {
		next.offset, next.meta, next.state = known.offset, known.meta, known.state
		next.last, next.hasLast, next.anchor = known.last, known.hasLast, known.anchor
	}
	if _, err := file.Seek(next.offset, io.SeekStart); err != nil {
		return nil, false, fmt.Errorf("seek transcript %s: %w", path, err)
	}
	fragment, whole, err := next.fold(ctx, bufio.NewReaderSize(file, 64<<10), engine, maxBytes)
	if err != nil || whole {
		return nil, whole, err
	}
	// The fold reads to the end of the file as it is now, past the stat above
	// when the engine appended in between. The cursor holds what was read, so
	// bytes landing after the fold still differ from it at the next poll, and
	// the reading is dated by a stat taken after the fold, never before its
	// newest record.
	betweenReadAndRestat(path)
	after, err := file.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("restat transcript %s: %w", path, err)
	}
	next.size = next.offset + int64(len(fragment))
	if modNS := after.ModTime().UnixNano(); modNS > next.modNS {
		next.modNS = modNS
	}
	next.reading = next.answer(fragment, engine, maxBytes)
	return next, false, nil
}

// resumable says known's fold still describes file's head: the same file, no
// shorter than the fold, and the bytes before its offset as they were.
func resumable(file *os.File, info os.FileInfo, known *cursor) (bool, error) {
	if known == nil || !os.SameFile(known.file, info) || info.Size() < known.offset || known.offset == 0 {
		return false, nil
	}
	held := make([]byte, len(known.anchor))
	if _, err := file.ReadAt(held, known.offset-int64(len(known.anchor))); err != nil {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, fmt.Errorf("read transcript %s: %w", file.Name(), err)
	}
	return bytes.Equal(held, known.anchor), nil
}

// fold reads every whole line into the cursor and returns the partial line
// after them, which stays unfolded.
func (next *cursor) fold(
	ctx context.Context,
	reader *bufio.Reader,
	engine string,
	maxBytes int,
) (fragment []byte, whole bool, err error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		line, readErr := reader.ReadBytes('\n')
		if len(line) > foldMaxLine {
			return nil, true, nil
		}
		if readErr == io.EOF {
			return line, false, nil
		}
		if readErr != nil {
			return nil, false, readErr
		}
		applyMeta(&next.meta, &next.state, scannedLine(line), engine)
		if entry, ok := parseCapped(line, engine, maxBytes); ok {
			next.last, next.hasLast = entry, true
		}
		next.offset += int64(len(line))
		next.anchor = append(next.anchor, line...)
		if extra := len(next.anchor) - anchorBytes; extra > 0 {
			next.anchor = append(next.anchor[:0], next.anchor[extra:]...)
		}
	}
}

// answer is the Reading at this cursor: its fold, then the partial line read
// as the whole reads would read it.
func (next *cursor) answer(fragment []byte, engine string, maxBytes int) Reading {
	meta, state := next.meta, next.state
	reading := Reading{Last: next.last, HasLast: next.hasLast}
	if len(fragment) > 0 {
		applyMeta(&meta, &state, scannedLine(fragment), engine)
		if entry, ok := parseCapped(fragment, engine, maxBytes); ok {
			reading.Last, reading.HasLast = entry, true
		}
	}
	meta.SizeBytes, meta.ModifiedUnixNS = next.size, next.modNS
	reading.Meta = meta
	return reading
}

// scannedLine is line as ReadMeta's bufio.ScanLines hands it over: without
// its newline or a carriage return before it.
func scannedLine(line []byte) []byte {
	line = bytes.TrimSuffix(line, []byte("\n"))
	return bytes.TrimSuffix(line, []byte("\r"))
}

// parseCapped is Tail's reading of one line: Parse, then the text and input
// capped at maxBytes when it is positive.
func parseCapped(line []byte, engine string, maxBytes int) (Entry, bool) {
	entry, ok := Parse(line, engine)
	if ok && maxBytes > 0 {
		entry.Text = Truncate(entry.Text, maxBytes)
		entry.Input = Truncate(entry.Input, maxBytes)
	}
	return entry, ok
}
