package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// TranscriptNames reads chat display names from a transcript index without
// writing anything: the index opens read-only, no migration runs and the
// shared fleet store is never opened. A read-only report names its chats
// through it; everything that indexes goes through Open.
type TranscriptNames struct {
	db   *sql.DB
	path string
}

// OpenTranscriptNames opens the transcript index at path read-only. An index
// that does not exist returns found=false and no error, since there is nothing
// to name. A path that cannot be read, or a file whose schema version is not
// one this binary wrote, returns an error.
func OpenTranscriptNames(ctx context.Context, path string) (names *TranscriptNames, found bool, returnErr error) {
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, fmt.Errorf("read transcript index %s: %w", path, err)
	}
	db, err := sqlitedb.OpenReadOnly(path, sqlitedb.StoreBusyTimeout)
	if err != nil {
		return nil, false, fmt.Errorf("open transcript index %s read-only: %w", path, err)
	}
	opened := &TranscriptNames{db: db, path: path}
	defer func() {
		if returnErr != nil {
			returnErr = errors.Join(returnErr, opened.Close())
		}
	}()
	version, err := userVersion(ctx, loggedDB{db: db})
	if err != nil {
		return nil, false, fmt.Errorf("read transcript index %s: %w", path, err)
	}
	if version < 1 || version > SchemaVersion {
		return nil, false, fmt.Errorf(
			"read transcript index %s: schema version %d is not one this pfm reads (1..%d)",
			path,
			version,
			SchemaVersion,
		)
	}
	return opened, true, nil
}

// DisplayName returns the display name of the chat with sessionID, by
// naming.DisplayName's precedence over the indexed row. A session the index
// does not hold returns a blank name and no error; a failed read returns the
// error.
func (names *TranscriptNames) DisplayName(ctx context.Context, sessionID string) (string, error) {
	// Transcript reads only through the handle, so a Store over the read-only
	// handle alone runs the indexed query without the shared fleet store.
	transcript, found, err := (&Store{db: names.db, path: names.path}).Transcript(ctx, sessionID)
	if err != nil {
		return "", fmt.Errorf("read chat name from transcript index %s: %w", names.path, err)
	}
	if !found {
		return "", nil
	}
	return naming.DisplayName(transcript.CustomTitle, transcript.AITitle, transcript.FirstPrompt), nil
}

// Close releases the read-only handle.
func (names *TranscriptNames) Close() error {
	if err := names.db.Close(); err != nil {
		return fmt.Errorf("close transcript index %s: %w", names.path, err)
	}
	return nil
}
