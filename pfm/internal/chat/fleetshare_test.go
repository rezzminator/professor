package chat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/compose"
)

const testShareKey = "test-key"

func newTestShare(t *testing.T, path string) *ScanShare {
	t.Helper()
	return &ScanShare{path: path, lock: path + ".lock", key: testShareKey, clock: clock.Real, noted: map[string]bool{}}
}

// filledRow sets every field of a row to a non-zero value, so a field the
// snapshot encoding drops fails the round trip.
func filledRow(t *testing.T) compose.Row {
	t.Helper()
	var row compose.Row
	value := reflect.ValueOf(&row).Elem()
	for index := range value.NumField() {
		field := value.Field(index)
		switch field.Kind() {
		case reflect.String:
			field.SetString("v" + strconv.Itoa(index))
		case reflect.Bool:
			field.SetBool(true)
		case reflect.Int, reflect.Int64:
			field.SetInt(int64(index + 1))
		case reflect.Uint8:
			field.SetUint(uint64(index + 1))
		case reflect.Slice:
			slice := reflect.MakeSlice(field.Type(), 1, 1)
			element := slice.Index(0)
			switch element.Kind() {
			case reflect.Int:
				element.SetInt(int64(index + 1))
			case reflect.String:
				element.SetString("e" + strconv.Itoa(index))
			default:
				t.Fatalf("row field %s: slice of %s has no test value", value.Type().Field(index).Name, element.Kind())
			}
			field.Set(slice)
		default:
			t.Fatalf("row field %s: kind %s has no test value", value.Type().Field(index).Name, field.Kind())
		}
	}
	return row
}

func sampleScan(t *testing.T, name string) FleetScan {
	row := filledRow(t)
	row.Name = name
	return FleetScan{Rows: []compose.Row{row}, Unreadable: map[string]string{"cc-1-1-1": "server unresponsive"}}
}

// TestSnapshotRoundTripsEveryRowField pins the encoding: a scan read back from
// a snapshot equals the scan written, field for field.
func TestSnapshotRoundTripsEveryRowField(t *testing.T) {
	want := sampleScan(t, "lad-a")
	start := time.Unix(1_700_000_000, 123)
	content, err := encodeSnapshot(want, testShareKey, start)
	if err != nil {
		t.Fatal(err)
	}
	got, gotStart, err := decodeSnapshot(content, testShareKey)
	if err != nil {
		t.Fatalf("decodeSnapshot: %v", err)
	}
	if !reflect.DeepEqual(got, want) || !gotStart.Equal(start) {
		t.Fatalf("round trip = %+v at %v, want %+v at %v", got, gotStart, want, start)
	}
}

// countingScan is a scan that records how often it ran.
type countingScan struct {
	calls  atomic.Int32
	result FleetScan
	err    error
	delay  time.Duration
}

func (scan *countingScan) run(context.Context) (FleetScan, error) {
	scan.calls.Add(1)
	time.Sleep(scan.delay)
	return scan.result, scan.err
}

// TestScanShareServesOneScanPerTick: a second watch polling at the same tick
// reads the first one's scan instead of scanning.
func TestScanShareServesOneScanPerTick(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch-snapshot.json")
	tick := time.Now()
	scan := &countingScan{result: sampleScan(t, "first")}
	first, err := newTestShare(t, path).Scan(context.Background(), tick, scan.run)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newTestShare(t, path).Scan(context.Background(), tick, scan.run)
	if err != nil {
		t.Fatal(err)
	}
	if calls := scan.calls.Load(); calls != 1 {
		t.Fatalf("scans = %d, want 1 shared by both watches", calls)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("second watch read %+v, want the first's %+v", second, first)
	}
}

// TestScanShareNeverReadsAnUnfitSnapshot: every snapshot that is not a whole,
// current scan of this key is passed over, the poll scans for itself, and its
// scan replaces the file.
func TestScanShareNeverReadsAnUnfitSnapshot(t *testing.T) {
	now := time.Now()
	since := now.Add(-time.Second)
	stale := sampleScan(t, "stale")
	valid := func(key string, start time.Time) []byte {
		content, err := encodeSnapshot(stale, key, start)
		if err != nil {
			t.Fatal(err)
		}
		return content
	}
	whole := valid(testShareKey, now)
	plantedFile := func(t *testing.T, path string) {
		t.Helper()
		if err := os.WriteFile(path, whole, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name    string
		content []byte // nil: no file, unless plant puts one there
		plant   func(t *testing.T, path string)
	}{
		{name: "missing"},
		{name: "empty", content: []byte{}},
		{name: "corrupt", content: []byte("{not a snapshot")},
		{name: "partial: a writer died mid-write", content: whole[:len(whole)-40]},
		{name: "payload altered", content: append(append([]byte{}, whole[:len(whole)-3]...), "xx}"...)},
		{name: "another key", content: valid("other-key", now)},
		{name: "begun before this poll's tick", content: valid(testShareKey, since.Add(-time.Nanosecond))},
		{name: "begun in this clock's future: a clock stepped back", content: valid(testShareKey, now.Add(time.Hour))},
		// A whole, current snapshot that is not a file this user alone wrote
		// is not trusted: anyone could have forged it, and a FIFO would block
		// the poll's open forever.
		{name: "writable by others", plant: func(t *testing.T, path string) {
			plantedFile(t, path)
			if err := os.Chmod(path, 0o666); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a symlink to a snapshot elsewhere", plant: func(t *testing.T, path string) {
			elsewhere := filepath.Join(t.TempDir(), "planted.json")
			plantedFile(t, elsewhere)
			if err := os.Symlink(elsewhere, path); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a FIFO", plant: func(t *testing.T, path string) {
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "watch-snapshot.json")
			if test.content != nil {
				if err := os.WriteFile(path, test.content, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if test.plant != nil {
				test.plant(t, path)
			}
			scan := &countingScan{result: sampleScan(t, "fresh")}
			got, err := newTestShare(t, path).Scan(context.Background(), since, scan.run)
			if err != nil {
				t.Fatal(err)
			}
			if scan.calls.Load() != 1 || !reflect.DeepEqual(got, scan.result) {
				t.Fatalf("Scan = %+v after %d scans, want the poll's own scan", got, scan.calls.Load())
			}
			again := &countingScan{result: sampleScan(t, "never")}
			reread, err := newTestShare(t, path).Scan(context.Background(), since, again.run)
			if err != nil {
				t.Fatal(err)
			}
			if again.calls.Load() != 0 || !reflect.DeepEqual(reread, scan.result) {
				t.Fatalf(
					"the next watch read %+v after %d scans, want the republished scan",
					reread,
					again.calls.Load(),
				)
			}
		})
	}
}

// holdLock takes the snapshot lock as another process would, stamped as
// having begun its scan at started.
func holdLock(t *testing.T, path string, started time.Time) func() {
	t.Helper()
	lock, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if err := stampHolder(lock, started); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release := func() { once.Do(func() { _ = lock.Close() }) }
	t.Cleanup(release)
	return release
}

// TestScanShareALockedSnapshotNeverStallsAWatch: a holder that never
// publishes delays a poll by at most ShareLockWait, and one already scanning
// for longer than that (stopped mid-scan) delays it not at all.
func TestScanShareALockedSnapshotNeverStallsAWatch(t *testing.T) {
	for _, test := range []struct {
		name      string
		started   time.Duration // the holder's stamp, relative to now
		atLeast   time.Duration
		atMost    time.Duration
		unstamped bool
	}{
		{name: "a holder just begun", started: 0, atLeast: ShareLockWait - 100*time.Millisecond, atMost: 2 * ShareLockWait},
		{name: "a holder stuck mid-scan", started: -10 * ShareLockWait, atMost: ShareLockWait / 2},
		{name: "a holder with no stamp", unstamped: true, atLeast: ShareLockWait - 100*time.Millisecond, atMost: 2 * ShareLockWait},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "watch-snapshot.json")
			holdLock(t, path, time.Now().Add(test.started))
			if test.unstamped {
				if err := os.Truncate(path+".lock", 0); err != nil {
					t.Fatal(err)
				}
			}
			scan := &countingScan{result: sampleScan(t, "own")}
			began := time.Now()
			got, err := newTestShare(t, path).Scan(context.Background(), began, scan.run)
			took := time.Since(began)
			if err != nil {
				t.Fatal(err)
			}
			if scan.calls.Load() != 1 || !reflect.DeepEqual(got, scan.result) {
				t.Fatalf("Scan = %+v after %d scans, want the poll's own scan", got, scan.calls.Load())
			}
			if took < test.atLeast || took > test.atMost {
				t.Fatalf("Scan took %s, want between %s and %s", took, test.atLeast, test.atMost)
			}
		})
	}
}

// TestScanShareAWaiterReadsTheHoldersSnapshot: a watch that finds the lock
// held waits for the holder's snapshot and reads it instead of scanning.
func TestScanShareAWaiterReadsTheHoldersSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch-snapshot.json")
	since := time.Now()
	release := holdLock(t, path, since)
	holder := newTestShare(t, path)
	published := sampleScan(t, "holder")
	go func() {
		time.Sleep(200 * time.Millisecond)
		if err := holder.publish(time.Now(), published); err != nil {
			t.Error(err)
		}
		release()
	}()
	scan := &countingScan{result: sampleScan(t, "never")}
	got, err := newTestShare(t, path).Scan(context.Background(), since, scan.run)
	if err != nil {
		t.Fatal(err)
	}
	if scan.calls.Load() != 0 || !reflect.DeepEqual(got, published) {
		t.Fatalf("Scan = %+v after %d scans, want the holder's snapshot", got, scan.calls.Load())
	}
}

// TestScanShareConcurrentWatchesPayOneScan: watches polling at one tick at
// once pay one scan between them, and every one of them gets it.
func TestScanShareConcurrentWatchesPayOneScan(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch-snapshot.json")
	since := time.Now()
	scan := &countingScan{result: sampleScan(t, "shared"), delay: 150 * time.Millisecond}
	const watches = 8
	results := make([]FleetScan, watches)
	errs := make([]error, watches)
	var group sync.WaitGroup
	for index := range watches {
		group.Go(func() {
			results[index], errs[index] = newTestShare(t, path).Scan(context.Background(), since, scan.run)
		})
	}
	group.Wait()
	for index := range watches {
		if errs[index] != nil || !reflect.DeepEqual(results[index], scan.result) {
			t.Fatalf("watch %d: Scan = %+v, %v, want the shared scan", index, results[index], errs[index])
		}
	}
	if calls := scan.calls.Load(); calls != 1 {
		t.Fatalf("scans = %d, want 1 for %d concurrent watches", calls, watches)
	}
}

// TestScanShareAFailedScanIsNeverShared: a scan error reaches its own poll and
// publishes nothing, so the next watch scans for itself.
func TestScanShareAFailedScanIsNeverShared(t *testing.T) {
	path := filepath.Join(t.TempDir(), "watch-snapshot.json")
	since := time.Now()
	failing := &countingScan{err: errors.New("tmux: no server")}
	if _, err := newTestShare(t, path).Scan(context.Background(), since, failing.run); !errors.Is(err, failing.err) {
		t.Fatalf("Scan error = %v, want the scan's own", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("snapshot after a failed scan: stat err = %v, want none published", err)
	}
	next := &countingScan{result: sampleScan(t, "next")}
	if _, err := newTestShare(
		t,
		path,
	).Scan(context.Background(), since, next.run); err != nil ||
		next.calls.Load() != 1 {
		t.Fatalf("next watch: err %v after %d scans, want its own scan", err, next.calls.Load())
	}
}

// TestScanSharePublishingSweepsADeadWritersScratch: a scratch file a writer
// killed mid-write left behind is removed by the next publisher once it is
// old enough to be no live writer's.
func TestScanSharePublishingSweepsADeadWritersScratch(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "watch-snapshot.json")
	dead := filepath.Join(directory, ".watch-snapshot.json.tmp-123")
	live := filepath.Join(directory, ".watch-snapshot.json.tmp-456")
	for _, scratch := range []string{dead, live} {
		if err := os.WriteFile(scratch, []byte("{partial"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * shareScratchAge)
	if err := os.Chtimes(dead, old, old); err != nil {
		t.Fatal(err)
	}
	scan := &countingScan{result: sampleScan(t, "fresh")}
	if _, err := newTestShare(t, path).Scan(context.Background(), time.Now(), scan.run); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dead); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dead writer's scratch: stat err = %v, want it swept", err)
	}
	if _, err := os.Stat(live); err != nil {
		t.Fatalf("a fresh scratch was swept (stat err = %v): it may be a live writer's", err)
	}
}
