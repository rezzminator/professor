package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// seedTranscriptIndex writes one transcript through the indexing Store and
// closes it, returning the index path.
func seedTranscriptIndex(t *testing.T, transcript Transcript) string {
	t.Helper()
	dbPath := setStoreTestJail(t)
	store := openTestStore(t)
	if err := store.UpsertTranscript(context.Background(), transcript); err != nil {
		t.Fatalf("UpsertTranscript() error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return dbPath
}

func TestTranscriptNamesReadsDisplayNameReadOnly(t *testing.T) {
	dbPath := seedTranscriptIndex(t, Transcript{
		UUID: "sess-1", Path: "/fixture/sess-1.jsonl", AITitle: "ai title", FirstPrompt: "first prompt",
	})
	// The reader must never reach the shared store the indexing Store opened.
	sharedPath := filepath.Join(t.TempDir(), "cc", "fleet.db")
	t.Setenv(paths.EnvFleetDB, sharedPath)

	ctx := context.Background()
	names, found, err := OpenTranscriptNames(ctx, dbPath)
	if err != nil || !found {
		t.Fatalf("OpenTranscriptNames() = %v, %v; want found and no error", found, err)
	}
	for _, tc := range []struct{ session, want string }{
		{"sess-1", "ai title"},
		{"sess-absent", ""},
	} {
		got, err := names.DisplayName(ctx, tc.session)
		if err != nil || got != tc.want {
			t.Fatalf("DisplayName(%q) = %q, %v; want %q and no error", tc.session, got, err, tc.want)
		}
	}
	if err := names.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := os.Stat(sharedPath); !os.IsNotExist(err) {
		t.Fatalf("reader touched the shared store %s: stat err = %v", sharedPath, err)
	}
}

func TestTranscriptNamesMissingIndexIsAbsence(t *testing.T) {
	names, found, err := OpenTranscriptNames(context.Background(), filepath.Join(t.TempDir(), "pfm.db"))
	if names != nil || found || err != nil {
		t.Fatalf("OpenTranscriptNames(missing) = %v, %v, %v; want nil, false, nil", names, found, err)
	}
}

func TestTranscriptNamesUnreadableIndexErrs(t *testing.T) {
	dir := t.TempDir()
	notDir := filepath.Join(dir, "file")
	garbage := filepath.Join(dir, "garbage.db")
	empty := filepath.Join(dir, "empty.db")
	for path, content := range map[string]string{notDir: "", garbage: strings.Repeat("not sqlite ", 200), empty: ""} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, path := range map[string]string{
		"path under a file":   filepath.Join(notDir, "pfm.db"),
		"not a database":      garbage,
		"unrecognized schema": empty,
	} {
		t.Run(name, func(t *testing.T) {
			names, found, err := OpenTranscriptNames(context.Background(), path)
			if err == nil || names != nil || found {
				t.Fatalf("OpenTranscriptNames(%s) = %v, %v, %v; want an error", path, names, found, err)
			}
		})
	}
}
