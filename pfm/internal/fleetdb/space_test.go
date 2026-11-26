package fleetdb

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

func TestSpaceRecordsOverwritesAndClears(t *testing.T) {
	t.Parallel()
	state := openTestStore(t)
	ctx := context.Background()
	for _, step := range []struct {
		name  string
		id    string
		space string
		at    int64
		want  map[string]ChatSpace
	}{
		{name: "none recorded", id: "", want: map[string]ChatSpace{}},
		{name: "first chat", id: "chat-a", space: "glass", at: 5, want: map[string]ChatSpace{"chat-a": {"glass", 5}}},
		{
			name: "second chat", id: "chat-b", space: "glass", at: 5,
			want: map[string]ChatSpace{"chat-a": {"glass", 5}, "chat-b": {"glass", 5}},
		},
		{
			name: "moved again", id: "chat-a", space: "epic two", at: 6,
			want: map[string]ChatSpace{"chat-a": {"epic two", 6}, "chat-b": {"glass", 5}},
		},
		{name: "cleared", id: "chat-a", at: 7, want: map[string]ChatSpace{"chat-a": {"", 7}, "chat-b": {"glass", 5}}},
		{
			name: "clearing twice", id: "chat-a", at: 8,
			want: map[string]ChatSpace{"chat-a": {"", 8}, "chat-b": {"glass", 5}},
		},
	} {
		if step.id != "" {
			if err := state.SetSpace(ctx, step.id, step.space, step.at); err != nil {
				t.Fatalf("%s: %v", step.name, err)
			}
		}
		if got, err := state.Spaces(ctx); err != nil || !reflect.DeepEqual(got, step.want) {
			t.Fatalf("%s: spaces = %v, %v; want %v", step.name, got, err, step.want)
		}
	}
}

func TestSpaceRefusesAnEmptyChatIDAndADegradedStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if err := openTestStore(t).SetSpace(ctx, "", "glass", 1); err == nil ||
		err.Error() != `record chat space "glass": empty chat id` {
		t.Fatalf("empty chat id: %v", err)
	}
	absent := OpenSharedStateReadOnly(ctx, paths.Values{StateDB: filepath.Join(t.TempDir(), "absent.db")})
	if got, err := absent.Spaces(ctx); err != nil || !reflect.DeepEqual(got, map[string]ChatSpace{}) {
		t.Fatalf("absent file: spaces = %v, %v; want none", got, err)
	}
	if err := absent.SetSpace(ctx, "chat-a", "glass", 1); err == nil {
		t.Fatal("a read-only store accepted a space")
	}
}

func TestSpaceTableReachesAnOlderFile(t *testing.T) {
	// The backup before a migration bounds its WAL checkpoint on the process
	// clock: on a fake one that bound arms no real timer.
	testwait.ProcessClock(t, clock.NewFake(time.Unix(1_800_000_000, 0)))
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pfm.db")
	seedSharedState(t, path, v3Schema)
	state := OpenSharedState(ctx, paths.Values{StateDB: path})
	defer func() { _ = state.Close() }()
	if err := state.SetSpace(ctx, "chat-a", "glass", 1); err != nil {
		t.Fatal(err)
	}
	if got, err := state.Spaces(
		ctx,
	); err != nil ||
		!reflect.DeepEqual(got, map[string]ChatSpace{"chat-a": {"glass", 1}}) {
		t.Fatalf("spaces after the upgrade = %v, %v", got, err)
	}
}

// A new session takes the space its seat's chat wears, never over a record of
// its own and never a cleared one.
func TestCarrySpaceKeepsTheSpaceOfTheSessionBefore(t *testing.T) {
	t.Parallel()
	state := openTestStore(t)
	ctx := context.Background()
	for _, record := range []struct {
		id, space string
	}{{"born", "glass"}, {"own", "epic two"}, {"cleared", ""}} {
		if err := state.SetSpace(ctx, record.id, record.space, 1); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		from, to string
		carried  bool
		want     ChatSpace
		found    bool
	}{
		{from: "born", to: "engine", carried: true, want: ChatSpace{"glass", 9}, found: true},
		{from: "born", to: "own", want: ChatSpace{"epic two", 1}, found: true},
		{from: "cleared", to: "after-clear"},
		{from: "none", to: "after-none"},
	} {
		carried, err := state.CarrySpace(ctx, test.from, test.to, 9)
		spaces, readErr := state.Spaces(ctx)
		got, found := spaces[test.to]
		if err != nil || readErr != nil || carried != test.carried || found != test.found || got != test.want {
			t.Fatalf("CarrySpace(%s, %s) = %v, %v; record %+v (found %v, %v); want %v, record %+v (found %v)",
				test.from, test.to, carried, err, got, found, readErr, test.carried, test.want, test.found)
		}
	}
	if _, err := state.CarrySpace(ctx, "", "engine", 9); err == nil ||
		err.Error() != `carry chat space from "" to "engine": empty chat id` {
		t.Fatalf("empty chat id: %v", err)
	}
}
