package harvest

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestResolveDOIAndFallbackPreserveMetadataOutage(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	withPublicDNSForProviderTest(t)
	metadataOutage := errors.New("metadata provider outage")
	failing := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, metadataOutage
	})}
	resolver := &Resolver{Client: failing}
	if candidates, err := resolver.ResolveDOI(context.Background(), providerFixtureDOI); err == nil {
		t.Errorf("ResolveDOI outage = candidates=%#v err=nil; want visible outage with no candidates", candidates)
	} else if len(candidates) != 0 {
		t.Errorf(
			"ResolveDOI outage = candidates=%#v err=%v; want no candidates with visible lookup failure",
			candidates,
			err,
		)
	}
	chromeFailing := &http.Client{Transport: failing.Transport}
	oaFailing := &http.Client{Transport: failing.Transport}
	h := mustNew(
		t,
		Options{
			CacheDir:  t.TempDir(),
			Client:    failing,
			Chrome:    chromeFailing,
			OA:        oaFailing,
			Converter: &fakeConverter{},
		},
	)
	got := h.fetchKnownID(context.Background(), providerFixtureDOI, IdentifierDOI, FetchOptions{})
	if got.Error == "" || got.ErrorKind != "connect" ||
		strings.Contains(strings.ToLower(got.Error), "likely paywalled") {
		t.Fatalf("DOI fallback outage = %#v; want visible connect outage", got)
	}
}
