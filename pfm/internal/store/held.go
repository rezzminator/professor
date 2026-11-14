package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"
)

// heldDegradedRetry is how long a Held keeps a Store whose shared half could
// not open before it opens both databases again.
const heldDegradedRetry = 10 * time.Second

// Held keeps one Store open across a long-lived reader's calls (the Herdr
// indicator painter reads the fleet on every half-second tick), so a call
// reuses the open connections and their loaded schema instead of opening,
// creating and migrating both databases again. It is no index daemon: it
// opens on the first call and only reads what its caller asks for.
//
// Store opens both databases again when either file is no longer the one the
// held Store opened, since a held handle on a removed or replaced file would
// keep reading the unlinked one; its inode cannot be reused while the handle
// holds it, so the identity check cannot mistake a new file for the old. A
// file replaced while the open ran is caught by taking its identity both
// before and after the open. A Store whose shared half was degraded is opened
// again once heldDegradedRetry has passed. A migration made in place by
// another process moves the file's user_version, so the Store is opened again
// and the open's own schema check (and its refusal of a file stamped newer
// than this binary) runs as it would for a new reader. The held connection
// holds no read transaction between statements, so it never blocks another
// process's WAL checkpoint.
//
// A Held is used by one goroutine; the Store it returns stays valid until the
// next Store or Close call.
type Held struct {
	options []OpenOption
	store   *Store
	// files are the cache and shared state files the held Store opened; a nil
	// entry is one that could not be read, opened again at the next call.
	files [2]os.FileInfo
	// versions are the user_version of each file at open; a change is a
	// migration another process made in place.
	versions [2]int
	openedAt time.Time
}

// NewHeld returns a Held that opens its Store through OpenContext with options.
func NewHeld(options ...OpenOption) *Held {
	return &Held{options: options}
}

// Store returns the held Store, opening it on the first call and again when
// a database file it opened was removed, replaced or migrated. A file that
// cannot be inspected is an error, never the held Store.
func (held *Held) Store(ctx context.Context) (*Store, error) {
	if held.store != nil {
		current, err := held.current(ctx)
		if err != nil {
			return nil, err
		}
		if current {
			return held.store, nil
		}
		if err := held.Close(); err != nil {
			return nil, fmt.Errorf("close the held store before reopening it: %w", err)
		}
	}
	resolved, _, err := resolveOpen(held.options)
	if err != nil {
		return nil, err
	}
	// The identity before the open; a file that cannot be inspected yet is
	// judged by its identity after the open alone, as a missing one is.
	var before [2]os.FileInfo
	for index, path := range [2]string{resolved.CacheDB, resolved.StateDB} {
		before[index], _ = heldStat(path)
	}
	opened, err := OpenContext(ctx, held.options...)
	if err != nil {
		return nil, err
	}
	held.store, held.openedAt = opened, opened.clockNow()
	degraded := opened.SharedDegraded() != nil
	for index, path := range held.paths() {
		held.files[index] = nil
		if index == 1 && degraded {
			continue // no shared file is open; current retries after heldDegradedRetry
		}
		after, err := heldStat(path)
		if err != nil {
			return nil, errors.Join(err, held.Close())
		}
		// A file present before the open and another one after it was replaced
		// while the open ran: which one the handle holds is unknown, so the
		// next call opens again.
		if after != nil && (before[index] == nil || os.SameFile(before[index], after)) {
			held.files[index] = after
		}
	}
	if held.versions, err = held.userVersions(ctx); err != nil {
		return nil, errors.Join(err, held.Close())
	}
	return opened, nil
}

// Close closes the held Store, if any; the next Store call opens a new one.
func (held *Held) Close() error {
	if held.store == nil {
		return nil
	}
	opened := held.store
	held.store, held.files = nil, [2]os.FileInfo{}
	return opened.Close()
}

func (held *Held) paths() [2]string {
	return [2]string{held.store.path, held.store.SharedPath()}
}

// current says whether the held Store still serves both files it opened, at
// the schema versions it opened them at.
func (held *Held) current(ctx context.Context) (bool, error) {
	degraded := held.store.SharedDegraded() != nil
	if degraded && held.store.clockNow().Sub(held.openedAt) >= heldDegradedRetry {
		return false, nil
	}
	for index, path := range held.paths() {
		if index == 1 && degraded {
			continue // no shared file is open to have been replaced
		}
		opened := held.files[index]
		if opened == nil {
			return false, nil
		}
		now, err := heldStat(path)
		if err != nil {
			return false, err
		}
		if now == nil || !os.SameFile(opened, now) {
			return false, nil
		}
	}
	versions, err := held.userVersions(ctx)
	if err != nil {
		return false, err
	}
	return versions == held.versions, nil
}

// userVersions reads the user_version of the cache and, unless degraded, the
// shared state (0 for a degraded half).
func (held *Held) userVersions(ctx context.Context) ([2]int, error) {
	var versions [2]int
	var err error
	if versions[0], err = held.store.UserVersion(ctx); err != nil {
		return versions, fmt.Errorf("read held cache database version: %w", err)
	}
	if held.store.SharedDegraded() != nil {
		return versions, nil
	}
	if versions[1], err = held.store.Shared().UserVersion(ctx); err != nil {
		return versions, fmt.Errorf("read held shared database version: %w", err)
	}
	return versions, nil
}

// heldStat is the identity of the database file at path; nil when it does not
// exist, and an error when it cannot be inspected.
func heldStat(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect held database %s: %w", path, err)
	}
	return info, nil
}
