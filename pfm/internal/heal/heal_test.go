package heal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

// codexJail builds a scratch Codex home: a state store, a history store, and
// the rollouts they point at. The real ~/.codex is never opened by a test —
// healing DELETES projection rows, and a fixture that could reach the live
// store is a fixture that can lose a chat.
type codexJail struct {
	root    string
	stores  Stores
	history *sql.DB
}

func newCodexJail(t *testing.T) *codexJail {
	t.Helper()
	root := t.TempDir()
	// Two generations on purpose: the newest is the live one, and picking the
	// wrong one heals a store Codex no longer reads.
	for _, name := range []string{
		"state_1.sqlite",
		"thread_history_1.sqlite",
	} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	statePath := filepath.Join(root, "state_2.sqlite")
	historyPath := filepath.Join(root, "thread_history_2.sqlite")

	state := openJailDB(t, statePath)
	execJail(t, state, `CREATE TABLE threads (
		id TEXT PRIMARY KEY,
		rollout_path TEXT
	)`)
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	history := openJailDB(t, historyPath)
	execJail(t, history, `CREATE TABLE thread_history_projection_state (
		thread_id TEXT PRIMARY KEY,
		next_rollout_byte_offset INTEGER,
		next_rollout_ordinal INTEGER
	)`)
	execJail(t, history, `CREATE TABLE thread_items (
		thread_id TEXT, ordinal INTEGER
	)`)
	execJail(t, history, `CREATE TABLE thread_turns (
		thread_id TEXT, ordinal INTEGER
	)`)
	t.Cleanup(func() { _ = history.Close() })

	return &codexJail{
		root:    root,
		stores:  Stores{State: statePath, History: historyPath, Root: root},
		history: history,
	}
}

func openJailDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	return database
}

func execJail(t *testing.T, database *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := database.Exec(statement, args...); err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
}

// addThread writes a rollout of `records` JSONL lines and registers the
// thread, returning the byte offset of each record.
func (jail *codexJail) addThread(t *testing.T, id string, records int) []int64 {
	t.Helper()
	path := filepath.Join(jail.root, "sessions", "rollout-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	var content strings.Builder
	offsets := make([]int64, 0, records)
	for index := 0; index < records; index++ {
		offsets = append(offsets, int64(content.Len()))
		content.WriteString(fmt.Sprintf(
			`{"ordinal":%d,"type":"event_msg","payload":{"n":%d}}`+"\n",
			index,
			index,
		))
	}
	if err := os.WriteFile(path, []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	state := openJailDB(t, jail.stores.State)
	defer func() {
		if err := state.Close(); err != nil {
			t.Errorf("close state: %v", err)
		}
	}()
	execJail(
		t,
		state,
		"INSERT INTO threads(id, rollout_path) VALUES(?, ?)",
		id,
		path,
	)
	return append(offsets, int64(content.Len()))
}

// jailRecord is one JSONL line for addThreadWithRecords: a canonical record
// sets only Ordinal (nil means "carries no ordinal"), and an anomaly record
// layers on the record/payload type the Detail message must surface.
type jailRecord struct {
	Ordinal     *int64
	Type        string
	PayloadType string
}

func int64p(value int64) *int64 { return &value }

// addThreadWithRecords writes a rollout built from explicit records — rather
// than addThread's canonical 0,1,2,… convenience — so a test can shape a
// duplicate, skipped, or missing ordinal exactly where it needs one, and
// registers the thread. It returns the byte offset of each record plus the
// file's final length, matching addThread's offsets contract.
func (jail *codexJail) addThreadWithRecords(t *testing.T, id string, records []jailRecord) []int64 {
	t.Helper()
	path := filepath.Join(jail.root, "sessions", "rollout-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	var content strings.Builder
	offsets := make([]int64, 0, len(records))
	for _, record := range records {
		offsets = append(offsets, int64(content.Len()))
		recordType := record.Type
		if recordType == "" {
			recordType = "event_msg"
		}
		payloadType := record.PayloadType
		if payloadType == "" {
			payloadType = "message"
		}
		ordinalField := "null"
		if record.Ordinal != nil {
			ordinalField = strconv.FormatInt(*record.Ordinal, 10)
		}
		content.WriteString(fmt.Sprintf(
			`{"ordinal":%s,"type":%q,"payload":{"type":%q}}`+"\n",
			ordinalField,
			recordType,
			payloadType,
		))
	}
	if err := os.WriteFile(path, []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	state := openJailDB(t, jail.stores.State)
	defer func() {
		if err := state.Close(); err != nil {
			t.Errorf("close state: %v", err)
		}
	}()
	execJail(
		t,
		state,
		"INSERT INTO threads(id, rollout_path) VALUES(?, ?)",
		id,
		path,
	)
	return append(offsets, int64(content.Len()))
}

// addThreadWithLines writes a rollout from raw physical lines — no JSON
// encoding applied — so a test can plant a line that will not parse at all,
// which addThreadWithRecords can never produce. It registers the thread and
// returns offsets matching addThread's / addThreadWithRecords' contract.
func (jail *codexJail) addThreadWithLines(t *testing.T, id string, lines []string) []int64 {
	t.Helper()
	path := filepath.Join(jail.root, "sessions", "rollout-"+id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	var content strings.Builder
	offsets := make([]int64, 0, len(lines))
	for _, line := range lines {
		offsets = append(offsets, int64(content.Len()))
		content.WriteString(line)
		content.WriteString("\n")
	}
	if err := os.WriteFile(path, []byte(content.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	state := openJailDB(t, jail.stores.State)
	defer func() {
		if err := state.Close(); err != nil {
			t.Errorf("close state: %v", err)
		}
	}()
	execJail(
		t,
		state,
		"INSERT INTO threads(id, rollout_path) VALUES(?, ?)",
		id,
		path,
	)
	return append(offsets, int64(content.Len()))
}

func (jail *codexJail) setCursor(t *testing.T, id string, offset, ordinal int64) {
	t.Helper()
	execJail(
		t,
		jail.history,
		"INSERT INTO thread_history_projection_state("+
			"thread_id, next_rollout_byte_offset, next_rollout_ordinal) VALUES(?,?,?)",
		id,
		offset,
		ordinal,
	)
	execJail(
		t,
		jail.history,
		"INSERT INTO thread_items(thread_id, ordinal) VALUES(?,?)",
		id,
		ordinal,
	)
	execJail(
		t,
		jail.history,
		"INSERT INTO thread_turns(thread_id, ordinal) VALUES(?,?)",
		id,
		ordinal,
	)
}

func (jail *codexJail) projectionRows(t *testing.T, id string) int {
	t.Helper()
	total := 0
	for _, table := range []string{
		"thread_history_projection_state",
		"thread_items",
		"thread_turns",
	} {
		var count int
		if err := jail.history.QueryRow(
			"SELECT count(*) FROM "+table+" WHERE thread_id = ?",
			id,
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		total += count
	}
	return total
}

// FindStores must land on the NEWEST generation of each store: Codex leaves
// the old ones behind, and healing one it no longer reads changes nothing
// while reporting success.
func TestFindStoresTakesTheNewestGeneration(t *testing.T) {
	jail := newCodexJail(t)
	stores, err := FindStores(jail.root)
	if err != nil {
		t.Fatalf("FindStores() error = %v", err)
	}
	if filepath.Base(stores.State) != "state_2.sqlite" {
		t.Fatalf("state store = %s, want state_2.sqlite", stores.State)
	}
	if filepath.Base(stores.History) != "thread_history_2.sqlite" {
		t.Fatalf("history store = %s, want thread_history_2.sqlite", stores.History)
	}
	if _, err := FindStores(filepath.Join(jail.root, "nowhere")); err == nil {
		t.Fatal("FindStores() accepted a Codex home that is not there")
	}
}

// The verdicts are the whole diagnosis, and each one has a distinct cause.
func TestSweepJudgesEveryCursorShape(t *testing.T) {
	jail := newCodexJail(t)
	const (
		caughtUp   = "11111111-1111-4111-8111-111111111111"
		consistent = "22222222-2222-4222-8222-222222222222"
		wedged     = "33333333-3333-4333-8333-333333333333"
		midline    = "44444444-4444-4444-8444-444444444444"
		noRollout  = "55555555-5555-4555-8555-555555555555"
	)
	caughtUpOffsets := jail.addThread(t, caughtUp, 3)
	consistentOffsets := jail.addThread(t, consistent, 3)
	wedgedOffsets := jail.addThread(t, wedged, 3)
	midlineOffsets := jail.addThread(t, midline, 3)

	jail.setCursor(t, caughtUp, caughtUpOffsets[3], 3)
	jail.setCursor(t, consistent, consistentOffsets[1], 1)
	// The 0.146.1 shape: the offset advanced, the ordinal did not.
	jail.setCursor(t, wedged, wedgedOffsets[2], 1)
	jail.setCursor(t, midline, midlineOffsets[1]+5, 1)
	jail.setCursor(t, noRollout, 0, 0)
	state := openJailDB(t, jail.stores.State)
	execJail(
		t,
		state,
		"INSERT INTO threads(id, rollout_path) VALUES(?, ?)",
		noRollout,
		filepath.Join(jail.root, "sessions", "gone.jsonl"),
	)
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	report, err := Sweep(context.Background(), jail.stores, "")
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	want := map[string]Verdict{
		caughtUp:   VerdictCaughtUp,
		consistent: VerdictConsistent,
		wedged:     VerdictWedged,
		midline:    VerdictMidline,
		noRollout:  VerdictNoRollout,
	}
	if len(report.Threads) != len(want) {
		t.Fatalf("Sweep() returned %d threads, want %d", len(report.Threads), len(want))
	}
	for _, thread := range report.Threads {
		if thread.Verdict != want[thread.ID] {
			t.Fatalf(
				"%s = %s (%s), want %s",
				thread.ID,
				thread.Verdict,
				thread.Detail,
				want[thread.ID],
			)
		}
	}
	if report.Totals[VerdictWedged] != 1 || report.Totals[VerdictMidline] != 1 {
		t.Fatalf("totals = %v", report.Totals)
	}
}

// A sweep is read-only: reporting must never change a projection.
func TestSweepChangesNothing(t *testing.T) {
	jail := newCodexJail(t)
	const wedged = "33333333-3333-4333-8333-333333333333"
	offsets := jail.addThread(t, wedged, 3)
	jail.setCursor(t, wedged, offsets[2], 1)
	if _, err := Sweep(context.Background(), jail.stores, ""); err != nil {
		t.Fatal(err)
	}
	if rows := jail.projectionRows(t, wedged); rows != 3 {
		t.Fatalf("a report deleted projection rows: %d remain, want 3", rows)
	}
}

// --apply heals exactly the broken threads, backs up first, and leaves every
// healthy projection alone.
func TestApplyHealsOnlyBrokenThreads(t *testing.T) {
	jail := newCodexJail(t)
	const (
		healthy = "22222222-2222-4222-8222-222222222222"
		wedged  = "33333333-3333-4333-8333-333333333333"
	)
	healthyOffsets := jail.addThread(t, healthy, 3)
	wedgedOffsets := jail.addThread(t, wedged, 3)
	jail.setCursor(t, healthy, healthyOffsets[1], 1)
	jail.setCursor(t, wedged, wedgedOffsets[2], 1)

	runner, err := New(jail.root, func() time.Time {
		return time.Unix(1800000000, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := runner.Run(context.Background(), Options{Apply: true})
	if err != nil {
		t.Fatalf("Run(--apply) error = %v", err)
	}
	if len(report.Healed) != 1 || report.Healed[0] != wedged {
		t.Fatalf("healed = %v, want [%s]", report.Healed, wedged)
	}
	if rows := jail.projectionRows(t, wedged); rows != 0 {
		t.Fatalf("the wedged projection still holds %d rows", rows)
	}
	if rows := jail.projectionRows(t, healthy); rows != 3 {
		t.Fatalf("a healthy projection lost rows: %d remain, want 3", rows)
	}
	if report.BackupDir == "" {
		t.Fatal("a heal ran with no backup")
	}
	if _, err := os.Stat(filepath.Join(
		report.BackupDir,
		filepath.Base(jail.stores.History),
	)); err != nil {
		t.Fatalf("the history store was not backed up: %v", err)
	}

	// Idempotent: the healed thread has no cursor left, so a second run finds
	// nothing to do and writes nothing.
	second, err := runner.Run(context.Background(), Options{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Healed) != 0 {
		t.Fatalf("a second run healed %v", second.Healed)
	}
	if second.BackupDir != "" {
		t.Fatal("a second run took a backup with nothing to heal")
	}
}

// A thread another seat is holding is never healed: Codex carries that
// cursor in memory and would write it back over the repair.
func TestLiveThreadsAreSkipped(t *testing.T) {
	jail := newCodexJail(t)
	const wedged = "33333333-3333-4333-8333-333333333333"
	offsets := jail.addThread(t, wedged, 3)
	jail.setCursor(t, wedged, offsets[2], 1)

	locks := filepath.Join(jail.root, "thread-writer-locks")
	if err := os.MkdirAll(locks, 0o700); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(locks, wedged+".lock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Errorf("close lock: %v", err)
		}
	}()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatalf("hold the writer lock: %v", err)
	}
	if !Live(jail.root, wedged) {
		t.Fatal("a held writer lock did not read as live")
	}

	runner, err := New(jail.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := runner.Run(context.Background(), Options{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Healed) != 0 {
		t.Fatalf("a live thread was healed: %v", report.Healed)
	}
	if len(report.SkippedLive) != 1 || report.SkippedLive[0] != wedged {
		t.Fatalf("skipped-live = %v, want [%s]", report.SkippedLive, wedged)
	}
	if rows := jail.projectionRows(t, wedged); rows != 3 {
		t.Fatalf("a live thread's projection was deleted anyway (%d rows left)", rows)
	}

	// Released, the same thread heals.
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if Live(jail.root, wedged) {
		t.Fatal("a released lock still reads as live")
	}
	report, err = runner.Run(context.Background(), Options{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Healed) != 1 {
		t.Fatalf("healed = %v after the lock was released", report.Healed)
	}
}

// Thread is the pre-resume shape: it repairs a broken thread, says so in one
// line, and is completely silent about everything else — including a Codex
// home that does not exist, because a resume must never fail over a repair.
func TestThreadIsSilentUnlessItRepairs(t *testing.T) {
	jail := newCodexJail(t)
	const (
		healthy = "22222222-2222-4222-8222-222222222222"
		wedged  = "33333333-3333-4333-8333-333333333333"
	)
	healthyOffsets := jail.addThread(t, healthy, 3)
	wedgedOffsets := jail.addThread(t, wedged, 3)
	jail.setCursor(t, healthy, healthyOffsets[1], 1)
	jail.setCursor(t, wedged, wedgedOffsets[2], 1)

	ctx := context.Background()
	if message := Thread(ctx, jail.root, healthy); message != "" {
		t.Fatalf("a healthy thread reported %q", message)
	}
	if rows := jail.projectionRows(t, wedged); rows != 3 {
		t.Fatalf("healing one thread touched another")
	}
	message := Thread(ctx, jail.root, wedged)
	if !strings.Contains(message, wedged) ||
		!strings.Contains(message, string(VerdictWedged)) {
		t.Fatalf("Thread() = %q, want a line naming the thread and its verdict", message)
	}
	if rows := jail.projectionRows(t, wedged); rows != 0 {
		t.Fatalf("Thread() reported a heal but left %d rows", rows)
	}
	if message := Thread(ctx, filepath.Join(jail.root, "nowhere"), wedged); message != "" {
		t.Fatalf("a missing Codex home reported %q instead of staying silent", message)
	}
	if message := Thread(ctx, jail.root, ""); message != "" {
		t.Fatalf("an empty thread id reported %q", message)
	}
}

// A duplicate resume-boundary rollout (openai/codex#38792) must never read
// as the 0.146.1 stale-cursor shape: WEDGED there means "a rebuild from zero
// is safe", and on this shape a rebuild from zero fails on the same record
// again on Codex < 0.154.0.
func TestDuplicateOrdinalIsNoncanonicalNotWedged(t *testing.T) {
	jail := newCodexJail(t)
	const id = "66666666-6666-4666-8666-666666666666"
	offsets := jail.addThreadWithRecords(t, id, []jailRecord{
		{Ordinal: int64p(0)},
		{Ordinal: int64p(1)},
		{Ordinal: int64p(2), PayloadType: "token_count"},
		{Ordinal: int64p(2), PayloadType: "thread_settings_applied"},
		{Ordinal: int64p(3)},
		{Ordinal: int64p(4)},
	})
	// The cursor sits at the SECOND ordinal-2 record expecting the next
	// ordinal, 3 — the resumed writer reused the ordinal instead of
	// desyncing offset from ordinal.
	jail.setCursor(t, id, offsets[3], 3)

	report, err := Sweep(context.Background(), jail.stores, "")
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if len(report.Threads) != 1 {
		t.Fatalf("Sweep() returned %d threads, want 1", len(report.Threads))
	}
	thread := report.Threads[0]
	if thread.Verdict != VerdictNoncanonical {
		t.Fatalf("verdict = %s (%s), want NONCANONICAL", thread.Verdict, thread.Detail)
	}
	if !strings.Contains(thread.Detail, "repeats ordinal 2") {
		t.Fatalf("Detail = %q, want it to name the repeated ordinal", thread.Detail)
	}
	if !strings.Contains(thread.Detail, "event_msg/thread_settings_applied") {
		t.Fatalf("Detail = %q, want the anomalous record's type", thread.Detail)
	}
}

// A NONCANONICAL projection is never touched by --apply, and a sweep of
// nothing but a noncanonical thread must not even take a backup: there is
// nothing broken to protect a copy of.
func TestApplyNeverDeletesANoncanonicalProjection(t *testing.T) {
	duplicateRecords := func() []jailRecord {
		return []jailRecord{
			{Ordinal: int64p(0)},
			{Ordinal: int64p(1)},
			{Ordinal: int64p(2), PayloadType: "token_count"},
			{Ordinal: int64p(2), PayloadType: "thread_settings_applied"},
			{Ordinal: int64p(3)},
			{Ordinal: int64p(4)},
		}
	}

	t.Run("mixed with a canonical wedge", func(t *testing.T) {
		jail := newCodexJail(t)
		const (
			canonical    = "22222222-2222-4222-8222-222222222222"
			noncanonical = "66666666-6666-4666-8666-666666666666"
		)
		canonicalOffsets := jail.addThread(t, canonical, 3)
		jail.setCursor(t, canonical, canonicalOffsets[2], 1)
		noncanonicalOffsets := jail.addThreadWithRecords(t, noncanonical, duplicateRecords())
		jail.setCursor(t, noncanonical, noncanonicalOffsets[3], 3)

		runner, err := New(jail.root, func() time.Time { return time.Unix(1800000000, 0) })
		if err != nil {
			t.Fatal(err)
		}
		report, err := runner.Run(context.Background(), Options{Apply: true})
		if err != nil {
			t.Fatalf("Run(--apply) error = %v", err)
		}
		if len(report.Healed) != 1 || report.Healed[0] != canonical {
			t.Fatalf("healed = %v, want [%s]", report.Healed, canonical)
		}
		if rows := jail.projectionRows(t, noncanonical); rows != 3 {
			t.Fatalf("a noncanonical projection lost rows: %d remain, want 3", rows)
		}
		if len(report.LeftAlone) != 1 || report.LeftAlone[0] != noncanonical {
			t.Fatalf("LeftAlone = %v, want [%s]", report.LeftAlone, noncanonical)
		}
	})

	t.Run("alone takes no backup", func(t *testing.T) {
		jail := newCodexJail(t)
		const noncanonical = "66666666-6666-4666-8666-666666666666"
		offsets := jail.addThreadWithRecords(t, noncanonical, duplicateRecords())
		jail.setCursor(t, noncanonical, offsets[3], 3)

		runner, err := New(jail.root, nil)
		if err != nil {
			t.Fatal(err)
		}
		report, err := runner.Run(context.Background(), Options{Apply: true})
		if err != nil {
			t.Fatalf("Run(--apply) error = %v", err)
		}
		if report.BackupDir != "" {
			t.Fatalf("a run with only a noncanonical thread took a backup: %s", report.BackupDir)
		}
		if rows := jail.projectionRows(t, noncanonical); rows != 3 {
			t.Fatalf("a noncanonical projection lost rows: %d remain, want 3", rows)
		}
	})
}

// A skipped ordinal is the same NONCANONICAL refusal as a duplicate: Codex
// < 0.154.0 does not distinguish a repeat, a regression, and a gap — it
// refuses the projection forever at the first non-canonical record either
// way, so a rebuild from zero fails there too.
func TestGapIsNoncanonical(t *testing.T) {
	jail := newCodexJail(t)
	const id = "77777777-7777-4777-8777-777777777777"
	offsets := jail.addThreadWithRecords(t, id, []jailRecord{
		{Ordinal: int64p(0)},
		{Ordinal: int64p(1)},
		{Ordinal: int64p(3)},
		{Ordinal: int64p(4)},
	})
	// The 0.146.1 stale-cursor shape: the cursor claims ordinal 3 lives at
	// the last record's offset, but the file there carries 4. Only a full
	// scan reveals the gap at ordinal 2 that makes a rebuild from zero fail
	// on the same record again.
	jail.setCursor(t, id, offsets[3], 3)

	report, err := Sweep(context.Background(), jail.stores, "")
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if len(report.Threads) != 1 {
		t.Fatalf("Sweep() returned %d threads, want 1", len(report.Threads))
	}
	thread := report.Threads[0]
	if thread.Verdict != VerdictNoncanonical {
		t.Fatalf("verdict = %s (%s), want NONCANONICAL", thread.Verdict, thread.Detail)
	}
	if !strings.Contains(thread.Detail, "skips to ordinal 3") {
		t.Fatalf("Detail = %q, want it to name the skip", thread.Detail)
	}
}

// The 0.146.1 shape — a canonical ordinal sequence, just a stale cursor —
// must still report WEDGED and still heal under --apply: NONCANONICAL must
// not swallow the case this package was built for (guards against
// over-refusal).
func TestCanonicalWedgeStillHeals(t *testing.T) {
	jail := newCodexJail(t)
	const wedged = "33333333-3333-4333-8333-333333333333"
	offsets := jail.addThread(t, wedged, 3)
	jail.setCursor(t, wedged, offsets[2], 1)

	report, err := Sweep(context.Background(), jail.stores, "")
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if len(report.Threads) != 1 || report.Threads[0].Verdict != VerdictWedged {
		t.Fatalf("Sweep() = %v, want a single WEDGED thread", report.Threads)
	}

	runner, err := New(jail.root, func() time.Time { return time.Unix(1800000000, 0) })
	if err != nil {
		t.Fatal(err)
	}
	applied, err := runner.Run(context.Background(), Options{Apply: true})
	if err != nil {
		t.Fatalf("Run(--apply) error = %v", err)
	}
	if len(applied.Healed) != 1 || applied.Healed[0] != wedged {
		t.Fatalf("healed = %v, want [%s]", applied.Healed, wedged)
	}
	if rows := jail.projectionRows(t, wedged); rows != 0 {
		t.Fatalf("a canonical wedged projection was left alone: %d rows remain", rows)
	}
}

// Thread() on a noncanonical rollout must say so and leave the projection
// exactly as it found it — the opposite of Thread()'s WEDGED behavior, which
// deletes and rebuilds.
func TestThreadReportsALeftAloneRollout(t *testing.T) {
	jail := newCodexJail(t)
	const id = "66666666-6666-4666-8666-666666666666"
	offsets := jail.addThreadWithRecords(t, id, []jailRecord{
		{Ordinal: int64p(0)},
		{Ordinal: int64p(1)},
		{Ordinal: int64p(2), PayloadType: "token_count"},
		{Ordinal: int64p(2), PayloadType: "thread_settings_applied"},
		{Ordinal: int64p(3)},
		{Ordinal: int64p(4)},
	})
	jail.setCursor(t, id, offsets[3], 3)

	message := Thread(context.Background(), jail.root, id)
	if !strings.Contains(message, id) ||
		!strings.Contains(message, "noncanonical") ||
		!strings.Contains(message, CodexProjectsPastAnomalies) {
		t.Fatalf(
			"Thread() = %q, want the id, \"noncanonical\", and %s",
			message,
			CodexProjectsPastAnomalies,
		)
	}
	if rows := jail.projectionRows(t, id); rows != 3 {
		t.Fatalf("Thread() touched a noncanonical projection: %d rows remain, want 3", rows)
	}
}

// TestUnreadableRolloutIsUnscanned pins UNSCANNED: a WEDGED/MIDLINE cursor
// whose rollout cannot be read end to end must be left alone, the same as
// NONCANONICAL, because "we failed to look" is not "nothing there".
//
// Injection seam: scanOrdinals reads through a package-level var
// `openRollout func(path string) (io.ReadCloser, error)` that classify calls
// instead of os.Open directly. The test swaps it for a reader that fails
// mid-stream, without touching the filesystem — a permission bit is not
// portable across CI, and a directory masquerading as a rollout never
// reaches the scan (os.Stat routes it to NO_ROLLOUT upstream of classify's
// WEDGED branch, before scanOrdinals is ever called).
func TestUnreadableRolloutIsUnscanned(t *testing.T) {
	jail := newCodexJail(t)
	const wedged = "33333333-3333-4333-8333-333333333333"
	offsets := jail.addThread(t, wedged, 3)
	jail.setCursor(t, wedged, offsets[2], 1)

	original := openRollout
	t.Cleanup(func() { openRollout = original })
	openRollout = func(path string) (io.ReadCloser, error) {
		return io.NopCloser(iotest.ErrReader(errors.New("disk fell off"))), nil
	}

	report, err := Sweep(context.Background(), jail.stores, "")
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if len(report.Threads) != 1 {
		t.Fatalf("Sweep() returned %d threads, want 1", len(report.Threads))
	}
	thread := report.Threads[0]
	if thread.Verdict != VerdictUnscanned {
		t.Fatalf("verdict = %s (%s), want UNSCANNED", thread.Verdict, thread.Detail)
	}
	if !strings.Contains(thread.Detail, "disk fell off") {
		t.Fatalf("Detail = %q, want the read error", thread.Detail)
	}

	runner, err := New(jail.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := runner.Run(context.Background(), Options{Apply: true})
	if err != nil {
		t.Fatalf("Run(--apply) error = %v", err)
	}
	if len(applied.Healed) != 0 {
		t.Fatalf("an UNSCANNED thread was healed: %v", applied.Healed)
	}
	if rows := jail.projectionRows(t, wedged); rows != 3 {
		t.Fatalf("an UNSCANNED projection lost rows: %d remain, want 3", rows)
	}
}

// A record with no "ordinal" key at all is the same NONCANONICAL refusal as
// a duplicate or a gap: scanOrdinals' "carries no ordinal" Kind, reached
// through a stale (WEDGED) cursor, and --apply must leave the projection
// untouched, taking no backup.
func TestRecordWithoutOrdinalIsNoncanonical(t *testing.T) {
	jail := newCodexJail(t)
	const id = "88888888-8888-4888-8888-888888888888"
	offsets := jail.addThreadWithRecords(t, id, []jailRecord{
		{Ordinal: int64p(0)},
		{Ordinal: int64p(1)},
		{Ordinal: nil, Type: "event_msg", PayloadType: "token_count"},
		{Ordinal: int64p(2)},
		{Ordinal: int64p(3)},
	})
	// A stale cursor: it claims an ordinal the file does not carry at that
	// offset, so classify reports WEDGED and the full-file scan runs.
	jail.setCursor(t, id, offsets[4], 99)

	report, err := Sweep(context.Background(), jail.stores, "")
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if len(report.Threads) != 1 {
		t.Fatalf("Sweep() returned %d threads, want 1", len(report.Threads))
	}
	thread := report.Threads[0]
	if thread.Verdict != VerdictNoncanonical {
		t.Fatalf("verdict = %s (%s), want NONCANONICAL", thread.Verdict, thread.Detail)
	}
	if !strings.Contains(thread.Detail, "line 3 carries no ordinal") {
		t.Fatalf("Detail = %q, want it to name the missing-ordinal line", thread.Detail)
	}
	if !strings.Contains(thread.Detail, "event_msg/token_count") {
		t.Fatalf("Detail = %q, want the anomalous record's type", thread.Detail)
	}

	runner, err := New(jail.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := runner.Run(context.Background(), Options{Apply: true})
	if err != nil {
		t.Fatalf("Run(--apply) error = %v", err)
	}
	if len(applied.Healed) != 0 {
		t.Fatalf("a NONCANONICAL thread was healed: %v", applied.Healed)
	}
	if rows := jail.projectionRows(t, id); rows != 3 {
		t.Fatalf("a NONCANONICAL projection lost rows: %d remain, want 3", rows)
	}
	if applied.BackupDir != "" {
		t.Fatalf("a run with only a NONCANONICAL thread took a backup: %s", applied.BackupDir)
	}
}

// A physical line that will not parse as JSON at all is the same
// NONCANONICAL refusal as any other anomaly — scanOrdinals' "is unparseable"
// Kind. An unparseable record carries no type to report, so the Detail's
// trailing "(%s)" clause must not render as the empty, meaningless "()".
func TestUnparseableRecordIsNoncanonical(t *testing.T) {
	jail := newCodexJail(t)
	const id = "99999999-9999-4999-8999-999999999999"
	offsets := jail.addThreadWithLines(t, id, []string{
		`{"ordinal":0,"type":"event_msg","payload":{"type":"message"}}`,
		`{"ordinal":1,"type":"event_msg","payload":{"type":"message"}}`,
		`{"ordinal":2,"type":"event_msg"`, // truncated: not valid JSON
		`{"ordinal":2,"type":"event_msg","payload":{"type":"message"}}`,
		`{"ordinal":3,"type":"event_msg","payload":{"type":"message"}}`,
	})
	// A stale cursor: it claims an ordinal the file does not carry at that
	// offset, so classify reports WEDGED and the full-file scan runs.
	jail.setCursor(t, id, offsets[4], 99)

	report, err := Sweep(context.Background(), jail.stores, "")
	if err != nil {
		t.Fatalf("Sweep() error = %v", err)
	}
	if len(report.Threads) != 1 {
		t.Fatalf("Sweep() returned %d threads, want 1", len(report.Threads))
	}
	thread := report.Threads[0]
	if thread.Verdict != VerdictNoncanonical {
		t.Fatalf("verdict = %s (%s), want NONCANONICAL", thread.Verdict, thread.Detail)
	}
	if !strings.Contains(thread.Detail, "line 3 is unparseable") {
		t.Fatalf("Detail = %q, want it to name the unparseable line", thread.Detail)
	}
	if strings.Contains(thread.Detail, "()") {
		t.Fatalf("Detail = %q, an unparseable record has no type to print in parens", thread.Detail)
	}

	runner, err := New(jail.root, nil)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := runner.Run(context.Background(), Options{Apply: true})
	if err != nil {
		t.Fatalf("Run(--apply) error = %v", err)
	}
	if len(applied.Healed) != 0 {
		t.Fatalf("a NONCANONICAL thread was healed: %v", applied.Healed)
	}
	if rows := jail.projectionRows(t, id); rows != 3 {
		t.Fatalf("a NONCANONICAL projection lost rows: %d remain, want 3", rows)
	}
	if applied.BackupDir != "" {
		t.Fatalf("a run with only a NONCANONICAL thread took a backup: %s", applied.BackupDir)
	}
}
