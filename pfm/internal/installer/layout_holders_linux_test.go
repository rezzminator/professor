//go:build linux

package installer

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLinuxDBHolderPIDsFixture(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(t.TempDir(), "state.db")
	fd := filepath.Join(root, "123", "fd")
	if err := os.MkdirAll(fd, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(db, filepath.Join(fd, "0")); err != nil {
		t.Fatal(err)
	}
	got, err := dbHolderPIDs(root, db)
	if err != nil || !reflect.DeepEqual(got, []string{"123"}) {
		t.Fatalf("dbHolderPIDs = %v, %v", got, err)
	}
}
