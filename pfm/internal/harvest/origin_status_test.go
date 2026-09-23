package harvest

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// storedArtifacts lists every cache file but the stats.jsonl scoreboard (a
// failing fetch may still record its outcome there).
func storedArtifacts(t *testing.T, dir string) []string {
	t.Helper()
	var artifacts []string
	if err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Base(path) != "stats.jsonl" {
			artifacts = append(artifacts, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("walk cache %s: %v", dir, err)
	}
	return artifacts
}

func originStatusHarvester(t *testing.T, origin, jina roundTripFunc) *Harvester {
	t.Helper()
	missing := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	return mustNew(t, Options{
		CacheDir: t.TempDir(),
		Client:   &http.Client{Transport: origin},
		Chrome:   &http.Client{Transport: origin},
		Jina:     &http.Client{Transport: jina},
		OA:       &http.Client{Transport: missing},
		Converter: legacyConverterFunc(
			func(_ context.Context, _, _ string, raw []byte) (string, error) { return string(raw), nil },
		),
		BrowserRung: browserOff(),
	})
}

// errorPageProse is long enough to clear the html thin-content floor, so only
// the status can keep it out of the cache.
var errorPageProse = strings.Repeat("We could not find what you were looking for. Try the search box or the docs. ", 12)

// TestOriginMissingPageIsAnErrorNotStored: an origin answering 404 or 410 with
// a full HTML error page is an error naming the status, and nothing is stored —
// not the origin's page, and not the reader rung's 200 copy of it (the deleted
// Google Sheet came back 200 through jina while the origin said 410).
func TestOriginMissingPageIsAnErrorNotStored(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{
		{http.StatusNotFound, "HTTP 404 Not Found"},
		{http.StatusGone, "HTTP 410 Gone"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			origin := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return response(request, tc.status, "text/html",
					"<html><head><title>Page not found</title></head><body><h1>Error: page not found</h1><p>"+
						errorPageProse+"</p></body></html>"), nil
			})
			jina := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return response(
					request,
					http.StatusOK,
					"text/plain",
					"Title: Page not found\n\nMarkdown Content:\n# Sorry, the file you have requested has been deleted.\n\n"+
						errorPageProse,
				), nil
			})
			h := originStatusHarvester(t, origin, jina)
			result := h.Fetch(context.Background(), "https://origin.example.test/missing-page")
			if result.Error == "" {
				t.Fatalf(
					"a %d error page was stored as success: method=%q status=%d",
					tc.status,
					result.Method,
					result.HTTPStatus,
				)
			}
			if result.HTTPStatus != tc.status || !strings.Contains(result.Error, fmt.Sprintf("HTTP %d", tc.status)) {
				t.Fatalf("failure does not name %q: status=%d error=%q", tc.want, result.HTTPStatus, result.Error)
			}
			if got := PublicFailure(result.Source, result); got.HTTPStatus != tc.status ||
				!strings.Contains(got.Error, tc.want) {
				t.Fatalf("public failure does not name %q: %#v", tc.want, got)
			}
			if artifacts := storedArtifacts(t, h.options.CacheDir); len(artifacts) != 0 {
				t.Fatalf("the error page entered the cache: %v", artifacts)
			}
		})
	}
}

// sucuriBlockPage is the shape of a Sucuri firewall block (a phpBB forum's
// 403), scrubbed: documentation-range IP, example host.
const sucuriBlockPage = `<!DOCTYPE html><html><head><title>Access Denied - Sucuri Website Firewall</title></head>
<body><div id="main"><h1>Access Denied - Sucuri Website Firewall</h1>
<p>If you are the site owner (or you manage this site), please whitelist your IP or if you think this block is an error
please <a href="https://support.sucuri.net/?utm_source=firewall_block">open a support ticket</a> and make sure to include
the block details (displayed in the box below), so we can assist you in troubleshooting the issue.</p>
<h2>Block details:</h2><table>
<tr><td>Your IP:</td><td>192.0.2.10</td></tr><tr><td>URL:</td><td>forum.example.test/viewtopic.php?t=1</td></tr>
<tr><td>Your Browser:</td><td>Mozilla/5.0 (compatible; harvester/1.0)</td></tr><tr><td>Block ID:</td><td>BNP007</td></tr>
<tr><td>Block reason:</td><td>Bad bot access attempt.</td></tr><tr><td>Time:</td><td>2026-01-01 00:00:00</td></tr>
<tr><td>Server ID:</td><td>10000</td></tr></table></div></body></html>`

// TestVendorBlockPageIsAWallNeverStored: a Sucuri 403 block page is judged a
// wall; with the browser off the fetch fails loud as a challenge, nothing stored.
func TestVendorBlockPageIsAWallNeverStored(t *testing.T) {
	if !isChallenge([]byte(sucuriBlockPage), http.StatusForbidden) {
		t.Fatal("the Sucuri block page is not judged a wall")
	}
	for _, page := range []string{
		"<html><body><h1>403 ERROR</h1><h2>The request could not be satisfied.</h2>Request blocked.<br>Generated by cloudfront (CloudFront)</body></html>",
		"<html><body>Access denied<br>Error code: 1020<br>You do not have access to origin.example.test.</body></html>",
		`<html><body><h1>Access Denied</h1>You don't have permission to access this server.<p>Reference #18.0<p>https://errors.edgesuite.net/18.0</body></html>`,
		"<html><body>Request unsuccessful. Incapsula incident ID: 0-000000000</body></html>",
	} {
		if !isChallenge([]byte(page), http.StatusForbidden) {
			t.Fatalf("vendor block page not judged a wall: %q", page)
		}
	}
	wall := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusForbidden, "text/html", sucuriBlockPage), nil
	})
	missing := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusNotFound, "application/json", `{}`), nil
	})
	h := originStatusHarvester(t, wall, missing)
	result := h.Fetch(context.Background(), "https://forum.example.test/viewtopic.php?t=1")
	if result.Error == "" || !result.Challenge {
		t.Fatalf("the Sucuri block page was not a loud wall failure: error=%q challenge=%t method=%q",
			result.Error, result.Challenge, result.Method)
	}
	if artifacts := storedArtifacts(t, h.options.CacheDir); len(artifacts) != 0 {
		t.Fatalf("the block page entered the cache: %v", artifacts)
	}
}

// TestReaderCopyOfAnOriginErrorIsNotStored: a 403 error page no wall marker
// names is still no content, and jina's 200 envelope that says the target
// returned an error is the origin's error page, not a way past it.
func TestReaderCopyOfAnOriginErrorIsNotStored(t *testing.T) {
	origin := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusForbidden, "text/html",
			"<html><body><h1>Forbidden</h1><p>"+errorPageProse+"</p></body></html>"), nil
	})
	jina := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(
			request,
			http.StatusOK,
			"text/plain",
			"Title: Forbidden\nURL Source: https://origin.example.test/page\nWarning: Target URL returned error 403: Forbidden\n\n"+
				"Markdown Content:\n# Forbidden\n\n"+errorPageProse,
		), nil
	})
	h := originStatusHarvester(t, origin, jina)
	result := h.Fetch(context.Background(), "https://origin.example.test/page")
	if result.Error == "" {
		t.Fatalf(
			"an origin 403 error page was stored as success: method=%q status=%d",
			result.Method,
			result.HTTPStatus,
		)
	}
	if artifacts := storedArtifacts(t, h.options.CacheDir); len(artifacts) != 0 {
		t.Fatalf("the error page entered the cache: %v", artifacts)
	}
}

func TestPublicMethodNamesTheRungNeverTheProvider(t *testing.T) {
	for _, tc := range [][2]string{
		{"direct", "direct"},
		{"browser-chrome", "browser-chrome"},
		{"pdf:ocr", "pdf:ocr"},
		{"mirror:wayback", "mirror:wayback"},
		{sourceDOIMirror, "mirror"},
		{"mirror:https://secret.example", "mirror"},
	} {
		if got := PublicMethod(tc[0]); got != tc[1] {
			t.Fatalf("PublicMethod(%q)=%q, want %q", tc[0], got, tc[1])
		}
	}
}

// TestSiteAPIRenderReportsTheDeliveringStatus: a page an extractor rendered
// from its site's API while the origin walled it under 403 reports the API's
// status (200), the rung that delivered the stored content, never the wall's —
// in the core result and in the public JSON a consumer reads.
func TestSiteAPIRenderReportsTheDeliveringStatus(t *testing.T) {
	site := seQuestionSite(t)
	h, _ := site.harvester(t)
	result := h.FetchWithOptions(context.Background(), seQuestionURL, FetchOptions{Refresh: true})
	if result.Error != "" {
		t.Fatalf("the question failed: %q", result.Error)
	}
	public := JSONResults([]Result{h.PublicResult(seQuestionURL, result, true)})[0]
	if result.HTTPStatus != http.StatusOK || public.HTTPStatus != http.StatusOK {
		t.Fatalf("http_status core=%d public=%d, want the API's 200 (the origin walled the page with 403)",
			result.HTTPStatus, public.HTTPStatus)
	}
}
