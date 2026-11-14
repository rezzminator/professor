package sqlitedb

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// markerPrefix opens the one line the test-process memory mode leaves at a
// database's path: `pfm memory database {name}`, name as OpenStoreMemory
// names a database.
const markerPrefix = "pfm memory database "

// testMemory holds the test-process memory mode: whether it is on, the
// databases it serves (each kept alive by one anchor connection), and the
// directories a test asked to keep as SQLite files.
var testMemory = struct {
	on     atomic.Bool
	create sync.Mutex // serializes a path's read-then-create, so two first opens share one database
	mu     sync.Mutex
	held   map[string]*memoryHold // by memory name
	files  map[string]int         // directory → allowances naming it
}{held: map[string]*memoryHold{}, files: map[string]int{}}

// memoryHold is one served database: the path its marker was first written at
// and the connection keeping it alive, as a memory database ends with its last.
type memoryHold struct {
	path   string
	anchor *sql.DB
}

// ServeTestMemory turns the test-process memory mode on: from now on every
// path OpenStore, OpenReadOnly and OpenReadWrite are handed is served from a
// memory database, reached through a marker file the door writes at the path,
// unless AllowDatabaseFiles names a directory holding it. Only testjail.Run
// calls it; production never does, so its doors open files.
func ServeTestMemory() { testMemory.on.Store(true) }

// AllowDatabaseFiles keeps every database under dir an SQLite file while the
// mode is on, until the returned release runs.
func AllowDatabaseFiles(dir string) (release func()) {
	key := absolute(dir)
	testMemory.mu.Lock()
	defer testMemory.mu.Unlock()
	testMemory.files[key]++
	return sync.OnceFunc(func() {
		testMemory.mu.Lock()
		defer testMemory.mu.Unlock()
		testMemory.files[key]--
		if testMemory.files[key] <= 0 {
			delete(testMemory.files, key)
		}
	})
}

// ReleaseTestMemory closes the anchor of every served database whose marker
// was first written under root, so each ends once its last handle closes; a
// marker left behind then names a database this process no longer holds.
func ReleaseTestMemory(root string) error {
	prefix := absolute(root) + string(filepath.Separator)
	testMemory.mu.Lock()
	var anchors []*sql.DB
	for name, hold := range testMemory.held {
		if strings.HasPrefix(hold.path, prefix) {
			anchors = append(anchors, hold.anchor)
			delete(testMemory.held, name)
		}
	}
	testMemory.mu.Unlock()
	var errs []error
	for _, anchor := range anchors {
		if err := anchor.Close(); err != nil {
			errs = append(errs, fmt.Errorf("release memory database under %s: %w", root, err))
		}
	}
	return errors.Join(errs...)
}

// AttachName is the name `ATTACH DATABASE ?` takes for path: the path (an
// OpenStoreMemory name too), or in the memory mode the memory database its
// marker names, created as ATTACH creates a missing file; a marker the door
// refuses is the error, never a bare path.
func AttachName(path string) (string, error) {
	if InMemory(path) || !served(path) {
		return path, nil
	}
	name, _, err := memoryName(context.Background(), path, true)
	return name, err
}

// absolute resolves path against the working directory, so a relative path
// meets the allowances and homes above it; unresolvable, it is matched as written.
func absolute(path string) string {
	if resolved, err := filepath.Abs(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

// served reports whether the memory mode answers for path.
func served(path string) bool {
	if !testMemory.on.Load() {
		return false
	}
	testMemory.mu.Lock()
	defer testMemory.mu.Unlock()
	for dir := absolute(path); ; {
		parent := filepath.Dir(dir)
		if parent == dir {
			return true
		}
		if testMemory.files[parent] > 0 {
			return false
		}
		dir = parent
	}
}

// readMarker reads the file at path: found is false for no file or an empty
// one, which SQLite reads as a new database; a file that is neither is
// refused, since the door never serves a file it did not write from memory;
// a marker naming a database this process does not hold is an error, never an
// empty database.
func readMarker(path string) (name string, found bool, err error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) || err == nil && len(content) == 0 {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read sqlite database marker %s: %w", path, err)
	}
	line, ok := bytes.CutPrefix(bytes.TrimSuffix(content, []byte("\n")), []byte(markerPrefix))
	if !ok || bytes.ContainsAny(line, "\n") {
		return "", false, fmt.Errorf(
			"sqlite database %s is a file this test process did not write; a test that needs SQLite's own file calls testjail.DatabaseFiles",
			path,
		)
	}
	name = string(line)
	testMemory.mu.Lock()
	_, held := testMemory.held[name]
	testMemory.mu.Unlock()
	if !held {
		return "", false, fmt.Errorf(
			"sqlite database %s names memory database %s, which this process no longer holds",
			path,
			name,
		)
	}
	return name, true, nil
}

// memoryName returns the memory database the marker at path names, creating
// one and writing its marker when create is set and no database stands there
// yet; found is false when none stands there and create is not set.
func memoryName(ctx context.Context, path string, create bool) (name string, found bool, err error) {
	testMemory.create.Lock()
	defer testMemory.create.Unlock()
	name, found, err = readMarker(path)
	if err != nil || found || !create {
		return name, found, err
	}
	name = fmt.Sprintf("file:/pfm-memory-%d?vfs=memdb", memoryNumber.Add(1))
	anchor, err := sql.Open(driverName, name)
	if err != nil {
		return "", false, fmt.Errorf("open memory database %s for %s: %w", name, path, err)
	}
	anchor.SetMaxIdleConns(1)
	if err := anchor.PingContext(ctx); err != nil {
		return "", false, errors.Join(fmt.Errorf("open memory database %s for %s: %w", name, path, err), anchor.Close())
	}
	if err := os.WriteFile(path, []byte(markerPrefix+name+"\n"), 0o600); err != nil {
		return "", false, errors.Join(fmt.Errorf("write sqlite database marker %s: %w", path, err), anchor.Close())
	}
	testMemory.mu.Lock()
	testMemory.held[name] = &memoryHold{path: absolute(path), anchor: anchor}
	testMemory.mu.Unlock()
	return name, true, nil
}

// openServedHandle opens a handle on a served database: one connection, and the
// query appended to its name (a read-only mode, a busy timeout).
func openServedHandle(name, query string) (*sql.DB, error) {
	dsn := name
	if query != "" {
		dsn += "&" + query
	}
	database, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database %s: %w", name, err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	return database, nil
}

// openStoreServed is OpenStore in the memory mode: the database the marker at
// path names, created on the first open, with the pragma set OpenStoreMemory
// gives a memory store.
func openStoreServed(ctx context.Context, path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create database directory for %s: %w", path, err)
	}
	name, _, err := memoryName(ctx, path, true)
	if err != nil {
		return nil, err
	}
	database, err := openServedHandle(name, "")
	if err != nil {
		return nil, err
	}
	if err := connectionPragmas(ctx, database); err != nil {
		return nil, errors.Join(fmt.Errorf("configure sqlite database %s: %w", path, err), database.Close())
	}
	return database, nil
}

// openForeignServed is OpenReadOnly or OpenReadWrite in the memory mode: the
// database the marker at path names, with the caller's mode and busy timeout.
// A read-write open creates it as SQLite creates a missing file; a read-only
// open of a path with no database is not found, left to the file open, so it
// fails as it does in production.
func openForeignServed(path, mode string, busy time.Duration) (database *sql.DB, found bool, err error) {
	name, found, err := memoryName(context.Background(), path, mode == "")
	if err != nil || !found {
		return nil, found, err
	}
	database, err = openServedHandle(name, fmt.Sprintf("%s_pragma=busy_timeout(%d)", mode, busy.Milliseconds()))
	return database, err == nil, err
}
