package store

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestHeldStoreServesOneStoreUntilAFileIsReplaced: a Held hands every call the
// Store it opened while both files are the ones it opened; a file another
// process removed and created again is opened afresh, both databases with it,
// and the new file's rows are what the next call reads.
func TestHeldStoreServesOneStoreUntilAFileIsReplaced(t *testing.T) {
	for _, tc := range []struct {
		name      string
		replace   func(paths.Values) string
		reopened  bool
		opens     map[string]int
		wantSeats []string
	}{
		{
			name:      "no file replaced",
			opens:     map[string]int{"cache": 1, "state": 1},
			wantSeats: []string{"seat-old"},
		},
		{
			name:      "cache replaced",
			replace:   func(values paths.Values) string { return values.CacheDB },
			reopened:  true,
			opens:     map[string]int{"cache": 3, "state": 3},
			wantSeats: []string{"seat-new", "seat-old"},
		},
		{
			name:      "shared state replaced",
			replace:   func(values paths.Values) string { return values.StateDB },
			reopened:  true,
			opens:     map[string]int{"cache": 3, "state": 3},
			wantSeats: []string{"seat-new"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testjail.Fleet(t)
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			ctx, recorder := obs.Test(t)
			held := NewHeld(WithWarningWriter(io.Discard))
			t.Cleanup(func() {
				if err := held.Close(); err != nil {
					t.Errorf("close held store: %v", err)
				}
			})
			first, err := held.Store(ctx)
			if err != nil {
				t.Fatal(err)
			}
			putSeat(ctx, t, first.Shared(), "seat-old")
			if again, err := held.Store(ctx); err != nil || again != first {
				t.Fatalf("second call = %p, %v; want the held store %p", again, err, first)
			}
			if tc.replace != nil {
				// The SQLite door serves this test from memory, so nothing holds
				// the file the way a real connection's handle does; hold it here,
				// or the replacement could be handed the freed inode.
				holder, err := os.Open(tc.replace(values))
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := holder.Close(); err != nil {
						t.Errorf("close the replaced file's holder: %v", err)
					}
				})
				if err := os.Remove(tc.replace(values)); err != nil {
					t.Fatal(err)
				}
				other, err := Open(WithWarningWriter(io.Discard))
				if err != nil {
					t.Fatal(err)
				}
				putSeat(ctx, t, other.Shared(), "seat-new")
				if err := other.Close(); err != nil {
					t.Fatal(err)
				}
			}
			latest, err := held.Store(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if reopened := latest != first; reopened != tc.reopened {
				t.Fatalf("reopened = %t, want %t", reopened, tc.reopened)
			}
			seats, err := latest.Shared().Seats(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var ids []string
			for _, seat := range seats {
				ids = append(ids, seat.SeatID)
			}
			if !reflect.DeepEqual(ids, tc.wantSeats) {
				t.Fatalf("seats read = %v, want %v", ids, tc.wantSeats)
			}
			opens := map[string]int{}
			for _, record := range recorder.Records() {
				if record.Message != "db.open" || record.Fields["kind"] != "store" {
					continue
				}
				switch record.Fields["path"] {
				case values.CacheDB:
					opens["cache"]++
				case values.StateDB:
					opens["state"]++
				default:
					t.Fatalf("open of an unexpected database: %v", record.Fields)
				}
			}
			if !reflect.DeepEqual(opens, tc.opens) {
				t.Fatalf("store opens = %v, want %v", opens, tc.opens)
			}
		})
	}
}

// TestHeldStoreRefusesAFileMigratedPastThisBinary: a newer pfm migrating a
// held file in place keeps its inode, so the Held reads the version stamp: the
// next call opens again, and the open refuses the file as it would for any
// reader older than the file — the cache with an error, the shared state as
// a degraded half (fleetdb.OpenSharedState).
func TestHeldStoreRefusesAFileMigratedPastThisBinary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		path    func(paths.Values) string
		version int
		// degraded: the refusal is the new Store's degraded shared half,
		// not an error.
		degraded bool
		want     string
	}{
		{
			name:    "cache migrated",
			path:    func(values paths.Values) string { return values.CacheDB },
			version: SchemaVersion + 1,
			want: fmt.Sprintf(
				"database schema version %d is newer than supported version %d", SchemaVersion+1, SchemaVersion,
			),
		},
		{
			name:     "shared state migrated",
			path:     func(values paths.Values) string { return values.StateDB },
			version:  fleetdb.SchemaVersion + 1,
			degraded: true,
			want: fmt.Sprintf(
				"schema version %d is newer than binary version %d", fleetdb.SchemaVersion+1, fleetdb.SchemaVersion,
			),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testjail.Fleet(t)
			values, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			held := NewHeld(WithWarningWriter(io.Discard))
			t.Cleanup(func() {
				if err := held.Close(); err != nil {
					t.Errorf("close held store: %v", err)
				}
			})
			first, err := held.Store(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if again, err := held.Store(ctx); err != nil || again != first {
				t.Fatalf("second call = %p, %v; want the held store %p", again, err, first)
			}
			newer, err := sqlitedb.OpenStore(ctx, tc.path(values))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := newer.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version=%d", tc.version)); err != nil {
				t.Fatal(err)
			}
			if err := newer.Close(); err != nil {
				t.Fatal(err)
			}
			latest, err := held.Store(ctx)
			if tc.degraded && err == nil && latest != first {
				err = latest.SharedDegraded()
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf(
					"call after the migration = %p (held %p), %v; want the refusal %q",
					latest,
					first,
					err,
					tc.want,
				)
			}
		})
	}
}

// TestHeldStoreRetriesADegradedSharedHalf: a Store whose shared state could
// not open is kept, not opened again on every call, until heldDegradedRetry
// has passed; then both databases open again and the shared half is read.
func TestHeldStoreRetriesADegradedSharedHalf(t *testing.T) {
	root := testjail.Fleet(t)
	migrated, err := Open(WithWarningWriter(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	if err := migrated.Close(); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("a file, not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvStateDB, filepath.Join(blocker, "pfm.db"))
	clk := clock.NewFake(time.Unix(1_800_000_000, 0))
	held := NewHeld(WithClock(clk), WithWarningWriter(io.Discard))
	t.Cleanup(func() {
		if err := held.Close(); err != nil {
			t.Errorf("close held store: %v", err)
		}
	})
	ctx := context.Background()
	first, err := held.Store(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.SharedDegraded() == nil {
		t.Fatal("the shared state under a regular file opened; the fixture does not degrade it")
	}
	clk.Advance(heldDegradedRetry - time.Nanosecond)
	if again, err := held.Store(ctx); err != nil || again != first {
		t.Fatalf("call before the retry = %p, %v; want the held store %p", again, err, first)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Nanosecond)
	retried, err := held.Store(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if retried == first || retried.SharedDegraded() != nil {
		t.Fatalf("call at the retry = %p (held %p), shared degraded %v; want a new store with its shared half open",
			retried, first, retried.SharedDegraded())
	}
}

func putSeat(ctx context.Context, t *testing.T, state *fleetdb.Store, id string) {
	t.Helper()
	seat := fleetdb.Seat{
		SeatID: id, SessionID: id, Engine: "cc", Backend: "herdr",
		HerdrSocket: "endpoint", HerdrPane: "w1:" + id, HerdrWorkspace: "w1", State: "live",
	}
	if err := state.PutSeat(ctx, seat); err != nil {
		t.Fatal(err)
	}
}
