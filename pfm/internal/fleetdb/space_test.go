package fleetdb

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestSpaceRecordsOverwritesAndClears(t *testing.T) {
	t.Parallel()
	state := openTestStore(t)
	ctx := context.Background()
	for _, step := range []struct {
		name  string
		id    string
		space string
		want  map[string]string
	}{
		{name: "none recorded", id: "", want: map[string]string{}},
		{name: "first chat", id: "chat-a", space: "glass", want: map[string]string{"chat-a": "glass"}},
		{
			name: "second chat", id: "chat-b", space: "glass",
			want: map[string]string{"chat-a": "glass", "chat-b": "glass"},
		},
		{
			name: "moved again", id: "chat-a", space: "epic two",
			want: map[string]string{"chat-a": "epic two", "chat-b": "glass"},
		},
		{name: "cleared", id: "chat-a", want: map[string]string{"chat-b": "glass"}},
		{name: "clearing twice", id: "chat-a", want: map[string]string{"chat-b": "glass"}},
	} {
		if step.id != "" || step.space != "" {
			if err := state.SetSpace(ctx, step.id, step.space, 5); err != nil {
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
	if got, err := absent.Spaces(ctx); err != nil || !reflect.DeepEqual(got, map[string]string{}) {
		t.Fatalf("absent file: spaces = %v, %v; want none", got, err)
	}
	if err := absent.SetSpace(ctx, "chat-a", "glass", 1); err == nil {
		t.Fatal("a read-only store accepted a space")
	}
}

func TestSpaceTableReachesAnOlderFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pfm.db")
	seedSharedState(t, path, v3Schema)
	state := OpenSharedState(ctx, paths.Values{StateDB: path})
	defer func() { _ = state.Close() }()
	if err := state.SetSpace(ctx, "chat-a", "glass", 1); err != nil {
		t.Fatal(err)
	}
	if got, err := state.Spaces(ctx); err != nil || !reflect.DeepEqual(got, map[string]string{"chat-a": "glass"}) {
		t.Fatalf("spaces after the upgrade = %v, %v", got, err)
	}
}
