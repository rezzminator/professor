package store

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestClearNameOverlaySurvivesIndexAndYieldsToRename(t *testing.T) {
	testjail.Fleet(t)
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	const id = "22222222-2222-4222-8222-222222222222"
	fresh, err := naming.ClearSuccessorName("Desk", id)
	if err != nil {
		t.Fatal(err)
	}
	name := naming.ClearName{Inherited: "Desk", Name: fresh}
	if err := db.state.PutClearName(ctx, id, name, 1); err != nil {
		t.Fatal(err)
	}
	for _, current := range []string{"Desk", fresh, "Desk", "Manual"} {
		if err := db.UpsertTranscript(
			ctx,
			Transcript{UUID: id, Path: "/fixture/successor.jsonl", CustomTitle: current, Size: 10, PromptCount: 1},
		); err != nil {
			t.Fatal(err)
		}
		got, found, err := db.Transcript(ctx, id)
		want := current
		if current == "Desk" && !name.Confirmed {
			want = fresh
		}
		if err != nil || !found || got.CustomTitle != want {
			t.Fatalf("title=%q found=%v err=%v want=%q", got.CustomTitle, found, err, want)
		}
		if current == fresh {
			name.Confirmed = true
		}
		all, err := db.Transcripts(ctx)
		if err != nil || len(all) != 1 || all[0].CustomTitle != want {
			t.Fatalf("Transcripts=%v err=%v", all, err)
		}
	}
}

func TestClearNameReservesSmallestFreeStoredNumber(t *testing.T) {
	testjail.Fleet(t)
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	for _, row := range []Transcript{
		{UUID: "original", Path: "/fixture/original", CustomTitle: "INTEGRATION"},
		{UUID: "occupied-two", Path: "/fixture/two", CustomTitle: "INTEGRATION #2"},
		{UUID: "occupied-four", Path: "/fixture/four", AITitle: "INTEGRATION #4"},
		{UUID: "prompt-name", Path: "/fixture/seven", FirstPrompt: "INTEGRATION #7"},
	} {
		if err := db.UpsertTranscript(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.UpsertCxName(ctx, CxName{ID: "codex", ThreadName: "INTEGRATION #5"}); err != nil {
		t.Fatal(err)
	}
	if err := db.state.PutSeat(
		ctx,
		fleetdb.Seat{SeatID: "other-seat", SessionID: "other", Backend: "tmux", State: "live", Name: "INTEGRATION #6"},
	); err != nil {
		t.Fatal(err)
	}
	const id = "22222222-2222-4222-8222-222222222222"
	name, err := db.ReserveClearName(ctx, id, "INTEGRATION")
	if err != nil || name.Name != "INTEGRATION #3" {
		t.Fatalf("first reservation=%+v err=%v want INTEGRATION #3", name, err)
	}
	again, err := db.ReserveClearName(ctx, id, "INTEGRATION")
	if err != nil || again != name {
		t.Fatalf("idempotent reservation=%+v err=%v want %+v", again, err, name)
	}
	next, err := db.ReserveClearName(ctx, "33333333-3333-4333-8333-333333333333", name.Name)
	if err != nil || next.Name != "INTEGRATION #8" {
		t.Fatalf("second reservation=%+v err=%v want INTEGRATION #8", next, err)
	}
	// Only the successor itself is excluded from the occupied-name lookup.
	const ownID = "44444444-4444-4444-8444-444444444444"
	if err := db.UpsertTranscript(
		ctx,
		Transcript{UUID: ownID, Path: "/fixture/self", CustomTitle: "Own #2"},
	); err != nil {
		t.Fatal(err)
	}
	own, err := db.ReserveClearName(ctx, ownID, "Own")
	if err != nil || own.Name != "Own #2" {
		t.Fatalf("successor counted its own stale title: %+v err=%v", own, err)
	}
	unnamed, err := db.ReserveClearName(ctx, "55555555-5555-4555-8555-555555555555", "")
	if err != nil || unnamed.Name != "" {
		t.Fatalf("unnamed reservation=%+v err=%v", unnamed, err)
	}
}

func TestClearNameLookupFailureReservesNothing(t *testing.T) {
	testjail.Fleet(t)
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	if _, err := db.db.ExecContext(ctx, "ALTER TABLE cx_names RENAME TO unavailable_names"); err != nil {
		t.Fatal(err)
	}
	const id = "22222222-2222-4222-8222-222222222222"
	_, err = db.ReserveClearName(ctx, id, "Desk")
	if err == nil || !strings.Contains(err.Error(), "look up occupied clear names") {
		t.Fatalf("failed lookup=%v want visible refusal", err)
	}
	if name, found, err := db.state.ClearName(ctx, id); err != nil || found {
		t.Fatalf("failed lookup guessed name=%+v found=%v err=%v", name, found, err)
	}
	if _, err := db.db.ExecContext(ctx, "ALTER TABLE unavailable_names RENAME TO cx_names"); err != nil {
		t.Fatal(err)
	}
	name, err := db.ReserveClearName(ctx, id, "Desk")
	if err != nil || name.Name != "Desk #2" {
		t.Fatalf("lookup repair did not retry first free number: %+v err=%v", name, err)
	}
}

func TestSeedClearTranscriptKeepsConcurrentIndexData(t *testing.T) {
	testjail.Fleet(t)
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()
	const id = "22222222-2222-4222-8222-222222222222"
	indexed := Transcript{UUID: id, Path: "/fixture/indexed", CustomTitle: "Manual", Size: 55, PromptCount: 3}
	if err := db.UpsertTranscript(ctx, indexed); err != nil {
		t.Fatal(err)
	}
	if err := db.SeedClearTranscript(ctx, Transcript{UUID: id, Path: "/fixture/stale"}); err != nil {
		t.Fatal(err)
	}
	got, found, err := db.Transcript(ctx, id)
	if err != nil || !found || got != indexed {
		t.Fatalf("seed overwrote indexed data: %+v found=%v err=%v", got, found, err)
	}
}

func TestClearNameDiagnosticReadsNeverWrite(t *testing.T) {
	testjail.Fleet(t)
	ctx := context.Background()
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	const id = "22222222-2222-4222-8222-222222222222"
	decision := naming.ClearName{Inherited: "Desk", Name: "Desk #2"}
	if err := db.state.PutClearName(ctx, id, decision, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTranscript(
		ctx,
		Transcript{UUID: id, Path: "/fixture/renamed", CustomTitle: "Manual"},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	readOnly, err := OpenWithoutMigrating(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readOnly.Close() }()
	got, found, err := readOnly.Transcript(ctx, id)
	if err != nil || !found || got.CustomTitle != "Manual" {
		t.Fatalf("read-only transcript=%+v found=%v err=%v", got, found, err)
	}
	all, err := readOnly.Transcripts(ctx)
	if err != nil || len(all) != 1 || all[0].CustomTitle != "Manual" {
		t.Fatalf("read-only transcripts=%+v err=%v", all, err)
	}
	stored, _, err := readOnly.state.ClearName(ctx, id)
	if err != nil || stored != decision {
		t.Fatalf("diagnostic read changed decision=%+v err=%v", stored, err)
	}
}

func TestClearNameDegradedDiagnosticKeepsCachedTranscripts(t *testing.T) {
	testjail.Fleet(t)
	ctx := context.Background()
	db, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTranscript(
		ctx,
		Transcript{UUID: "cached", Path: "/fixture/cached", CustomTitle: "Desk"},
	); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvStateDB, filepath.Join(t.TempDir(), "absent.db"))
	readOnly, err := OpenWithoutMigrating(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = readOnly.Close() }()
	if readOnly.SharedDegraded() == nil {
		t.Fatal("missing shared-state diagnosis was hidden")
	}
	got, found, err := readOnly.Transcript(ctx, "cached")
	if err != nil || !found || got.CustomTitle != "Desk" {
		t.Fatalf("degraded diagnostic transcript=%+v found=%v err=%v", got, found, err)
	}
	all, err := readOnly.Transcripts(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("degraded diagnostic transcripts=%+v err=%v", all, err)
	}
}

func TestClearNameReservationsAreUniqueAcrossCaches(t *testing.T) {
	root := testjail.Fleet(t)
	const count = 6
	databases := make([]*Store, count)
	for i := range databases {
		t.Setenv(paths.EnvCacheDB, filepath.Join(root, fmt.Sprintf("cache-%d.db", i)))
		db, err := Open()
		if err != nil {
			t.Fatal(err)
		}
		databases[i] = db
		t.Cleanup(func() { _ = db.Close() })
	}
	type result struct {
		name string
		err  error
	}
	results := make(chan result, count)
	start := make(chan struct{})
	for i, db := range databases {
		go func() {
			<-start
			id := fmt.Sprintf("%08d-2222-4222-8222-222222222222", i)
			name, err := db.ReserveClearName(context.Background(), id, "Desk")
			results <- result{name.Name, err}
		}()
	}
	close(start)
	var names []string
	for range count {
		got := <-results
		if got.err != nil {
			t.Errorf("concurrent reservation: %v", got.err)
		}
		names = append(names, got.name)
	}
	sort.Strings(names)
	for i, name := range names {
		if want := fmt.Sprintf("Desk #%d", i+2); name != want {
			t.Errorf("names=%v: index %d=%q want %q", names, i, name, want)
		}
	}
}
