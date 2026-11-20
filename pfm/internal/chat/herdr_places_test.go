package chat

import (
	"bytes"
	"context"
	"maps"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// The startup hook outside a Herdr server has nothing to place: it exits 0
// and says nothing.
func TestHerdrPlacesEntryOutsideHerdrIsSilent(t *testing.T) {
	var stderr bytes.Buffer
	if code := HerdrPlacesEntry(&stderr, &paths.MapEnv{}, config.Runtime{}); code != 0 || stderr.Len() != 0 {
		t.Fatalf("outside Herdr: code %d, stderr %q", code, stderr.String())
	}
}

// The scan Herdr placement files chats by carries the recorded space of every
// chat the index holds, the chats the default view lists no row for included;
// a record of a chat that no longer exists reserves nothing.
func TestHerdrPlacesCarriesEveryRecordedSpace(t *testing.T) {
	root := testjail.Fleet(t)
	ctx := context.Background()
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	const id = "a1111111-1111-4111-8111-111111111111"
	seedClaudeChat(t, root, id)
	state := fleetdb.OpenSharedState(ctx, values)
	for chat, space := range map[string]string{id: "glass", "b2222222-2222-4222-8222-222222222222": "epic"} {
		if err := state.SetSpace(ctx, chat, space, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	fleet, err := HerdrPlaces(ctx, &config.Runtime{Paths: values})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{id: "glass"}; !maps.Equal(fleet.Spaces, want) {
		t.Fatalf("spaces = %v, want %v", fleet.Spaces, want)
	}
}
