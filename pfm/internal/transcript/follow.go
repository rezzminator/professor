package transcript

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
)

// Size is the transcript's current length in bytes — the frontier a caller
// records BEFORE it says something, so the answer it waits for afterwards
// cannot be an older one. A file that does not exist yet is length zero, not
// an error: a chat that has not spoken has a frontier all the same. A
// compressed rollout's length is its lines', the offsets From reads it at,
// never its compressed bytes.
func Size(path string) (size int64, returnErr error) {
	if path == "" {
		return 0, nil
	}
	if !codexmeta.Compressed(path) {
		info, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return 0, nil
			}
			return 0, err
		}
		return info.Size(), nil
	}
	rollout, err := codexmeta.OpenRollout(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	defer func() {
		if err := rollout.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close transcript %s: %w", path, err))
		}
	}()
	size, err = io.Copy(io.Discard, rollout)
	if err != nil {
		return 0, fmt.Errorf("measure transcript %s: %w", path, err)
	}
	return size, nil
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
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, offset, nil
		}
		return nil, offset, err
	}
	if offset < 0 || !codexmeta.Compressed(path) && info.Size() < offset {
		// The file shrank: a rotated or rewritten transcript. Re-reading it
		// from the top repeats entries, which is noisy; starting from the new
		// end loses them, which is wrong. The record wins. A compressed
		// rollout's size counts no line bytes, so it never says it shrank.
		offset = 0
	}
	file, err := codexmeta.OpenRolloutAt(path, offset)
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
