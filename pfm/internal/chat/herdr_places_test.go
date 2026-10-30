package chat

import (
	"bytes"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// The startup hook outside a Herdr server has nothing to place: it exits 0
// and says nothing.
func TestHerdrPlacesEntryOutsideHerdrIsSilent(t *testing.T) {
	var stderr bytes.Buffer
	if code := HerdrPlacesEntry(&stderr, &paths.MapEnv{}, config.Runtime{}); code != 0 || stderr.Len() != 0 {
		t.Fatalf("outside Herdr: code %d, stderr %q", code, stderr.String())
	}
}
