package harvestmcp

import (
	"net/url"
	"strings"
	"testing"
)

// TestAssertPublicURLIsTheHarvestChokepoint pins assertPublicURL (spec
// doh-seams-spec.md, change B.3) against the same suffix list
// harvest.AssertFetchable enforces. A .ts.net host is refused as
// private/internal by harvest's check today but has no equivalent in this
// package's private copy of the check, so on the unfixed code it falls
// through to a plain DNS lookup whose failure — when the lookup itself
// errors, as it does for a nonexistent hostname — reads as a resolution
// failure, not the private/internal refusal this test requires.
func TestAssertPublicURLIsTheHarvestChokepoint(t *testing.T) {
	for _, raw := range []string{"https://10.0.0.1/", "https://localhost/", "https://x.ts.net/"} {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatalf("url.Parse(%q): %v", raw, err)
		}
		refusal := assertPublicURL(parsed)
		if refusal == nil {
			t.Fatalf("assertPublicURL(%q) = nil, want a refusal", raw)
		}
		if !strings.Contains(refusal.Error(), "private") && !strings.Contains(refusal.Error(), "internal") {
			t.Fatalf(
				"assertPublicURL(%q) = %v, want the private/internal refusal, not a different reason",
				raw,
				refusal,
			)
		}
	}
}
