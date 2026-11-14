package chat

import (
	"context"
	"io"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestResolvedSelfIsRequestLocalAndExact(t *testing.T) {
	testjail.Fleet(t)
	want := headless.Chat{
		Name: "second", ID: "split-second", Engine: "cc", Path: "/jail/second.jsonl",
		CWD: "/work/second", Socket: "cc-shared", Session: "renamed", Pane: "%8", Live: true,
	}
	ctx := WithResolvedSelf(context.Background(), want)
	for _, name := range []string{"self", "me", " self ", `"me"`} {
		got, found, err := Resolve(ctx, name, io.Discard, nil)
		if err != nil || !found || got != want {
			t.Fatalf("Resolve(%q) = %+v found=%t err=%v, want exact scoped chat %+v", name, got, found, err, want)
		}
	}
	if _, found, err := Resolve(ctx, "somebody-else", io.Discard, nil); err != nil || found {
		t.Fatalf("explicit target used scoped self: found=%t err=%v", found, err)
	}
}

// memoryFleet is testjail.Fleet's scratch fleet with the verbs' store handed
// in (fleetIndex), opened by the fleet's paths: the SQLite door serves them
// from memory, so a code path that opens the store again by path reads the
// same database. The shared state starts empty, so a reader by path (the
// native seats, the launch rows) reads an empty fleet, as in a new jail. It
// returns the root.
func memoryFleet(t *testing.T) string {
	t.Helper()
	root := testjail.Fleet(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	fleetIndex = database
	t.Cleanup(func() {
		fleetIndex = nil
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return root
}
