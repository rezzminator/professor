package fleetdb

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// The reader must not be what creates the state store: a missing database is a
// missing database, not an empty one.
func TestPrimaryAccountNeverCreatesTheDatabase(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	values := paths.Values{
		StateDB: filepath.Join(root, "state", "pfm.db"),
		Home:    filepath.Join(root, "home"),
	}
	if account, found, err := ClaudePrimaryAccount(context.Background(), values); found || err != nil {
		t.Fatalf("PrimaryAccount() = %d, %v, %v, want not found, nil", account, found, err)
	}
	if _, err := os.Stat(values.StateDB); !os.IsNotExist(err) {
		t.Fatalf("reading the primary account created %s: %v", values.StateDB, err)
	}
}

func TestSetPrimaryAccountKeepsDatabaseAndMirrorInLockstep(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	values := paths.Values{
		Home:    root,
		StateDB: filepath.Join(root, ".local", "state", "pfm", "pfm.db"),
	}
	if err := SetClaudePrimaryAccount(context.Background(), values, 2, 123); err != nil {
		t.Fatal(err)
	}
	if account, found, err := ClaudePrimaryAccount(context.Background(), values); !found || account != 2 || err != nil {
		t.Fatalf("PrimaryAccount()=%d,%v,%v, want 2,true,nil", account, found, err)
	}
	content, err := os.ReadFile(filepath.Join(root, ".claude-primary"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "2\n" {
		t.Fatalf("mirror=%q, want 2\\n", content)
	}
}
