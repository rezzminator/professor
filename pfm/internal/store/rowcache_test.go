package store

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// rowCacheReads lists the rows each select on table returned, in order: a
// refresh after a write reads the written rows, then the table's count(*), one
// row, and the keys only when that count shows a delete.
func rowCacheReads(recorder *obs.Recorder, table string) []string {
	var reads []string
	for _, record := range recorder.Records() {
		if record.Message != "db.statement" || record.Fields["op"] != "select" || record.Fields["table"] != table {
			continue
		}
		reads = append(reads, fmt.Sprint(record.Fields["rows"]))
	}
	return reads
}

func transcriptStates(t *testing.T, ctx context.Context, store *Store) []string {
	t.Helper()
	transcripts, err := store.Transcripts(ctx)
	if err != nil {
		t.Fatalf("Transcripts() error = %v", err)
	}
	states := make([]string, 0, len(transcripts))
	for index := range transcripts {
		transcript := &transcripts[index]
		states = append(
			states,
			fmt.Sprintf("%s superseded=%t size=%d", transcript.UUID, transcript.Superseded, transcript.Size),
		)
	}
	return states
}

// TestRowCacheRereadsOnlyWrittenRows: a held Store reads a table whole once,
// then only the rows written since, nothing at all while nothing was written,
// and the keys only after a delete; what it returns is the table as it is. A
// write count that went back (a file restored in place) reads it whole again,
// and a Store opened without the cache reads whole every time.
func TestRowCacheRereadsOnlyWrittenRows(t *testing.T) {
	store := openTestStore(t, withRowCache())
	ctx, recorder := obs.Test(t)
	upsert := func(uuid, continuedIn string, size int64) {
		t.Helper()
		if err := store.UpsertTranscript(ctx, Transcript{
			UUID: uuid, Path: "/fixture/" + uuid + ".jsonl", ContinuedIn: continuedIn, Size: size, PromptCount: 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	upsert("a", "b", 10)
	upsert("c", "", 30)

	steps := []struct {
		name  string
		write func()
		want  []string
		reads []string
	}{
		{
			"first read is whole",
			func() {},
			[]string{"a superseded=false size=10", "c superseded=false size=30"},
			[]string{"2"},
		},
		{
			"no write reads nothing",
			func() {},
			[]string{"a superseded=false size=10", "c superseded=false size=30"},
			[]string{"2"},
		},
		{
			"a new successor supersedes its predecessor", func() { upsert("b", "", 20) },
			[]string{"a superseded=true size=10", "b superseded=false size=20", "c superseded=false size=30"},
			[]string{"2", "1", "1"},
		},
		{
			"an update reads its row alone", func() { upsert("c", "", 31) },
			[]string{"a superseded=true size=10", "b superseded=false size=20", "c superseded=false size=31"},
			[]string{"2", "1", "1", "1", "1"},
		},
		{"a delete reads the keys", func() {
			if err := store.DeleteTranscript(ctx, "b"); err != nil {
				t.Fatal(err)
			}
		}, []string{"a superseded=false size=10", "c superseded=false size=31"}, []string{"2", "1", "1", "1", "1", "0", "1", "2"}},
		{"a delete and an insert of equal count read the keys", func() {
			if err := store.DeleteTranscript(ctx, "a"); err != nil {
				t.Fatal(err)
			}
			upsert("d", "", 40)
		}, []string{"c superseded=false size=31", "d superseded=false size=40"}, []string{
			"2", "1", "1", "1", "1", "0", "1", "2", "1", "1", "2",
		}},
		{"a write count gone back reads whole", func() {
			if _, err := store.db.ExecContext(ctx, "UPDATE row_seq SET value=0 WHERE id=1"); err != nil {
				t.Fatal(err)
			}
			upsert("c", "", 32)
		}, []string{"c superseded=false size=32", "d superseded=false size=40"}, []string{
			"2", "1", "1", "1", "1", "0", "1", "2", "1", "1", "2", "2",
		}},
	}
	for _, step := range steps {
		step.write()
		if got := transcriptStates(t, ctx, store); !reflect.DeepEqual(got, step.want) {
			t.Fatalf("%s: transcripts = %q, want %q", step.name, got, step.want)
		}
		if got := rowCacheReads(recorder, "transcripts"); !reflect.DeepEqual(got, step.reads) {
			t.Fatalf("%s: transcripts selects returned rows %q, want %q", step.name, got, step.reads)
		}
	}
}

// TestStoreWithoutRowCacheReadsWhole: a Store that is not held keeps no rows
// between reads, so each read is the whole table.
func TestStoreWithoutRowCacheReadsWhole(t *testing.T) {
	store := openTestStore(t)
	ctx, recorder := obs.Test(t)
	if err := store.UpsertTranscript(ctx, Transcript{UUID: "a", Path: "/fixture/a.jsonl", PromptCount: 1}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if got := transcriptStates(t, ctx, store); !reflect.DeepEqual(got, []string{"a superseded=false size=0"}) {
			t.Fatalf("transcripts = %q, want the one written row", got)
		}
	}
	// A whole read (queryRows) records no row count; the cached path's first
	// read would record "1" and its second nothing.
	if got := rowCacheReads(recorder, "transcripts"); !reflect.DeepEqual(got, []string{"<nil>", "<nil>"}) {
		t.Fatalf("transcripts selects returned rows %q, want two whole reads", got)
	}
}
