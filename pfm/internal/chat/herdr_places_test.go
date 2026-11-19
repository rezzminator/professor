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

// The scan Herdr placement files chats by carries every chat's recorded
// space, the chats the default view lists no row for included.
func TestHerdrPlacesCarriesEveryRecordedSpace(t *testing.T) {
	testjail.Fleet(t)
	ctx := context.Background()
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	state := fleetdb.OpenSharedState(ctx, values)
	if err := state.SetSpace(ctx, "chat-a", "glass", 1); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	fleet, err := HerdrPlaces(ctx, &config.Runtime{Paths: values})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"chat-a": "glass"}; !maps.Equal(fleet.Spaces, want) {
		t.Fatalf("spaces = %v, want %v", fleet.Spaces, want)
	}
}
