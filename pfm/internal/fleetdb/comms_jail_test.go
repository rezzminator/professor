//go:build jail

package fleetdb

import (
	"context"
	"testing"
)

// TestCommsVersionMovesWhenAnotherConnectionCommits pins the ledger change
// signal the cosmos sampler gates its 24h read on: SQLite's data_version moves
// when ANOTHER connection commits — in WAL mode the commit lands in pfm.db-wal
// and the main file does not change until a checkpoint — and it keeps moving
// across a checkpoint that truncates the WAL (a restarted WAL can repeat the
// size of the one before it). Reading it must not pin the WAL either: the
// checkpoint in between completes.
func TestCommsVersionMovesWhenAnotherConnectionCommits(t *testing.T) {
	t.Parallel()
	reader, values := openFileTestStore(t)
	writer := OpenSharedState(context.Background(), values)
	if err := writer.Degraded(); err != nil {
		t.Fatalf("open the writing connection: %v", err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	ctx := context.Background()

	var mode string
	if err := writer.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q (%v), want wal: the gate is only a contract for WAL commits", mode, err)
	}
	version := func() CommsStamp {
		t.Helper()
		got, err := reader.CommsVersion(ctx)
		if err != nil {
			t.Fatalf("read the ledger version: %v", err)
		}
		return got
	}
	record := func(at int64) {
		t.Helper()
		if err := writer.RecordComms(
			ctx,
			CommsEvent{AtNS: at, Kind: KindCall, Target: "t", Message: "m"},
		); err != nil {
			t.Fatalf("record comms from the other connection: %v", err)
		}
	}

	first := version()
	if again := version(); again != first {
		t.Fatalf("version moved from %+v to %+v with no commit", first, again)
	}
	record(10)
	second := version()
	if second == first {
		t.Fatalf("version stayed %+v after another connection committed: a gate on it misses the event", first)
	}
	if again := version(); again != second {
		t.Fatalf("version moved from %+v to %+v with no commit", second, again)
	}

	var busy, logFrames, checkpointed int
	if err := writer.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").
		Scan(&busy, &logFrames, &checkpointed); err != nil {
		t.Fatalf("truncate the WAL: %v", err)
	}
	if busy != 0 {
		t.Fatalf("checkpoint(TRUNCATE) busy=%d: reading the version pins the WAL", busy)
	}
	record(20)
	if third := version(); third == second {
		t.Fatalf("version stayed %+v after a commit that followed a WAL truncate", second)
	}
	events, err := reader.CommsSince(ctx, 0, 10)
	if err != nil || len(events) != 2 {
		t.Fatalf("CommsSince = %d events, %v; want both events", len(events), err)
	}
}
