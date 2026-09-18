package opencodegen

import "testing"

func TestOpenCodeMarkerClaimsNewAndOldOutputs(t *testing.T) {
	for _, marker := range []string{newMarker, oldMarker} {
		if !hasMarker(marker + " from fixture") {
			t.Fatalf("marker %q was not claimable", marker)
		}
	}
}
