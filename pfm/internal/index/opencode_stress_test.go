package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/store"
)

// seedOpencodeStress builds a large, hostile session store: hundreds of
// sessions, unicode/control-character titles, oversized prompts, malformed
// model JSON, NULL-heavy rows, and duplicate timestamps.
func seedOpencodeStress(t *testing.T, root string, count int) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "opencode.db"))
	if err != nil {
		t.Fatalf("open stress store: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	}()
	script := `
CREATE TABLE project (id TEXT PRIMARY KEY, worktree TEXT NOT NULL);
CREATE TABLE session (
  id TEXT PRIMARY KEY,
  project_id TEXT NOT NULL,
  parent_id TEXT,
  slug TEXT NOT NULL,
  directory TEXT NOT NULL,
  title TEXT NOT NULL,
  version TEXT NOT NULL,
  share_url TEXT,
  summary_additions INTEGER,
  summary_deletions INTEGER,
  summary_files INTEGER,
  summary_diffs TEXT,
  revert TEXT,
  permission TEXT,
  time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL,
  time_compacting INTEGER,
  time_archived INTEGER,
  workspace_id TEXT,
  path TEXT
);
CREATE TABLE message (
  id TEXT PRIMARY KEY,
  session_id TEXT NOT NULL,
  time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL,
  data TEXT NOT NULL
);
CREATE TABLE part (
  id TEXT PRIMARY KEY,
  message_id TEXT NOT NULL,
  session_id TEXT NOT NULL,
  time_created INTEGER NOT NULL,
  time_updated INTEGER NOT NULL,
  data TEXT NOT NULL
);
CREATE INDEX message_session_time_created_id_idx ON message (session_id, time_created, id);
CREATE INDEX part_message_id_id_idx ON part (message_id, id);
CREATE INDEX part_session_idx ON part (session_id);`
	if _, err := db.Exec(script); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	// Production opencode.db runs in WAL mode (-wal/-shm siblings live beside
	// it); the stress fixture must match or its locking behavior tests a
	// database OpenCode never writes.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		t.Fatalf("enable WAL: %v", err)
	}
	if _, err := db.Exec("INSERT INTO project VALUES ('p1', '/stress/repo')"); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	hostileTitles := []string{
		"quote'and\"double",
		"unicode ∑ ≤ ≥ 🚀 ‱",
		"tab\tand\nnewline",
		strings.Repeat("long", 500),
		"",
		"; rm -rf / --no-preserve-root",
	}
	bigPrompt := strings.Repeat("prompt body ", 8192) // ~100KB
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for i := 0; i < count; i++ {
		parent := any(nil)
		if i%7 == 3 { // every seventh row is a subagent child
			parent = fmt.Sprintf("ses_%d", i-1)
		}
		var archived any
		if i%11 == 5 {
			archived = int64(i * 100)
		}
		if _, err := tx.Exec(
			"INSERT INTO session (id, project_id, parent_id, slug, directory, title, version, time_created, time_updated, time_archived) VALUES (?, 'p1', ?, ?, ?, ?, '1.14.30', ?, ?, ?)",
			fmt.Sprintf("ses_%d", i),
			parent,
			fmt.Sprintf("session-%d", i),
			[]any{"/stress/repo", "", "/stress/repo/"}[i%3],
			hostileTitles[i%len(hostileTitles)],
			int64(i),
			int64(i), // duplicate timestamps on purpose
			archived,
		); err != nil {
			t.Fatalf("seed session %d: %v", i, err)
		}
		prompt := bigPrompt
		if i%2 == 0 {
			prompt = hostileTitles[i%len(hostileTitles)]
		}
		messageID := fmt.Sprintf("msg_%d", i)
		if _, err := tx.Exec(
			`INSERT INTO message (id, session_id, time_created, time_updated, data)
			 VALUES (?, ?, ?, ?, json_object(
			   'role','user', 'agent','build',
			   'model',json_object('providerID','stress','modelID','fixture')
			 ))`,
			messageID, fmt.Sprintf("ses_%d", i), int64(i), int64(i),
		); err != nil {
			t.Fatalf("seed message %d: %v", i, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data)
			 VALUES (?, ?, ?, ?, ?, json_object('type','text','text',?))`,
			fmt.Sprintf("part_%d", i), messageID, fmt.Sprintf("ses_%d", i), int64(i), int64(i), prompt,
		); err != nil {
			t.Fatalf("seed part %d: %v", i, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO message (id, session_id, time_created, time_updated, data)
			 VALUES (?, ?, ?, ?, json_object(
			   'role','assistant', 'agent','build',
			   'providerID','stress', 'modelID','fixture',
			   'tokens',json_object('input',?,'output',?),
			   'cost',?
			 ))`,
			fmt.Sprintf("assistant_%d", i), fmt.Sprintf("ses_%d", i),
			int64(i), int64(i), i, i, float64(i%97)/7.0,
		); err != nil {
			t.Fatalf("seed assistant %d: %v", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestStressOpencodeIndexSurvivesHostileStore(t *testing.T) {
	t.Parallel()

	const count = 300
	root := t.TempDir()
	seedOpencodeStress(t, root, count)

	for pass := 0; pass < 2; pass++ {
		sessions, err := ReadOpencodeSessions(context.Background(), root)
		if err != nil {
			t.Fatalf("pass %d read: %v", pass, err)
		}
		if len(sessions) != count {
			t.Fatalf("pass %d read %d sessions, want %d", pass, len(sessions), count)
		}
		children, archived := 0, 0
		for _, session := range sessions {
			if session.ParentID != "" {
				children++
			}
			if session.TimeArchivedMS != 0 {
				archived++
			}
			if len([]rune(session.FirstPrompt)) > 201 {
				t.Fatalf("first prompt escaped its clip: %d runes", len([]rune(session.FirstPrompt)))
			}
		}
		wantChildren := countChildren(count)
		if children != wantChildren {
			t.Errorf("children = %d, want %d", children, wantChildren)
		}
		if archived != countArchived(count) {
			t.Errorf("archived = %d, computed %d", archived, countArchived(count))
		}
	}
}

func countChildren(n int) int {
	total := 0
	for i := 0; i < n; i++ {
		if i%7 == 3 {
			total++
		}
	}
	return total
}

func countArchived(n int) int {
	total := 0
	for i := 0; i < n; i++ {
		if i%11 == 5 {
			total++
		}
	}
	return total
}

// The mirror must converge under repeated full passes and concurrent readers:
// multiple goroutines indexing the same store race the mirror-replace path.
func TestStressOpencodeMirrorConcurrentPasses(t *testing.T) {
	const count = 250
	root := t.TempDir()
	seedOpencodeStress(t, root, count)

	jail := t.TempDir()
	t.Setenv(paths.EnvDB, filepath.Join(jail, "fleet.db"))
	t.Setenv(paths.EnvSharedDB, filepath.Join(jail, "shared.db"))
	database, err := store.Open()
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()

	var wg sync.WaitGroup
	const workers = 3
	errs := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var counters Counters
			if err := syncOpencodeMirror(context.Background(), database, root, &counters); err != nil {
				errs <- err
				return
			}
			if counters.OcSessions != count {
				errs <- fmt.Errorf("OcSessions = %d, want %d", counters.OcSessions, count)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	stored, err := database.OcSessions(context.Background())
	if err != nil {
		t.Fatalf("read mirror: %v", err)
	}
	if len(stored) != count {
		t.Fatalf("mirror holds %d rows, want %d", len(stored), count)
	}
}

// A live OpenCode process checkpoints into the WAL while we copy; the reader
// must tolerate the database growing mid-copy without erroring or hanging.
func TestStressOpencodeReadWhileWriterActive(t *testing.T) {
	t.Parallel()

	const (
		count      = 100
		liveWrites = 2000
	)
	root := t.TempDir()
	seedOpencodeStress(t, root, count)

	dbPath := filepath.Join(root, "opencode.db")
	stop := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		// A zero-timeout fixture can fail while SQLite performs ordinary WAL
		// recovery even though the reader never blocks a write transaction. Give
		// the writer one bounded lock wait, matching the production reader's
		// contract: persistent contention still fails after five seconds.
		live, err := sql.Open("sqlite", "file:"+dbPath+"?_pragma=busy_timeout(5000)")
		if err != nil {
			writerDone <- err
			return
		}
		var writerErr error
		defer func() {
			writerDone <- errors.Join(writerErr, live.Close())
		}()
		for round := 0; round < liveWrites; round++ {
			select {
			case <-stop:
				return
			default:
			}
			tx, err := live.Begin()
			if err != nil {
				writerErr = err
				return
			}
			messageID := fmt.Sprintf("live_msg_%d", round)
			timestamp := int64(1_000_000 + round)
			if _, err := tx.Exec(
				`INSERT INTO message (id, session_id, time_created, time_updated, data)
				 VALUES (?, 'ses_0', ?, ?, json_object(
				   'role','user', 'agent','build',
				   'model',json_object('providerID','stress','modelID','fixture')
				 ))`,
				messageID, timestamp, timestamp,
			); err != nil {
				_ = tx.Rollback()
				writerErr = err
				return
			}
			if _, err := tx.Exec(
				`INSERT INTO part (id, message_id, session_id, time_created, time_updated, data)
				 VALUES (?, ?, 'ses_0', ?, ?, json_object('type','text','text',?))`,
				fmt.Sprintf("live_part_%d", round), messageID, timestamp, timestamp, strings.Repeat("w", 2000),
			); err != nil {
				_ = tx.Rollback()
				writerErr = err
				return
			}
			if err := tx.Commit(); err != nil {
				writerErr = err
				return
			}
			// Yield the write lock: OpenCode writes in bursts between turns,
			// never as a zero-gap spin, and a spin starves the snapshot.
			time.Sleep(2 * time.Millisecond)
		}
		// Keep the writer connection live after the bounded write burst. An
		// unbounded producer makes the fixture itself grow faster than a reader
		// can finish under CPU contention, turning a concurrency probe into an
		// ever-expanding benchmark that times out by construction.
		<-stop
	}()

	reads := 0
	deadline := 5
	for round := 0; round < deadline; round++ {
		sessions, err := ReadOpencodeSessions(context.Background(), root)
		if err != nil {
			close(stop)
			t.Fatalf("concurrent read %d failed: %v", round, err)
		}
		if len(sessions) != count {
			close(stop)
			t.Fatalf("concurrent read %d saw %d sessions, want %d", round, len(sessions), count)
		}
		reads++
	}
	close(stop)
	if err := <-writerDone; err != nil {
		t.Fatalf("writer failed: %v", err)
	}
	if reads != deadline {
		t.Fatalf("completed %d reads, want %d", reads, deadline)
	}
}
