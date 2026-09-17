package fleetdb

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hostops/pfm/internal/paths"
)

func TestCommsRoundTripIsNewestFirstAndBounded(t *testing.T) {
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
	state, _ := openTestStore(t)
	err := state.RecordComms(context.Background(), CommsEvent{Kind: "reload", Message: "excluded"})
	if err == nil || !strings.Contains(err.Error(), "record comms event") {
		t.Fatalf("RecordComms() error = %v, want wrapped schema rejection", err)
	}
}

func TestDegradedStoreRejectsCommsWritesAndReads(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := OpenSharedState(context.Background(), paths.Values{FleetDB: filepath.Join(blocker, "fleet.db")})
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
