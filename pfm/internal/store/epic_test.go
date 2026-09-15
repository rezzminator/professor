package store

import (
	"context"
	"testing"
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
