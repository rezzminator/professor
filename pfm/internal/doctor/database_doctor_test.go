package doctor

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// doctorDatabaseFile is what doctor must leave as it found it: the main
// file's bytes and the schema version a reader sees through its WAL.
type doctorDatabaseFile struct {
	sum     [sha256.Size]byte
	version int
}

// stageDoctorDatabaseVersion creates both databases the normal way, then
// sets one file's user_version: below its SchemaVersion it is a file this
// build would migrate, above it one a newer pfm wrote.
func stageDoctorDatabaseVersion(t *testing.T, path string, version int) {
	t.Helper()
	database, err := store.Open(store.WithWarningWriter(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitedb.OpenReadWrite(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version=%d", version)); err != nil {
		t.Fatal(errors.Join(err, db.Close()))
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func readDoctorDatabaseFile(t *testing.T, path string) doctorDatabaseFile {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitedb.OpenReadOnly(path, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(errors.Join(err, db.Close()))
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return doctorDatabaseFile{sum: sha256.Sum256(raw), version: version}
}

// assertDoctorLeftDatabase fails when doctor migrated, rewrote or backed up
// the file: a migration always writes a .bak-before-vN copy and takes the
// .migrate.lock first.
func assertDoctorLeftDatabase(t *testing.T, path string, before doctorDatabaseFile, output string) {
	t.Helper()
	after := readDoctorDatabaseFile(t, path)
	if after.version != before.version || after.sum != before.sum {
		t.Fatalf(
			"doctor changed %s: user_version %d -> %d, bytes changed=%t\n%s",
			path, before.version, after.version, after.sum != before.sum, output,
		)
	}
	backups, err := filepath.Glob(path + ".bak-before-v*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Fatalf("doctor wrote a migration backup %v\n%s", backups, output)
	}
	if _, err := os.Lstat(path + ".migrate.lock"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("doctor left a migration lock beside %s (lstat err=%v)\n%s", path, err, output)
	}
}

// TestDoctorReadsAnOlderStateDatabaseWithoutMigratingIt is the live failure:
// a newer build's doctor migrated pfm.db, and the installed older pfm then
// refused every command. Doctor reads it, warns, and leaves it as found.
func TestDoctorReadsAnOlderStateDatabaseWithoutMigratingIt(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	path := runtime.Paths.StateDB
	older := fleetdb.SchemaVersion - 1
	stageDoctorDatabaseVersion(t, path, older)
	before := readDoctorDatabaseFile(t, path)

	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, &stdout, &stderr, runtime)
	output := stdout.String()
	assertDoctorLeftDatabase(t, path, before, output)
	warning := fmt.Sprintf(
		"doctor: warning state database %s schema v%d, this build expects v%d — ", path, older, fleetdb.SchemaVersion,
	)
	if !strings.Contains(output, warning) || !strings.Contains(output, "pfm install --yes") {
		t.Fatalf("doctor did not name the older state database and its remedy (want %q):\n%s", warning, output)
	}
	unread := fmt.Sprintf(
		"could not look: state database %s schema v%d, this build expects v%d", path, older, fleetdb.SchemaVersion,
	)
	if !strings.Contains(output, "doctor: rows "+unread) {
		t.Fatalf("the kill census rendered an unread state database as a count (want %q):\n%s", unread, output)
	}
	if code != 1 {
		t.Fatalf("older state database doctor code=%d, want 1 (warnings, no failure)\n%s", code, output)
	}
}

func TestDoctorNamesANewerStateDatabaseWithoutTouchingIt(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	path := runtime.Paths.StateDB
	newer := fleetdb.SchemaVersion + 1
	stageDoctorDatabaseVersion(t, path, newer)
	before := readDoctorDatabaseFile(t, path)

	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, &stdout, &stderr, runtime)
	output := stdout.String()
	assertDoctorLeftDatabase(t, path, before, output)
	warning := fmt.Sprintf(
		"doctor: warning state database %s schema v%d is newer than this build's v%d — a newer pfm wrote it",
		path, newer, fleetdb.SchemaVersion,
	)
	if !strings.Contains(output, warning) {
		t.Fatalf("doctor did not say a newer pfm wrote the state database (want %q):\n%s", warning, output)
	}
	if code != 1 {
		t.Fatalf("newer state database doctor code=%d, want 1\n%s", code, output)
	}
}

func TestDoctorReadsAnOlderCacheDatabaseWithoutMigratingIt(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	path := runtime.Paths.CacheDB
	older := store.SchemaVersion - 1
	stageDoctorDatabaseVersion(t, path, older)
	before := readDoctorDatabaseFile(t, path)

	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, &stdout, &stderr, runtime)
	output := stdout.String()
	assertDoctorLeftDatabase(t, path, before, output)
	warning := fmt.Sprintf(
		"doctor: warning cache database %s schema v%d, this build expects v%d — ", path, older, store.SchemaVersion,
	)
	if !strings.Contains(output, warning) || !strings.Contains(output, "pfm install --yes") {
		t.Fatalf("doctor did not name the older cache database and its remedy (want %q):\n%s", warning, output)
	}
	unread := fmt.Sprintf(
		"doctor: rows could not look: cache database %s schema v%d, this build expects v%d",
		path, older, store.SchemaVersion,
	)
	if !strings.Contains(output, unread) {
		t.Fatalf("the row census rendered an unread cache database as counts (want %q):\n%s", unread, output)
	}
	if code != 1 {
		t.Fatalf("older cache database doctor code=%d, want 1\n%s", code, output)
	}
}

// TestDoctorCreatesNeitherDatabase: on a host with no database yet, doctor
// stays clean as before and still creates nothing.
func TestDoctorCreatesNeitherDatabase(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	databases := []string{runtime.Paths.StateDB, runtime.Paths.CacheDB}
	for _, path := range databases {
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
		}
	}

	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, &stdout, &stderr, runtime)
	output := stdout.String()
	for _, path := range databases {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("doctor created %s (lstat err=%v)\n%s", path, err, output)
		}
	}
	if code != 0 || !strings.Contains(output, "doctor: clean") {
		t.Fatalf("a host with no database yet is no longer clean: code=%d\n%s", code, output)
	}
}

// TestDoctorReachesTheHostChecksPastAPendingLegacyDatabase: a legacy database
// waiting for its move is a host-check BLOCK whose row carries the fix, so
// doctor reads past the database it cannot open and prints that row — it
// never stops at the open, creates the new database, or touches the legacy one.
func TestDoctorReachesTheHostChecksPastAPendingLegacyDatabase(t *testing.T) {
	for _, tc := range []struct {
		check  string
		legacy func(home string) string
	}{
		{check: "legacy-state-db", legacy: paths.LegacyStateDB},
		{check: "legacy-cache-db", legacy: paths.LegacyCacheDB},
	} {
		t.Run(tc.check, func(t *testing.T) {
			runtime := buildCleanDoctorHome(t)
			databases := []string{runtime.Paths.StateDB, runtime.Paths.CacheDB}
			for _, path := range databases {
				for _, suffix := range []string{"", "-wal", "-shm"} {
					if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
						t.Fatal(err)
					}
				}
			}
			legacy := tc.legacy(runtime.Paths.Home)
			if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(legacy, []byte("legacy rows"), 0o600); err != nil {
				t.Fatal(err)
			}

			var stdout, stderr bytes.Buffer
			code := runDoctor(nil, &stdout, &stderr, runtime)
			output := stdout.String()
			if code != 3 || !strings.Contains(output, "host-check: BLOCK "+tc.check+" ") {
				t.Fatalf("doctor stopped before the %s host-check row: code=%d\n%s", tc.check, code, output)
			}
			if strings.Contains(output, "doctor: unhealthy database:") || !strings.Contains(output, "could not look") {
				t.Fatalf("the pending legacy database is not a could-not-look row:\n%s", output)
			}
			if !strings.Contains(output, "doctor: failures=") {
				t.Fatalf("doctor printed no failure count:\n%s", output)
			}
			for _, path := range databases {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("doctor created %s beside the pending legacy database (lstat err=%v)", path, err)
				}
			}
			if body, err := os.ReadFile(legacy); err != nil || string(body) != "legacy rows" {
				t.Fatalf("doctor touched the legacy database: %q err=%v", body, err)
			}
		})
	}
}
