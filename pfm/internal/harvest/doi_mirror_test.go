package harvest

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

const doiMirrorFixtureDOI = "10.1371/journal.pone.0033693"

func withPublicDNSForProviderTest(t *testing.T) {
	t.Helper()
	previous := lookupIP
	lookupIP = func(string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.10")}, nil
	}
	t.Cleanup(func() { lookupIP = previous })
}

func doiMirrorFixtureTransport(t *testing.T, pdfReferer, postIdentifier *string) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch {
		case r.Method == http.MethodPost && r.URL.Host == "doi-mirror.test":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			values, err := url.ParseQuery(string(body))
			if err != nil {
				return nil, err
			}
			*postIdentifier = values.Get("request")
			finalRequest := r.Clone(r.Context())
			finalRequest.URL, _ = url.Parse("https://doi-mirror.test/article/fixture")
			return response(
				finalRequest,
				http.StatusOK,
				"text/html",
				`<html><body><div id="article"><iframe id="pdf" src="//pdf.mirror.test/pdf/fixture.pdf#view=FitH"></iframe></div></body></html>`,
			), nil
		case r.Method == http.MethodGet && r.URL.Host == "pdf.mirror.test":
			*pdfReferer = r.Header.Get("Referer")
			return response(r, http.StatusOK, "application/pdf", "%PDF-1.7\nfixture\n%%EOF"), nil
		default:
			return response(r, http.StatusNotFound, "application/json", `{}`), nil
		}
	})}
}

func TestDOIMirrorLookupPostsIdentifierAndUsesFinalPageAsPDFReferer(t *testing.T) {
	withPublicDNSForProviderTest(t)
	var pdfReferer, postIdentifier string
	client := doiMirrorFixtureTransport(t, &pdfReferer, &postIdentifier)
	converter := &fakeConverter{}
	h := mustNew(t, Options{
		CacheDir:     t.TempDir(),
		Client:       client,
		Chrome:       client,
		Converter:    converter,
		DOIMirrorURL: "https://doi-mirror.test/",
	})

	got := h.fetchDOIMirror(context.Background(), doiMirrorFixtureDOI, FetchOptions{})
	if got.Error != "" {
		t.Fatalf("fetchDOIMirror() error = %q", got.Error)
	}
	if got.Source != doiMirrorFixtureDOI || got.Method != "doi-mirror" || got.Kind != "pdf" {
		t.Fatalf("fetchDOIMirror() receipt = %#v", got)
	}
	if postIdentifier != doiMirrorFixtureDOI {
		t.Fatalf("DOIMirror POST request identifier = %q, want %q", postIdentifier, doiMirrorFixtureDOI)
	}
	if pdfReferer != "https://doi-mirror.test/article/fixture" {
		t.Fatalf("PDF Referer = %q, want final lookup page URL", pdfReferer)
	}
	if converter.calls != 1 {
		t.Fatalf("converter calls = %d, want one PDF conversion", converter.calls)
	}
	if !containsString(got.Rungs, "doi-mirror") {
		t.Fatalf("DOIMirror rung missing from receipt: %#v", got.Rungs)
	}
}

func TestDOIMirrorPOSTRedirectsToDOIPageAndUsesFinalPageAsPDFReferer(t *testing.T) {
	withPublicDNSForProviderTest(t)
	var methods []string
	var postIdentifier, pdfReferer string
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPost && r.URL.Host == "doi-mirror.test" && r.URL.Path == "/":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				return nil, err
			}
			values, err := url.ParseQuery(string(body))
			if err != nil {
				return nil, err
			}
			postIdentifier = values.Get("request")
			return &http.Response{
				StatusCode: http.StatusFound,
				Status:     http.StatusText(http.StatusFound),
				Header:     http.Header{"Location": []string{"/" + doiMirrorFixtureDOI}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    r,
			}, nil
		case r.Method == http.MethodGet && r.URL.Host == "doi-mirror.test" && r.URL.Path == "/"+doiMirrorFixtureDOI:
			return response(
				r,
				http.StatusOK,
				"text/html",
				`<div id="article"><iframe id="pdf" src="//pdf.mirror.test/pdf/redirect.pdf#view=FitH"></iframe></div>`,
			), nil
		case r.Method == http.MethodGet && r.URL.Host == "pdf.mirror.test":
			pdfReferer = r.Header.Get("Referer")
			return response(r, http.StatusOK, "application/pdf", "%PDF-1.7\nredirect fixture\n%%EOF"), nil
		default:
			return nil, errors.New("unexpected DOIMirror request: " + r.Method + " " + r.URL.String())
		}
	})}
	h := mustNew(t, Options{
		CacheDir:     t.TempDir(),
		Client:       client,
		Chrome:       client,
		Converter:    &fakeConverter{},
		DOIMirrorURL: "https://doi-mirror.test/",
	})

	got := h.fetchDOIMirror(context.Background(), doiMirrorFixtureDOI, FetchOptions{})
	if got.Error != "" {
		t.Fatalf("fetchDOIMirror() error = %q", got.Error)
	}
	if got.Method != "doi-mirror" || got.Kind != "pdf" {
		t.Fatalf("redirected DOIMirror receipt = %#v", got)
	}
	if postIdentifier != doiMirrorFixtureDOI {
		t.Fatalf("POST request identifier = %q, want %q", postIdentifier, doiMirrorFixtureDOI)
	}
	wantMethods := []string{"POST /", "GET /" + doiMirrorFixtureDOI, "GET /pdf/redirect.pdf"}
	if strings.Join(methods, "|") != strings.Join(wantMethods, "|") {
		t.Fatalf("redirect request sequence = %#v, want %#v", methods, wantMethods)
	}
	if pdfReferer != "https://doi-mirror.test/"+doiMirrorFixtureDOI {
		t.Fatalf("PDF Referer = %q, want final DOI page URL", pdfReferer)
	}
}

func TestDOIMirrorClientPreservesBaseRedirectPolicy(t *testing.T) {
	withPublicDNSForProviderTest(t)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("base redirect policy")
	called := false
	base := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		called = true
		return wantErr
	}}
	client := gatewayClient(base, jar)
	next, err := http.NewRequest(http.MethodGet, "https://mirror.example/final", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	gotErr := client.CheckRedirect(next, []*http.Request{{Method: http.MethodGet}})
	if !called {
		t.Fatal("base CheckRedirect callback was not called")
	}
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("wrapped CheckRedirect error = %v, want %v", gotErr, wantErr)
	}
}

func TestDOIMirrorBinaryPDFWithChallengeWordsIsAccepted(t *testing.T) {
	withPublicDNSForProviderTest(t)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Host != "doi-mirror.test" {
			return nil, errors.New("unexpected binary DOIMirror request: " + r.Method + " " + r.URL.String())
		}
		return response(
			r,
			http.StatusOK,
			"application/pdf",
			"%PDF-1.7\nCAPTCHA Cloudflare words inside a valid PDF\n%%EOF",
		), nil
	})}
	h := mustNew(t, Options{
		CacheDir:     t.TempDir(),
		Client:       client,
		Chrome:       client,
		Converter:    &fakeConverter{},
		DOIMirrorURL: "https://doi-mirror.test/",
	})

	got := h.fetchDOIMirror(context.Background(), doiMirrorFixtureDOI, FetchOptions{})
	if got.Error != "" || got.Method != "doi-mirror" || got.Kind != "pdf" {
		t.Fatalf("binary PDF containing challenge words = %#v", got)
	}
	if got.Challenge {
		t.Fatalf("valid binary PDF was marked as a challenge: %#v", got)
	}
}

func TestDOIMirrorPDFLinkResolvesSupportedFormsAndRejectsPrivateTargets(t *testing.T) {
	withPublicDNSForProviderTest(t)
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "iframe under article",
			body: `<div id="article"><iframe src="/pdf/paper.pdf"></iframe></div>`,
			want: "https://doi-mirror.test/pdf/paper.pdf",
		},
		{
			name: "pdf iframe id",
			body: `<iframe id="pdf" src="//pdf.mirror.test/pdf/paper.pdf#view=FitH"></iframe>`,
			want: "https://pdf.mirror.test/pdf/paper.pdf",
		},
		{
			name: "embed under article",
			body: `<div id="article"><embed src="https://pdf.mirror.test/pdf/paper.pdf"></div>`,
			want: "https://pdf.mirror.test/pdf/paper.pdf",
		},
		{
			name: "object",
			body: `<object type="application/pdf" data="/pdf/paper.pdf"></object>`,
			want: "https://doi-mirror.test/pdf/paper.pdf",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := doiMirrorPDFLink([]byte(tc.body), "https://doi-mirror.test/article/fixture")
			if err != nil || got != tc.want {
				t.Fatalf("doiMirrorPDFLink() = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	if _, err := doiMirrorPDFLink(
		[]byte(`<iframe id="pdf" src="http://127.0.0.1:8080/secret.pdf"></iframe>`),
		"https://doi-mirror.test/article/fixture",
	); err == nil ||
		!strings.Contains(err.Error(), "private") {
		t.Fatalf("private PDF target error = %v, want explicit private-host refusal", err)
	}
	for _, href := range []string{"http://[::1", "://malformed"} {
		if _, err := doiMirrorPDFLink(
			[]byte(`<iframe id="pdf" src="`+href+`"></iframe>`),
			"https://doi-mirror.test/article/fixture",
		); err == nil {
			t.Fatalf("malformed PDF href %q was accepted", href)
		}
	}
}

func TestDOIFallsBackToDOIMirrorAfterOpenAccessExhaustion(t *testing.T) {
	withPublicDNSForProviderTest(t)
	var pdfReferer, postIdentifier string
	client := doiMirrorFixtureTransport(t, &pdfReferer, &postIdentifier)
	oaClient := &http.Client{Transport: client.Transport}
	h := mustNew(t, Options{
		CacheDir:             t.TempDir(),
		Client:               client,
		Chrome:               fixtureTwin(client),
		Jina:                 client,
		OA:                   oaClient,
		Converter:            &fakeConverter{},
		ContactEmail:         "qa@example.test",
		DOIMirrorURL:         "https://doi-mirror.test/",
		NegativeTTL:          -1,
		NegativeTransientTTL: -1,
	})

	got := h.fetchKnownID(context.Background(), doiMirrorFixtureDOI, IdentifierDOI, FetchOptions{})
	if got.Error != "" || got.Method != "doi-mirror" {
		t.Fatalf("DOI fallback receipt = %#v", got)
	}
	if postIdentifier != doiMirrorFixtureDOI || pdfReferer != "https://doi-mirror.test/article/fixture" {
		t.Fatalf("DOIMirror fallback request identifier=%q referer=%q", postIdentifier, pdfReferer)
	}
}

func TestSuccessfulOpenAccessSkipsDOIMirrorFallback(t *testing.T) {
	withPublicDNSForProviderTest(t)
	const oaPDF = "https://repo.test/paper.pdf"
	direct := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() == oaPDF {
			return response(r, http.StatusOK, "application/pdf", "%PDF-1.7\nopen access\n%%EOF"), nil
		}
		return response(r, http.StatusNotFound, "application/json", `{}`), nil
	})}
	oaClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Host, "api.unpaywall.org") {
			return jsonResponse(
				r,
				`{"is_oa":true,"oa_status":"gold","best_oa_location":{"url_for_pdf":"`+oaPDF+`","version":"publishedVersion"}}`,
			), nil
		}
		return response(r, http.StatusNotFound, "application/json", `{}`), nil
	})}
	h := mustNew(t, Options{
		CacheDir:     t.TempDir(),
		Client:       direct,
		Chrome:       direct,
		Jina:         direct,
		OA:           oaClient,
		Converter:    &fakeConverter{},
		ContactEmail: "qa@example.test",
		DOIMirrorURL: "https://doi-mirror.test/",
	})

	got := h.fetchKnownID(context.Background(), "10.9999/oa", IdentifierDOI, FetchOptions{})
	if got.Error != "" || got.Method == "doi-mirror" || !containsString(got.Rungs, "oa:unpaywall") {
		t.Fatalf("successful OA receipt = %#v", got)
	}
}

func TestPMIDWithoutPMCIDFallsBackToDOIMirror(t *testing.T) {
	withPublicDNSForProviderTest(t)
	var pdfReferer, postIdentifier string
	fixture := doiMirrorFixtureTransport(t, &pdfReferer, &postIdentifier)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "doi-mirror.test" || r.URL.Host == "pdf.mirror.test" {
			return fixture.Transport.RoundTrip(r)
		}
		if strings.Contains(r.URL.Path, "/idconv/api/") {
			return jsonResponse(r, `{"records":[]}`), nil
		}
		return response(r, http.StatusNotFound, "application/json", `{}`), nil
	})}
	h := mustNew(t, Options{
		CacheDir:     t.TempDir(),
		Client:       client,
		Chrome:       fixtureTwin(client),
		OA:           fixtureTwin(client),
		Converter:    &fakeConverter{},
		DOIMirrorURL: "https://doi-mirror.test/",
	})

	got := h.fetchKnownID(context.Background(), "1234567", IdentifierPMID, FetchOptions{})
	if got.Error != "" || got.Method != "doi-mirror" {
		t.Fatalf("PMID fallback receipt = %#v", got)
	}
	if postIdentifier != "1234567" || pdfReferer != "https://doi-mirror.test/article/fixture" {
		t.Fatalf("PMID DOIMirror request identifier=%q referer=%q", postIdentifier, pdfReferer)
	}
}

// TestDOIMirrorLookupSplitsRequestFailureFromResponseFailure: a transport
// failure (no response ever arrived) and a response failure (a response
// arrived and then its body failed to read/decode/fit the byte ceiling) are
// different facts about where the wire broke, and doiMirrorLookup's own
// message must say which.
func TestDOIMirrorLookupSplitsRequestFailureFromResponseFailure(t *testing.T) {
	withPublicDNSForProviderTest(t)

	t.Run("transport failure names the request", func(t *testing.T) {
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp: connection refused")
		})}
		h := mustNew(
			t,
			Options{
				CacheDir:     t.TempDir(),
				Client:       client,
				Chrome:       fixtureTwin(client),
				Converter:    &fakeConverter{},
				DOIMirrorURL: "https://doi-mirror.test/",
			},
		)
		got := h.fetchDOIMirror(context.Background(), doiMirrorFixtureDOI, FetchOptions{})
		if !strings.Contains(got.Error, "lookup request failed") {
			t.Fatalf("error = %q, want it to name the request failure", got.Error)
		}
		if strings.Contains(got.Error, "lookup response failed") {
			t.Fatalf("error = %q, a transport failure must not be reported as a response failure", got.Error)
		}
	})

	t.Run("oversize response names the response", func(t *testing.T) {
		oversize := strings.Repeat("a", 51*1024*1024)
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return response(r, http.StatusOK, "text/html", oversize), nil
		})}
		h := mustNew(
			t,
			Options{
				CacheDir:     t.TempDir(),
				Client:       client,
				Chrome:       fixtureTwin(client),
				Converter:    &fakeConverter{},
				DOIMirrorURL: "https://doi-mirror.test/",
			},
		)
		got := h.fetchDOIMirror(context.Background(), doiMirrorFixtureDOI, FetchOptions{})
		if !strings.Contains(got.Error, "lookup response failed") {
			t.Fatalf("error = %q, want it to name the response failure", got.Error)
		}
		if strings.Contains(got.Error, "lookup request failed") {
			t.Fatalf(
				"error = %q, a received-but-oversize response must not be reported as a request failure",
				got.Error,
			)
		}
	})
}

// TestReadDOIMirrorResponseErrorsAreSourceNeutral: readDOIMirrorResponse
// backs oversizeTruncate:false for EVERY gateway caller (postJSON,
// getJSONWithHeaders, searchBrave, the doi-mirror provider itself), so its
// own error strings must not name "doi-mirror" — a CORE or Brave failure
// reading "doi-mirror returned an empty response body" names the wrong
// source.
func TestReadDOIMirrorResponseErrorsAreSourceNeutral(t *testing.T) {
	_, _, _, err := readDOIMirrorResponse(nil, 1024)
	if err == nil || strings.Contains(err.Error(), "doi-mirror") {
		t.Fatalf("readDOIMirrorResponse(nil) error = %v, must not name doi-mirror", err)
	}
	emptyBody := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: nil}
	_, _, _, err = readDOIMirrorResponse(emptyBody, 1024)
	if err == nil || strings.Contains(err.Error(), "doi-mirror") {
		t.Fatalf("readDOIMirrorResponse(empty body) error = %v, must not name doi-mirror", err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
