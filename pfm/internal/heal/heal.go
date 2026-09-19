// Package heal repairs Codex thread-history projections so a resumed seat
// opens WHOLE instead of amnesiac at its first prompt.
//
// Codex ≥0.146 renders a resumed paginated thread from a projection of its
// rollout held in thread_history_<N>.sqlite, advanced by a stored (byte
// offset, ordinal) cursor. Codex 0.146.1 desynced that cursor on every
// paginated thread — the offset advanced past a token_count record and the
// ordinal did not — and later versions refuse to project past the
// inconsistency, forever. The thread then resumes showing only its first turn
// while the rollout on disk is complete.
//
// Deleting that thread's projection rows makes Codex rebuild the whole
// projection from the rollout at the next resume (measured: 110 MB in under
// 20 s), so the chat opens exactly as it was closed. Nothing else is touched,
// the rollout is never written, and the store is copied before any delete.
package heal

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"hostops/pfm/internal/atomicfile"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/obs"
	"hostops/pfm/internal/sqlitedb"
)

// healKind names heal's two foreign SQLite stores to the db component's
// records — heal never opens a database of its own, only Codex's.
var healKind = pfmengine.MustLookup(pfmengine.Codex).LongName

// CodexProjectsPastAnomalies is the first Codex release whose projector skips a repeated,
// regressed, or missing rollout ordinal instead of refusing the thread forever
// (openai/codex PR #42369, first tag rust-v0.154.0-alpha.1). Below it, a NONCANONICAL
// thread's projection must not be deleted: the rebuild from zero fails on the same record.
const CodexProjectsPastAnomalies = "0.154.0"

// Verdict is one thread's projection state.
type Verdict string

const (
	// VerdictCaughtUp means the projection has consumed the whole rollout.
	VerdictCaughtUp Verdict = "CAUGHT_UP"
	// VerdictConsistent means the cursor points at a record whose ordinal is
	// the one the projection expects: healthy, mid-file.
	VerdictConsistent Verdict = "CONSISTENT"
	// VerdictWedged means the record at the cursor carries a DIFFERENT
	// ordinal than the projection expects. Codex will never project past it.
	VerdictWedged Verdict = "WEDGED"
	// VerdictMidline means the cursor points inside a record rather than at
	// the start of one — the same dead end by another route.
	VerdictMidline Verdict = "MIDLINE"
	// VerdictNoRollout means the thread's rollout file is gone, so there is
	// nothing to project and nothing to repair.
	VerdictNoRollout Verdict = "NO_ROLLOUT"
	// VerdictNoncanonical means the cursor is wedged or midline AND a
	// full-file scan found the rollout's ordinal sequence is not exactly
	// 0,1,2,… — a repeated, regressed, or skipped ordinal, a record with no
	// ordinal, or an unparseable record. A rebuild from zero fails on the
	// same record on Codex < CodexProjectsPastAnomalies, so the projection
	// is left alone.
	VerdictNoncanonical Verdict = "NONCANONICAL"
	// VerdictUnscanned means the cursor is wedged or midline but the rollout
	// could not be read end to end while scanning it: "we failed to look" is
	// not "nothing there", so the projection is left alone and the read
	// error is the Detail.
	VerdictUnscanned Verdict = "UNSCANNED"
)

// Broken reports whether a verdict is one healing fixes.
func (verdict Verdict) Broken() bool {
	return verdict == VerdictWedged || verdict == VerdictMidline
}

// LeftAlone reports whether a verdict is a wedged/midline cursor healing
// refuses to touch: the rollout itself rules out a safe rebuild, or the scan
// that would tell us could not run.
func (verdict Verdict) LeftAlone() bool {
	return verdict == VerdictNoncanonical || verdict == VerdictUnscanned
}

// ThreadState is one thread's cursor, its rollout, and the verdict on them.
type ThreadState struct {
	ID          string
	RolloutPath string
	Offset      int64
	Ordinal     int64
	Size        int64
	Verdict     Verdict
	Detail      string
}

// Report is one sweep or one heal.
type Report struct {
	Threads []ThreadState
	Totals  map[Verdict]int
	// Healed and SkippedLive are filled by a heal run.
	Healed      []string
	SkippedLive []string
	BackupDir   string
	// LeftAlone holds the ids of every thread whose verdict is
	// NONCANONICAL or UNSCANNED. Sweep fills it, not Run, so a report-only
	// run carries it too.
	LeftAlone []string
}

// Stores are the two SQLite files a heal reads: Codex's thread registry and
// its history projection.
type Stores struct {
	State   string
	History string
	Root    string
}

// FindStores locates the newest generation of each store under codexHome.
// Codex leaves older generations behind when it migrates, and the highest N is
// the live one.
func FindStores(codexHome string) (Stores, error) {
	if codexHome == "" {
		return Stores{}, errors.New("no Codex home to heal")
	}
	entries, err := os.ReadDir(codexHome)
	if errors.Is(err, fs.ErrNotExist) {
		return Stores{}, fmt.Errorf("no Codex home at %s", codexHome)
	}
	if err != nil {
		return Stores{}, fmt.Errorf("read Codex home %q: %w", codexHome, err)
	}
	stores := Stores{Root: codexHome}
	stateGeneration, historyGeneration := -1, -1
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if generation, ok := storeGeneration(entry.Name(), "state_"); ok &&
			generation > stateGeneration {
			stateGeneration = generation
			stores.State = filepath.Join(codexHome, entry.Name())
		}
		if generation, ok := storeGeneration(entry.Name(), "thread_history_"); ok &&
			generation > historyGeneration {
			historyGeneration = generation
			stores.History = filepath.Join(codexHome, entry.Name())
		}
	}
	if stores.State == "" {
		return Stores{}, fmt.Errorf("no state_N.sqlite under %s", codexHome)
	}
	if stores.History == "" {
		return Stores{}, fmt.Errorf("no thread_history_N.sqlite under %s", codexHome)
	}
	return stores, nil
}

func storeGeneration(name, prefix string) (int, bool) {
	rest, found := strings.CutPrefix(name, prefix)
	if !found {
		return 0, false
	}
	rest, found = strings.CutSuffix(rest, ".sqlite")
	if !found {
		return 0, false
	}
	number, err := strconv.Atoi(rest)
	if err != nil || number < 0 {
		return 0, false
	}
	return number, true
}

// Sweep reads every projection cursor and judges it. It opens both stores
// READ-ONLY, and never immutable: an immutable handle hides the -wal and
// would judge a store Codex is actively writing from a stale snapshot.
//
// only, when set, limits the sweep to one thread id.
func Sweep(ctx context.Context, stores Stores, only string) (report Report, returnErr error) {
	rolloutByID, err := rolloutPaths(ctx, stores.State)
	if err != nil {
		return Report{}, err
	}
	history, err := openReadOnly(stores.History)
	if err != nil {
		return Report{}, err
	}
	defer func() {
		if err := history.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close history store %q: %w", stores.History, err))
		}
	}()

	query := "SELECT thread_id, next_rollout_byte_offset, next_rollout_ordinal " +
		"FROM thread_history_projection_state"
	op := obs.SQL(ctx, healKind, query)
	rows, err := history.QueryContext(ctx, query)
	op.End(-1, err)
	if err != nil {
		return Report{}, fmt.Errorf(
			"read the projection cursors in %q: %w",
			stores.History,
			err,
		)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close projection cursor rows: %w", err))
		}
	}()

	report = Report{Totals: make(map[Verdict]int)}
	for rows.Next() {
		var state ThreadState
		if err := rows.Scan(&state.ID, &state.Offset, &state.Ordinal); err != nil {
			return Report{}, fmt.Errorf("scan a projection cursor: %w", err)
		}
		if only != "" && state.ID != only {
			continue
		}
		state.RolloutPath = rolloutByID[state.ID]
		state.Verdict, state.Detail, state.Size = classify(
			state.RolloutPath,
			state.Offset,
			state.Ordinal,
		)
		report.Totals[state.Verdict]++
		report.Threads = append(report.Threads, state)
		if state.Verdict.LeftAlone() {
			report.LeftAlone = append(report.LeftAlone, state.ID)
		}
	}
	if err := rows.Err(); err != nil {
		return Report{}, fmt.Errorf("iterate the projection cursors: %w", err)
	}
	sort.Slice(report.Threads, func(left, right int) bool {
		return report.Threads[left].ID < report.Threads[right].ID
	})
	sort.Strings(report.LeftAlone)
	return report, nil
}

// classify judges one cursor against the rollout it points into. It is the
// single definition of "wedged" (K3): the pre-resume guard and the reporting
// command must agree, or one of them heals a thread the other calls healthy.
func classify(
	rolloutPath string,
	offset, ordinal int64,
) (Verdict, string, int64) {
	if rolloutPath == "" {
		return VerdictNoRollout, "the state store records no rollout", 0
	}
	info, err := os.Stat(rolloutPath)
	if err != nil {
		return VerdictNoRollout, rolloutPath, 0
	}
	size := info.Size()
	if offset >= size {
		return VerdictCaughtUp, fmt.Sprintf("%d/%d", offset, size), size
	}
	file, err := os.Open(rolloutPath)
	if err != nil {
		return VerdictNoRollout, err.Error(), size
	}
	defer func() {
		if err := file.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "heal: close rollout %s: %v\n", rolloutPath, err)
		}
	}()

	if offset > 0 {
		previous := make([]byte, 1)
		if _, err := file.ReadAt(previous, offset-1); err != nil {
			return applyOrdinalScan(rolloutPath, VerdictMidline,
				fmt.Sprintf("offset %d is unreadable in %d bytes", offset, size),
				size)
		}
		if previous[0] != '\n' {
			return applyOrdinalScan(rolloutPath, VerdictMidline,
				fmt.Sprintf("offset %d sits inside a record of %d bytes", offset, size),
				size)
		}
	}
	line, err := readRecordAt(file, offset)
	if err != nil {
		return applyOrdinalScan(rolloutPath, VerdictMidline,
			fmt.Sprintf("no readable record at offset %d", offset),
			size)
	}
	var record struct {
		Ordinal *int64 `json:"ordinal"`
	}
	if err := json.Unmarshal(line, &record); err != nil {
		return applyOrdinalScan(rolloutPath, VerdictMidline,
			fmt.Sprintf("unparseable record at offset %d", offset),
			size)
	}
	fileOrdinal := int64(-1)
	if record.Ordinal != nil {
		fileOrdinal = *record.Ordinal
	}
	if fileOrdinal == ordinal {
		return VerdictConsistent,
			fmt.Sprintf("%d/%d ordinal %d", offset, size, ordinal),
			size
	}
	return applyOrdinalScan(rolloutPath, VerdictWedged,
		fmt.Sprintf(
			"expects ordinal %d, the file has %d; %.1f MB unprojected",
			ordinal,
			fileOrdinal,
			float64(size-offset)/1e6,
		),
		size)
}

// unscannedSuffix is the fixed clause classify appends to a WEDGED/MIDLINE
// Detail when scanOrdinals could not read the rollout end to end. Thread
// strips it back off to report just the read error.
const unscannedSuffix = "the rollout could not be scanned: "

// applyOrdinalScan runs the full-file ordinal scan behind a WEDGED/MIDLINE
// verdict and downgrades it: NONCANONICAL when the rollout's ordinal
// sequence is not canonical, UNSCANNED when the scan itself could not finish.
// Neither downgrade is ever deleted — see Verdict.LeftAlone.
func applyOrdinalScan(
	rolloutPath string,
	verdict Verdict,
	detail string,
	size int64,
) (Verdict, string, int64) {
	found, ok, err := scanOrdinals(rolloutPath)
	if err != nil {
		return VerdictUnscanned,
			fmt.Sprintf("%s; %s%s", detail, unscannedSuffix, err),
			size
	}
	if ok {
		return VerdictNoncanonical,
			fmt.Sprintf("%s; %s", detail, found.clause()),
			size
	}
	return verdict, detail, size
}

// anomaly is the first departure scanOrdinals finds from a canonical
// 0,1,2,… ordinal sequence anywhere in a rollout.
type anomaly struct {
	Line     int    // 1-based physical line
	Offset   int64  // byte offset of the record's first byte
	Expected int64  // the ordinal the running counter expected at this record
	Ordinal  int64  // -1 when the record carries none
	Kind     string // "repeats", "skips to", "carries no ordinal", "is unparseable"
	Type     string // "event_msg/thread_settings_applied" — record type + payload type
}

// clause renders one anomaly as the trailing Detail clause a NONCANONICAL
// verdict appends. "carries no ordinal" has no ordinal to name and
// "is unparseable" has neither an ordinal nor a record type to name, so each
// kind gets its own shape rather than a single format string that would
// print a meaningless "ordinal -1" or an empty "()".
func (found anomaly) clause() string {
	switch found.Kind {
	case "is unparseable":
		return fmt.Sprintf("line %d is unparseable at offset %d", found.Line, found.Offset)
	case "carries no ordinal":
		return fmt.Sprintf(
			"line %d carries no ordinal at offset %d (%s)",
			found.Line, found.Offset, found.Type,
		)
	default:
		return fmt.Sprintf(
			"line %d %s ordinal %d at offset %d (%s)",
			found.Line, found.Kind, found.Ordinal, found.Offset, found.Type,
		)
	}
}

// openRollout opens a rollout for a full-file ordinal scan. It is a package
// var so a test can inject a reader that fails mid-stream without touching
// the filesystem.
var openRollout = func(path string) (io.ReadCloser, error) {
	return os.Open(path)
}

// scanOrdinals streams a rollout end to end looking for the first record
// whose ordinal breaks the canonical 0,1,2,… sequence: a repeat, a
// regression, a gap, a record with no ordinal, or one that will not parse.
// Records exceed 64 KB, so this reads with bufio.Reader.ReadBytes rather than
// bufio.Scanner, whose default token buffer would truncate them. Blank lines
// are not records and not anomalies, matching the projector's own skip; a
// trailing partial line with no newline is not an anomaly either — Codex
// leaves it for the next pass.
func scanOrdinals(path string) (found anomaly, hasAnomaly bool, returnErr error) {
	source, err := openRollout(path)
	if err != nil {
		return anomaly{}, false, fmt.Errorf("open %q to scan ordinals: %w", path, err)
	}
	defer func() {
		if err := source.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close rollout %q: %w", path, err))
		}
	}()

	reader := bufio.NewReader(source)
	var offset int64
	line := 0
	expected := int64(0)
	for {
		raw, readErr := reader.ReadBytes('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return anomaly{}, false, fmt.Errorf(
				"scan %q at byte %d: %w", path, offset, readErr,
			)
		}
		if len(raw) == 0 {
			break
		}
		if raw[len(raw)-1] != '\n' {
			// A trailing partial line: not a record, and not an anomaly.
			break
		}
		lineOffset := offset
		offset += int64(len(raw))
		line++
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 {
			continue
		}
		var record struct {
			Ordinal *int64 `json:"ordinal"`
			Type    string `json:"type"`
			Payload struct {
				Type string `json:"type"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(trimmed, &record); err != nil {
			return anomaly{
				Line:     line,
				Offset:   lineOffset,
				Expected: expected,
				Ordinal:  -1,
				Kind:     "is unparseable",
			}, true, nil
		}
		recordType := record.Type
		if record.Payload.Type != "" {
			recordType += "/" + record.Payload.Type
		}
		if record.Ordinal == nil {
			return anomaly{
				Line:     line,
				Offset:   lineOffset,
				Expected: expected,
				Ordinal:  -1,
				Kind:     "carries no ordinal",
				Type:     recordType,
			}, true, nil
		}
		fileOrdinal := *record.Ordinal
		if fileOrdinal != expected {
			kind := "skips to"
			if fileOrdinal < expected {
				kind = "repeats"
			}
			return anomaly{
				Line:     line,
				Offset:   lineOffset,
				Expected: expected,
				Ordinal:  fileOrdinal,
				Kind:     kind,
				Type:     recordType,
			}, true, nil
		}
		expected++
	}
	return anomaly{}, false, nil
}

// readRecordAt returns the one JSONL record starting at offset.
func readRecordAt(file *os.File, offset int64) ([]byte, error) {
	const chunk = 64 * 1024
	buffer := make([]byte, 0, chunk)
	scratch := make([]byte, chunk)
	for {
		read, err := file.ReadAt(scratch, offset+int64(len(buffer)))
		if read > 0 {
			if index := indexByte(scratch[:read], '\n'); index >= 0 {
				return append(buffer, scratch[:index]...), nil
			}
			buffer = append(buffer, scratch[:read]...)
		}
		if err != nil {
			if len(buffer) > 0 {
				return buffer, nil
			}
			return nil, err
		}
	}
}

func indexByte(data []byte, target byte) int {
	for index, value := range data {
		if value == target {
			return index
		}
	}
	return -1
}

// rolloutPaths reads the thread → rollout mapping out of the state store.
func rolloutPaths(ctx context.Context, statePath string) (paths map[string]string, returnErr error) {
	state, err := openReadOnly(statePath)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := state.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close state store %q: %w", statePath, err))
		}
	}()
	const query = "SELECT id, rollout_path FROM threads"
	op := obs.SQL(ctx, healKind, query)
	rows, err := state.QueryContext(ctx, query)
	op.End(-1, err)
	if err != nil {
		return nil, fmt.Errorf("read threads from %q: %w", statePath, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close thread rows: %w", err))
		}
	}()
	paths = make(map[string]string)
	for rows.Next() {
		var id string
		var path sql.NullString
		if err := rows.Scan(&id, &path); err != nil {
			return nil, fmt.Errorf("scan a thread row: %w", err)
		}
		paths[id] = path.String
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate thread rows: %w", err)
	}
	return paths, nil
}

func openReadOnly(path string) (*sql.DB, error) {
	database, err := sqlitedb.OpenReadOnly(path, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("open %q read-only: %w", path, err)
	}
	return database, nil
}

// Live reports whether Codex holds this thread's writer lock right now.
//
// A held lock means a running seat owns the thread and its in-memory cursor
// would race a heal, so the thread is left alone. A lock file that exists but
// is NOT held is the ordinary leftover of a closed seat.
func Live(codexHome, threadID string) bool {
	path := filepath.Join(codexHome, "thread-writer-locks", threadID+".lock")
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	defer func() {
		if err := file.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "heal: close writer lock %s: %v\n", path, err)
		}
	}()
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return true
	}
	// The probe took the lock; give it straight back, because holding it is
	// itself the thing that would block a seat from opening.
	_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
	return false
}

// Backup copies the history store, and its -wal and -shm siblings, into a
// stamped directory beside it. It runs BEFORE any delete, every time: the
// projection rows are the only copy of work Codex would otherwise have to
// rebuild, and a rebuild is cheap only when the rollout is intact.
func Backup(stores Stores, now time.Time) (string, error) {
	destination := filepath.Join(
		stores.Root,
		"heal-backup-"+now.UTC().Format("20060102-150405"),
	)
	if err := os.MkdirAll(destination, 0o700); err != nil {
		return "", fmt.Errorf("create the heal backup directory: %w", err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		source := stores.History + suffix
		content, err := os.ReadFile(source)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("read %s for backup: %w", source, err)
		}
		target := filepath.Join(destination, filepath.Base(source))
		if err := atomicfile.Write(target, content, 0o600); err != nil {
			return "", fmt.Errorf("write %s: %w", target, err)
		}
	}
	return destination, nil
}

// Delete removes one thread's projection so Codex rebuilds it from the
// rollout at the next resume. The three tables go in ONE immediate
// transaction: a projection state without its items is a thread that resumes
// empty, which is the very failure this repairs.
func Delete(ctx context.Context, stores Stores, threadID string) (returnErr error) {
	// The state door: present → deleted for the thread this call removes,
	// ERROR-shaped when the delete never committed.
	defer func() {
		obs.Transition(ctx, "heal", "present", "deleted", "operator delete")(returnErr)
	}()

	database, err := sqlitedb.OpenReadWrite(stores.History, 5*time.Second)
	if err != nil {
		return fmt.Errorf("open %q: %w", stores.History, err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close history store %q: %w", stores.History, err))
		}
	}()

	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin the heal transaction: %w", err)
	}
	defer func() { _ = transaction.Rollback() }()
	for _, table := range []string{
		"thread_history_projection_state",
		"thread_items",
		"thread_turns",
	} {
		query := "DELETE FROM " + table + " WHERE thread_id = ?"
		op := obs.SQL(ctx, healKind, query)
		result, err := transaction.ExecContext(ctx, query, threadID)
		op.End(deletedRows(result, err), err)
		if err != nil {
			return fmt.Errorf("clear %s for %s: %w", table, threadID, err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit the heal for %s: %w", threadID, err)
	}
	return nil
}

// deletedRows reads a delete result's row count for the db door, or -1
// (unknown) when there is none or the driver cannot say.
func deletedRows(result sql.Result, err error) int64 {
	if err != nil || result == nil {
		return -1
	}
	affected, countErr := result.RowsAffected()
	if countErr != nil {
		return -1
	}
	return affected
}
