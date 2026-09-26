package harvest

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestCallerHeadersReachTheWorkLandingOnly: read (publications) on a bare DOI with a
// caller header resolves the DOI as without one — the resolver and the
// metadata APIs never see the header — and sends it to the landing URL's
// origin; the open-access copy on another origin never sees it, and the text
// it served names the omission in partial.
func TestCallerHeadersReachTheWorkLandingOnly(t *testing.T) {
	ctx, logs := captureLogs(t)
	seen := newHeaderSeen()
	web := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		seen.record(r)
		switch r.URL.Host {
		case "doi.org":
			moved := response(r, http.StatusFound, "text/html", "")
			moved.Header.Set("Location", "https://publisher.test/article/landing-test")
			return moved, nil
		case "publisher.test":
			return response(r, http.StatusForbidden, "text/html", "<html><body>subscribe to read</body></html>"), nil
		case "api.openalex.org":
			return jsonResponse(
				r,
				`{"open_access":{"oa_url":"https://mirror.test/copy.html","oa_status":"green"}}`,
			), nil
		case "mirror.test":
			return response(
				r,
				http.StatusOK,
				"text/html",
				"<html><body>"+strings.Repeat("<p>the work's full text</p>", 80)+"</body></html>",
			), nil
		}
		return response(r, http.StatusNotFound, "application/json", `{}`), nil
	})
	h := mustNew(t, Options{
		CacheDir:  t.TempDir(),
		Client:    &http.Client{Transport: web},
		Chrome:    &http.Client{Transport: web},
		Jina:      &http.Client{Transport: web},
		OA:        &http.Client{Transport: web},
		Converter: &fakeConverter{},
	})
	got := probeLines().Fetch(ctx, h, "10.9999/landing-test", FetchOptions{})
	if got.Error != "" {
		t.Fatalf("Fetch error = %q, want the open-access copy", got.Error)
	}
	if !seen.probed("publisher.test") {
		t.Fatalf("the landing origin never received the caller header: %v", seen.probes)
	}
	for _, host := range []string{"doi.org", "api.openalex.org", "mirror.test"} {
		if len(seen.probes[host]) == 0 {
			t.Fatalf("%s was never asked; the ladder did not run as without headers", host)
		}
		if seen.probed(host) {
			t.Fatalf("the caller header reached %s", host)
		}
	}
	if !strings.Contains(got.Partial, "without the caller's headers") {
		t.Fatalf("partial = %q, want the copy read without the caller's headers named", got.Partial)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for surface, text := range map[string]string{"output": string(encoded), "logs": logs()} {
		if strings.Contains(text, probeValue) {
			t.Fatalf("the header value appears in the %s", surface)
		}
	}
}
