package harvestpy

import (
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

// TestConverterFailureReachesTheCallerByName: the sidecar's own named failure
// (an ok:false answer, as converterFailure wraps it) is published as a
// conversion failure carrying the converter's words — never "could not
// classify", never the exception class or the stderr tail.
func TestConverterFailureReachesTheCallerByName(t *testing.T) {
	named := "the feed parsed to no title and no items: a broken or empty feed " +
		"(Couldn't find end of Start Tag chan line 1); a feedparser fallback on a broken feed is unmeasured"
	err := converterFailure("ValueError", "ValueError: "+named, "conversion failed for kind='feed': ValueError: "+named)
	source := "/tmp/demo/broken-feed.xml"
	published := harvest.PublicFailure(source, harvest.Result{Source: source, Kind: "feed", Error: err.Error()})
	if published.ErrorKind != "conversion" {
		t.Errorf("kind = %q, want conversion", published.ErrorKind)
	}
	if !strings.Contains(published.Error, named) {
		t.Errorf("text = %q, want the converter's named failure %q", published.Error, named)
	}
	for _, refuse := range []string{"could not classify", "ValueError", "stderr"} {
		if strings.Contains(published.Error, refuse) {
			t.Errorf("text = %q carries %q", published.Error, refuse)
		}
	}
}
