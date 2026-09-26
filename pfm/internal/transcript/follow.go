package transcript

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
)

// Size is the transcript's current length in bytes — the frontier a caller
// records BEFORE it says something, so the answer it waits for afterwards
// cannot be an older one. A file that does not exist yet is length zero, not
// an error: a chat that has not spoken has a frontier all the same.
func Size(path string) (int64, error) {
	if path == "" {
		return 0, nil
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return info.Size(), nil
}

// From reads the entries appended after offset and returns them with the
// offset to resume from.
//
// A partial trailing line is NOT consumed: both engines append with ordinary
// buffered writes, so a poll can land mid-record, and half a JSON object read
// as a whole one would be silently dropped — the caller would then wait
// forever for an answer that had already been written.
func From(
	ctx context.Context,
	path, engine string,
	offset int64,
) (entries []Entry, consumed int64, returnErr error) {
	if path == "" {
		return nil, offset, nil
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, offset, nil
		}
		return nil, offset, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close transcript %s: %w", path, err))
		}
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, offset, err
	}
	if offset < 0 || info.Size() < offset {
		// The file shrank: a rotated or rewritten transcript. Re-reading it
		// from the top repeats entries, which is noisy; starting from the new
		// end loses them, which is wrong. The record wins.
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}
	reader := bufio.NewReaderSize(file, 64<<10)
	entries = make([]Entry, 0, 8)
	consumed = offset
	for {
		if err := ctx.Err(); err != nil {
			return entries, consumed, err
		}
		line, readErr := reader.ReadBytes('\n')
		if readErr == nil {
			consumed += int64(len(line))
			if entry, ok := Parse(line, engine); ok {
				entries = append(entries, entry)
			}
			continue
		}
		if readErr == io.EOF {
			return entries, consumed, nil
		}
		return entries, consumed, readErr
	}
}
