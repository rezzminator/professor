package testjail

import (
	"testing"

	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

// DatabaseFiles keeps every SQLite database under dir a real file for the rest
// of the test: in a test process the SQLite door otherwise serves each path
// from memory (the memory mode Run turns on). A test calls it when its subject is
// the file itself — a migration of a file found on disk, its backup, its WAL, a
// lock a second process holds — or when it hands the database to a real child.
// Outside a jail file the measure counts a test reaching it as home-sqlite.
func DatabaseFiles(t testing.TB, dir string) {
	t.Helper()
	t.Cleanup(sqlitedb.AllowDatabaseFiles(dir))
}

// releaseDatabases ends the memory databases served under root once the test
// that built it is done, so a marker copied out of it names a database this
// process no longer holds.
func releaseDatabases(t testing.TB, root string) {
	t.Helper()
	t.Cleanup(func() {
		if err := sqlitedb.ReleaseTestMemory(root); err != nil {
			t.Errorf("%v", err)
		}
	})
}
