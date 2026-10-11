package fleetdb

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestCommsRoundTripIsNewestFirstAndBounded(t *testing.T) {
	t.Parallel()
	state, _ := openTestStore(t)
	ctx := context.Background()
	events := []CommsEvent{
		{
			AtNS: 10, Kind: KindInject, SenderSession: "sender-session",
			SenderLabel: "alpha", SenderUUID: "sender-uuid", Target: "beta",
			ReceiverSocket: "cc-beta", ReceiverPane: "%1", Message: "first\nverbatim",
		},
		{
			// "group" is a historical kind value: the CHECK constraint and the
			// GroupName/Members columns stay for rows already on disk, even
			// though nothing writes this kind anymore.
			AtNS: 20, Kind: "group", SenderLabel: "alpha", Target: "bet*",
			GroupName: "crew", Members: `["beta"]`, Message: "second ⏎ verbatim",
		},
		{
			AtNS: 30, Kind: KindSpawn, SenderSession: "parent", Target: "child",
			ReceiverSocket: "cx-child", Message: "initial prompt",
		},
		{
			AtNS: 30, Kind: KindInject, SenderLabel: "newest tie", Target: "child",
			Message: "newest row at the same timestamp",
		},
	}
	for _, event := range events {
		if err := state.RecordComms(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	got, err := state.CommsSince(ctx, 20, 3)
	if err != nil {
		t.Fatal(err)
	}
	want := []CommsEvent{events[3], events[2], events[1]}
	for index := range want {
		want[index].ID = got[index].ID
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CommsSince() = %#v, want %#v", got, want)
	}

	empty, err := state.CommsSince(ctx, 31, 5)
	if err != nil {
		t.Fatal(err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("healthy empty CommsSince() = %#v, want non-nil empty slice", empty)
	}
}

func TestCommsRejectsUnknownKind(t *testing.T) {
	t.Parallel()
	state, _ := openTestStore(t)
	err := state.RecordComms(context.Background(), CommsEvent{Kind: "reload", Message: "excluded"})
	if err == nil || !strings.Contains(err.Error(), "record comms event") {
		t.Fatalf("RecordComms() error = %v, want wrapped schema rejection", err)
	}
}

func TestDegradedStoreRejectsCommsWritesAndReads(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	blocker := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := OpenSharedState(context.Background(), paths.Values{StateDB: filepath.Join(blocker, "pfm.db")})
	t.Cleanup(func() { _ = state.Close() })

	if err := state.RecordComms(
		context.Background(),
		CommsEvent{Kind: KindInject, Message: "x"},
	); err == nil ||
		!strings.Contains(err.Error(), "record comms event") {
		t.Fatalf("degraded RecordComms() error = %v", err)
	}
	if events, err := state.CommsSince(
		context.Background(),
		0,
		5,
	); err == nil || events != nil ||
		!strings.Contains(err.Error(), "query comms events") {
		t.Fatalf("degraded CommsSince() = %#v, %v", events, err)
	}
}

// TestCommsVersionMovesWhenAnotherConnectionCommits pins the ledger change
// signal the cosmos sampler gates its 24h read on: SQLite's data_version moves
// when ANOTHER connection commits — in WAL mode the commit lands in pfm.db-wal
// and the main file does not change until a checkpoint — and it keeps moving
// across a checkpoint that truncates the WAL (a restarted WAL can repeat the
// size of the one before it). Reading it must not pin the WAL either: the
// checkpoint in between completes.
func TestCommsVersionMovesWhenAnotherConnectionCommits(t *testing.T) {
	t.Parallel()
	reader, values := openTestStore(t)
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
			CommsEvent{AtNS: at, Kind: KindInject, Target: "t", Message: "m"},
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

// TestCommsVersionMovesForTheStoresOwnWrites pins the other half of the stamp:
// SQLite does not move data_version for the connection that committed, so a
// Store that records an event itself must still see its own ledger change.
func TestCommsVersionMovesForTheStoresOwnWrites(t *testing.T) {
	t.Parallel()
	state, _ := openTestStore(t)
	ctx := context.Background()
	before, err := state.CommsVersion(ctx)
	if err != nil {
		t.Fatalf("read the ledger version: %v", err)
	}
	if err := state.RecordComms(ctx, CommsEvent{AtNS: 5, Kind: KindInject, Target: "t", Message: "m"}); err != nil {
		t.Fatalf("record comms: %v", err)
	}
	after, err := state.CommsVersion(ctx)
	if err != nil {
		t.Fatalf("read the ledger version: %v", err)
	}
	if after == before {
		t.Fatalf("stamp stayed %+v after the store's own commit", before)
	}
}
