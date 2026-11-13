package fleet

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// One engine's pass failing leaves the others indexed: Scan warns on stderr
// and lists from the index instead of failing the whole scan.
func TestScanWarnsOnAPartialIndexAndLists(t *testing.T) {
	root := testjail.Fleet(t)
	openCodeRoot := filepath.Join(root, "opencode")
	if err := os.MkdirAll(openCodeRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	testjail.DatabaseFiles(t, openCodeRoot)
	if err := os.WriteFile(filepath.Join(openCodeRoot, "opencode.db"), []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvOpenCodeRoot, openCodeRoot)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	var stderr bytes.Buffer
	if _, err := Scan(
		context.Background(),
		database,
		Request{View: compose.DefaultView, ReadOnly: true},
		&stderr,
	); err != nil {
		t.Fatalf("Scan() = %v, want the partial index warned and the scan listed", err)
	}
	want := "pfm: index warning: index engine ox: read opencode sessions: read opencode store tables: " +
		"file is not a database (26)\n"
	if got := stderr.String(); got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}
