package store

import (
	"testing"
)

// openTestStore opens the in-memory store (OpenMemory), the store a test
// opens unless the database file itself is its subject.
func openTestStore(t *testing.T, options ...OpenOption) *Store {
	t.Helper()

	store, err := OpenMemory(options...)
	if err != nil {
		t.Fatalf("OpenMemory() error = %v", err)
	}
	return store
}

// openFileTestStore opens the store on the files setStoreTestJail names, for
// a test of the file itself: its path, a reopen, an on-disk older version, a
// second connection or process on the same file.
func openFileTestStore(t *testing.T, options ...OpenOption) *Store {
	t.Helper()

	store, err := Open(options...)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	return store
}
