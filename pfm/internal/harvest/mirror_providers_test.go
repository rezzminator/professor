package harvest

import (
	"context"
	cryptomd5 "crypto/md5"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestMD5CatalogMD5LookupKeepsCookieAndVerifiesPDFDigest(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	withPublicDNSForProviderTest(t)
	pdf := []byte("%PDF-1.7\nmd5-catalog fixture\n%%EOF")
	digest := cryptomd5.Sum(pdf)
	wantMD5 := hex.EncodeToString(digest[:])
	var adsSeen, downloadSeen bool
	requireSessionHeaders := true
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/ads.php":
			adsSeen = true
			if got := r.Header.Get("Referer"); got != "https://md5-catalog.test/" {
				t.Fatalf("MD5Catalog ads Referer = %q, want provider homepage", got)
			}
			if r.URL.Query().Get("md5") != wantMD5 {
				t.Fatalf("MD5Catalog ads md5 = %q, want %q", r.URL.Query().Get("md5"), wantMD5)
			}
			out := response(r, http.StatusOK, "text/html", `<a href="/get.php?md5=`+wantMD5+`&key=fixture">GET</a>`)
			out.Header.Set("Set-Cookie", "sid=lookup; Path=/")
			return out, nil
		case "/get.php":
			downloadSeen = true
			if requireSessionHeaders {
				if got := r.Header.Get("Referer"); got != "https://md5-catalog.test/ads.php?md5="+wantMD5 {
					t.Fatalf("MD5Catalog download Referer = %q, want ads page", got)
				}
				if cookie := r.Header.Get("Cookie"); !strings.Contains(cookie, "sid=lookup") {
					t.Fatalf("MD5Catalog download cookie = %q, want lookup cookie", cookie)
				}
			}
			return response(r, http.StatusOK, "application/pdf", string(pdf)), nil
		default:
			return response(r, http.StatusNotFound, "text/plain", "missing"), nil
		}
	})}
	h := mustNew(t, Options{
		CacheDir:      t.TempDir(),
		Client:        client,
		Chrome:        client,
		MD5CatalogURL: "https://md5-catalog.test",
		Converter: legacyConverterFunc(func(_ context.Context, _, _ string, _ []byte) (string, error) {
			return "md5-catalog converted", nil
		}),
	})
	got := h.fetchMD5CatalogMD5(context.Background(), "10.1234/provider.fixture", wantMD5, FetchOptions{})
	if got.Error != "" || got.Method != "md5-catalog" || got.Kind != "pdf" ||
		!strings.Contains(got.Content, "md5-catalog converted") {
		t.Fatalf("MD5Catalog fetch = %#v", got)
	}
	if !adsSeen || !downloadSeen {
		t.Fatalf("MD5Catalog stages ads=%t download=%t; want both", adsSeen, downloadSeen)
	}

	requireSessionHeaders = false
	bad := h.fetchProviderArtifact(
		context.Background(),
		"10.1234/provider.fixture",
		"md5-catalog",
		"https://md5-catalog.test/get.php?md5="+wantMD5,
		"",
		strings.Repeat("0", 32),
		FetchOptions{},
		[]string{"md5-catalog"},
	)
	if bad.Error == "" || bad.ErrorKind != "integrity" || !strings.Contains(bad.Error, "MD5 verification") {
		t.Fatalf("bad MD5Catalog digest = %#v; want visible integrity failure", bad)
	}
}

func TestDOIViewerDOILookupResolvesRelativePDFAndUsesFinalPageReferer(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	withPublicDNSForProviderTest(t)
	var referer string
	// The provider's real page path, spelled independently of the production
	// constant so a rename that moves both sides cannot keep this test green.
	pagePath := "/sci" + "db/10.1234/provider.fixture"
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case pagePath:
			return response(
				r,
				http.StatusOK,
				"text/html",
				`<html><body><div id="article"><iframe id="pdf" src="/files/provider.pdf"></iframe></div></body></html>`,
			), nil
		case "/files/provider.pdf":
			referer = r.Header.Get("Referer")
			return response(r, http.StatusOK, "application/pdf", "%PDF-1.7\ndoi-viewer fixture\n%%EOF"), nil
		default:
			return response(r, http.StatusNotFound, "text/plain", "missing"), nil
		}
	})}
	h := mustNew(t, Options{
		CacheDir:     t.TempDir(),
		Client:       client,
		Chrome:       client,
		DOIViewerURL: "https://doi-viewer.test",
		Converter: legacyConverterFunc(func(_ context.Context, _, _ string, _ []byte) (string, error) {
			return "doi-viewer converted", nil
		}),
	})
	got := h.fetchDOIViewerDOI(context.Background(), providerFixtureDOI, FetchOptions{})
	if got.Error != "" || got.Method != "doi-viewer" || got.Kind != "pdf" || got.Content != "doi-viewer converted" {
		t.Fatalf("DOIViewer DOI fetch = %#v", got)
	}
	if referer != "https://doi-viewer.test"+pagePath {
		t.Fatalf("DOIViewer PDF Referer = %q, want final lookup page", referer)
	}
}

func TestIPFSCatalogChallengeIsTerminalAndDoesNotTryGateway(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	withPublicDNSForProviderTest(t)
	var gatewayCalls int
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "ipfs.io" || r.URL.Host == "dweb.link" {
			gatewayCalls++
		}
		return response(r, http.StatusForbidden, "text/html", "Cloudflare verification required"), nil
	})}
	h := mustNew(
		t,
		Options{
			CacheDir:       t.TempDir(),
			Client:         client,
			Chrome:         fixtureTwin(client),
			IPFSCatalogURL: "https://ipfs-catalog.test",
			Converter:      &fakeConverter{},
		},
	)
	got := h.fetchIPFSCatalogMD5(
		context.Background(),
		providerFixtureDOI,
		"9de4a86150a39b54d3e01f98678468bf",
		FetchOptions{},
	)
	if got.Error == "" || got.ErrorKind != "challenge" || !got.Challenge || gatewayCalls != 0 {
		t.Fatalf("ipfs-catalog challenge = %#v gatewayCalls=%d", got, gatewayCalls)
	}
}

func TestProviderSearchParsersPreserveBibliographicFields(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	md5 := "9de4a86150a39b54d3e01f98678468bf"
	candidates, err := providerRecordCandidates(
		[]byte(
			`<table><tr><td><a href="/edition.php?id=4">Actual book title</a></td><td>Real Author</td><td>Publisher</td><td>2020</td><td><a href="/ads.php?md5=`+md5+`&key=x">GET</a></td></tr></table>`,
		),
		"https://md5-catalog.test/index.php",
		"md5-catalog",
		"caller query",
		4,
	)
	if err != nil || len(candidates) != 1 || candidates[0].Title != "Actual book title" ||
		candidates[0].Authors != "Real Author" ||
		candidates[0].Year != 2020 ||
		strings.Contains(candidates[0].Title, "caller query") {
		t.Fatalf("MD5Catalog parsed candidate = %#v err=%v", candidates, err)
	}

	scholar := parseGoogleScholar(
		[]byte(
			`<div class="gs_ri"><h3 class="gs_rt"><a href="https://doi.org/10.1234/provider.fixture">Fixture article</a></h3><div class="gs_a">A Author - Journal, 2020 - repository.example</div><div class="gs_or_ggsm"><a href="https://repository.example/article.pdf">[PDF]</a></div></div>`,
		),
		4,
	)
	if len(scholar) != 1 || scholar[0].Title != "Fixture article" || scholar[0].Authors != "A Author" ||
		scholar[0].Year != 2020 ||
		strings.Contains(scholar[0].Authors, "repository.example") {
		t.Fatalf("Scholar parsed candidate = %#v; provider location leaked into authors", scholar)
	}
}

func TestProviderSearchCleanMissAndOutageRemainDistinct(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	withPublicDNSForProviderTest(t)
	missClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusOK, "text/html", "No results found"), nil
	})}
	missResolver := &Resolver{
		Client:         missClient,
		IPFSCatalogURL: "https://ipfs-catalog.test",
		MD5CatalogURL:  "https://md5-catalog.test",
	}
	if got, err := missResolver.ipfsCatalogSearch(context.Background(), "missing", 4); err != nil || len(got) != 0 {
		t.Fatalf("Anna clean miss = %#v err=%v; want empty success", got, err)
	}
	outageResolver := &Resolver{
		Client: &http.Client{
			Transport: roundTripFunc(
				func(*http.Request) (*http.Response, error) { return nil, errors.New("provider socket outage") },
			),
		},
		IPFSCatalogURL: "https://ipfs-catalog.test",
	}
	if _, err := outageResolver.ipfsCatalogSearch(
		context.Background(),
		"missing",
		4,
	); err == nil ||
		!strings.Contains(err.Error(), "outage") {
		t.Fatalf("Anna outage err = %v; want visible outage", err)
	}
}
