package fleetdb

import (
	"context"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/naming"
)

func TestClearNameDurableAndErrorsVisible(t *testing.T) {
	db := openTestStore(t)
	ctx := context.Background()
	const id = "22222222-2222-4222-8222-222222222222"
	fresh, err := naming.ClearSuccessorName("Desk", id)
	if err != nil {
		t.Fatal(err)
	}
	name := naming.ClearName{Inherited: "Desk", Name: fresh}
	if err := db.PutClearName(ctx, id, name, 1); err != nil {
		t.Fatal(err)
	}
	got, found, err := db.ClearName(ctx, id)
	if err != nil || !found || got != name {
		t.Fatalf("ClearName=%+v,%v,%v", got, found, err)
	}
	name.Emitted = true
	if err := db.PutClearName(ctx, id, name, 2); err != nil {
		t.Fatal(err)
	}
	if err := db.SetMeta(ctx, clearNamePrefix+id, "broken-json", 3); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.ClearName(ctx, id); err == nil {
		t.Fatal("corrupt record rendered as absence")
	}
	degraded := &Store{degraded: context.Canceled}
	if err := degraded.PutClearName(ctx, id, name, 4); err == nil {
		t.Fatal("failed write rendered as success")
	}
	if _, _, err := degraded.ClearName(ctx, id); err == nil {
		t.Fatal("failed read rendered as absence")
	}
}

func TestClearNameObservationsMergeStaleWriters(t *testing.T) {
	for _, confirmFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "prompt first", true: "observer first"}[confirmFirst], func(t *testing.T) {
			db := openTestStore(t)
			ctx := context.Background()
			const id = "22222222-2222-4222-8222-222222222222"
			initial := naming.ClearName{Inherited: "Desk", Name: "Desk #2"}
			if err := db.PutClearName(ctx, id, initial, 1); err != nil {
				t.Fatal(err)
			}
			// Both actors read before either writes: the prompt marks output,
			// while an index/reporter observation confirms the engine title.
			prompt := initial
			prompt.Emitted = true
			observer := initial.ObserveTitle(initial.Name)
			updates := []naming.ClearName{prompt, observer}
			if confirmFirst {
				updates[0], updates[1] = updates[1], updates[0]
			}
			for _, update := range updates {
				if err := db.PutClearName(ctx, id, update, 2); err != nil {
					t.Fatal(err)
				}
			}
			got, found, err := db.ClearName(ctx, id)
			if err != nil || !found || !got.Emitted || !got.Confirmed || got.Title("Desk") != "Desk" {
				t.Fatalf("stale writer lost a naming observation: %+v found=%v err=%v", got, found, err)
			}
		})
	}
}
