package store

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

func TestEpicInjectionDedupeIsKeyedBySessionAndSlug(t *testing.T) {
	setStoreTestJail(t)
	database, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	ctx := context.Background()

	seen, err := database.EpicInjected(ctx, "session-a", "alpha")
	if err != nil || seen {
		t.Fatalf("initial EpicInjected = %v, %v", seen, err)
	}
	if err := database.RecordEpicInjection(ctx, "session-a", "alpha"); err != nil {
		t.Fatal(err)
	}
	seen, err = database.EpicInjected(ctx, "session-a", "alpha")
	if err != nil || !seen {
		t.Fatalf("recorded EpicInjected = %v, %v", seen, err)
	}
	if seen, err = database.EpicInjected(ctx, "session-a", "beta"); err != nil || seen {
		t.Fatalf("renamed slug EpicInjected = %v, %v", seen, err)
	}
	if seen, err = database.EpicInjected(ctx, "session-b", "alpha"); err != nil || seen {
		t.Fatalf("other session EpicInjected = %v, %v", seen, err)
	}
	if err := database.RecordEpicInjection(ctx, "session-a", "alpha"); err != nil {
		t.Fatal(err)
	}
}

func TestRecordEpicInjectionUsesInjectedClock(t *testing.T) {
	setStoreTestJail(t)
	when := time.Date(2026, 9, 18, 10, 55, 0, 123456789, time.UTC)
	database, err := Open(WithClock(clock.NewFake(when)))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()

	if err := database.RecordEpicInjection(context.Background(), "session-clock", "fixed"); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := database.db.QueryRow(
		"SELECT injected_at FROM epic_injections WHERE session_id=? AND slug=?",
		"session-clock",
		"fixed",
	).Scan(&got); err != nil {
		t.Fatalf("read injected_at: %v", err)
	}
	if want := when.Format(time.RFC3339Nano); got != want {
		t.Fatalf("injected_at = %q, want fake clock value %q", got, want)
	}
	var absent sql.NullString
	if err := database.db.QueryRow(
		"SELECT injected_at FROM epic_injections WHERE session_id=? AND slug=?",
		"missing",
		"fixed",
	).Scan(&absent); err != sql.ErrNoRows {
		t.Fatalf("missing injection query error = %v, want sql.ErrNoRows", err)
	}
}
