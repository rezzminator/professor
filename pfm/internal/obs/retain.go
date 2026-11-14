package obs

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// retain is the size limit, run only at a roll, by the process that rolled:
// the current segment and every newest closed segment that starts within
// keepBytes of the newest byte are kept whole, so the newest keepBytes always
// are; every older segment is reduced, oldest
// first — its WARN and ERROR records appended, byte for byte, to {name}.kept/
// (reduce) — and deleted. A segment that cannot be read, kept or removed is an
// error, never a silent skip; the kept archive has no ceiling.
func (writer *rotator) retain() error {
	dir := segmentsDir(writer.path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("list activity log segments %s: %w", dir, err)
	}
	total := writer.identity.Size()
	var past []string
	// ReadDir sorts by name, and a segment's name is the time it closed.
	for index := len(entries) - 1; index >= 0; index-- {
		entry := entries[index]
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect activity log segment %s: %w", filepath.Join(dir, entry.Name()), err)
		}
		// A segment starting within keepBytes stays whole: a roll closes a
		// segment just past segmentBytes, so the newest keepBytes are always kept.
		if total >= writer.keepBytes {
			past = append(past, filepath.Join(dir, entry.Name()))
		}
		total += info.Size()
	}
	var failures []error
	for _, segment := range slices.Backward(past) {
		if writer.reduce {
			if err := keepWarnings(segment, keptDir(writer.path)); err != nil {
				failures = append(failures, err)
				continue
			}
		}
		if err := os.Remove(segment); err != nil {
			failures = append(failures, fmt.Errorf("drop activity log segment %s: %w", segment, err))
		}
	}
	return errors.Join(failures...)
}

// keepWarnings appends segment's WARN and ERROR records, byte for byte, to
// kept/{day}.jsonl, the UTC day each record carries in its ts. A line that is
// not a record (damaged) is kept too, under the day of the record before it
// (else the segment's), because it may be an error's; a last line missing its
// newline gets one, so the archive stays one record per line.
func keepWarnings(segment, kept string) error {
	file, err := os.Open(segment)
	if err != nil {
		return fmt.Errorf("read activity log segment %s: %w", segment, err)
	}
	defer file.Close() //nolint:errcheck // read-only: nothing is lost by a failed close
	day, err := segmentDay(segment, file)
	if err != nil {
		return err
	}
	days := map[string]*bytes.Buffer{}
	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) != 0 {
			level, stamp, readable := recordHead(line)
			if readable && !stamp.IsZero() {
				day = stamp.UTC().Format(keptStamp)
			}
			if !readable || level >= slog.LevelWarn {
				if days[day] == nil {
					days[day] = &bytes.Buffer{}
				}
				days[day].Write(line)
				if line[len(line)-1] != '\n' {
					days[day].WriteByte('\n')
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read activity log segment %s: %w", segment, err)
		}
	}
	if len(days) != 0 {
		if err := os.MkdirAll(kept, 0o700); err != nil {
			return fmt.Errorf("create kept activity log %s: %w", kept, err)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(days)) {
		path := filepath.Join(kept, name+filepath.Ext(segment))
		archive, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return fmt.Errorf("open kept activity log %s: %w", path, err)
		}
		_, writeErr := archive.Write(days[name].Bytes())
		if err := errors.Join(writeErr, archive.Close()); err != nil {
			return fmt.Errorf("keep warnings of %s in %s: %w", segment, path, err)
		}
	}
	return nil
}

// segmentDay is the UTC day a segment closed: read from its name, or, for a
// file not named by SegmentPath, from when it was last written.
func segmentDay(segment string, file *os.File) (string, error) {
	stamp := strings.TrimSuffix(filepath.Base(segment), filepath.Ext(segment))
	if closed, err := time.Parse(segmentStamp, stamp); err == nil {
		return closed.Format(keptStamp), nil
	}
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("inspect activity log segment %s: %w", segment, err)
	}
	return info.ModTime().UTC().Format(keptStamp), nil
}

// recordPrefix is how every record the activity log writes begins: slog's
// JSON handler writes the time first and the level second, so the common case
// is read without decoding the line.
const recordPrefix = `{"` + FieldTime + `":"`

// recordHead reads one record's level and time; readable is false for a line
// that is not a record.
func recordHead(line []byte) (level slog.Level, stamp time.Time, readable bool) {
	var head struct {
		TS    string `json:"ts"`
		Level string `json:"level"`
	}
	if rest, ok := bytes.CutPrefix(line, []byte(recordPrefix)); ok {
		ts, after, found := bytes.Cut(rest, []byte(`","level":"`))
		name, _, closed := bytes.Cut(after, []byte(`"`))
		if found && closed {
			head.TS, head.Level = string(ts), string(name)
		}
	}
	if head.Level == "" && json.Unmarshal(bytes.TrimSpace(line), &head) != nil {
		return 0, time.Time{}, false
	}
	if err := level.UnmarshalText([]byte(head.Level)); err != nil {
		return 0, time.Time{}, false
	}
	stamp, err := time.Parse(time.RFC3339Nano, head.TS)
	if err != nil {
		return level, time.Time{}, true
	}
	return level, stamp, true
}

// segmentsDir and keptDir name a log's folders of closed segments and of kept
// warnings and errors, from its current segment's path.
func segmentsDir(path string) string { return logStem(path) + segmentsSuffix }

func keptDir(path string) string { return logStem(path) + keptSuffix }

func logStem(path string) string { return strings.TrimSuffix(path, filepath.Ext(path)) }

// closeSegment renames the current segment into {name}.segments/ under the
// UTC time it closed.
func (writer *rotator) closeSegment() error {
	target, err := SegmentPath(writer.path, writer.timing.Now())
	if err != nil {
		return err
	}
	if err := os.Rename(writer.path, target); err != nil {
		return fmt.Errorf("close activity log segment %s: %w", target, err)
	}
	return nil
}

// SegmentPath is a free name for a segment of the log whose current segment is
// current, closed at closed: {name}.segments/{UTC time}.{ext}, a name already
// taken moving a millisecond on; the folder is created. pfm install files the
// home's pre-root log files as segments through it.
func SegmentPath(current string, closed time.Time) (string, error) {
	dir := segmentsDir(current)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create activity log segments %s: %w", dir, err)
	}
	closed = closed.UTC()
	for {
		target := filepath.Join(dir, closed.Format(segmentStamp)+filepath.Ext(current))
		if _, err := os.Lstat(target); errors.Is(err, fs.ErrNotExist) {
			return target, nil
		} else if err != nil {
			return "", fmt.Errorf("inspect activity log segment %s: %w", target, err)
		}
		closed = closed.Add(time.Millisecond)
	}
}
