package harvest

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"testing"
)

// TestGatewayAttemptNeverLeaksURLQueryOnTransportFailure pins F15's arming
// fix: http.Client.Do wraps a transport failure in *url.Error, carrying the
// FULL request URL — query string included, and with it a credential a
// provider puts there (books.go's Google Books key, for one). Watched
// FAILING before the fix (gatewayAttempt returned the raw err unsanitized).
func TestGatewayAttemptNeverLeaksURLQueryOnTransportFailure(t *testing.T) {
	const sentinel = "SENTINEL-KEY-1234"
	// A listener bound then immediately closed: connecting to it refuses
	// deterministically, producing a genuine *url.Error from client.Do.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	raw := "http://" + addr + "/lookup?key=" + sentinel

	var logBuf bytes.Buffer
	log.SetOutput(&logBuf)
	defer log.SetOutput(io.Discard)

	_, attemptErr := gatewayAttempt(context.Background(), gatewayRequest{
		url:           raw,
		client:        &http.Client{},
		trustedOrigin: true, // skip the SSRF pre-check; this test targets the transport-error path only
	})
	if attemptErr == nil {
		t.Fatal("gatewayAttempt against a closed listener returned a nil error")
	}
	if strings.Contains(attemptErr.Error(), sentinel) {
		t.Fatalf("gatewayAttempt error %q still carries the sentinel key", attemptErr.Error())
	}
	if strings.Contains(logBuf.String(), sentinel) {
		t.Fatalf("log output %q still carries the sentinel key", logBuf.String())
	}

	// The same pipeline gatewayFetch drives (escalation on) must not leak it
	// either: gateway.go:200's errGatewayNoRung message names req.url too.
	h := mustNew(t, Options{CacheDir: t.TempDir(), Client: &http.Client{}})
	h.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("boom")
	})}
	_, fetchErr := h.gatewayFetch(context.Background(), gatewayRequest{
		url:    "https://example.test/lookup?key=" + sentinel,
		client: h.client,
		policy: gatewayNoEscalate,
	})
	if fetchErr == nil {
		t.Fatal("gatewayFetch with a failing client returned a nil error")
	}
	if strings.Contains(fetchErr.Error(), sentinel) {
		t.Fatalf("gatewayFetch error %q still carries the sentinel key", fetchErr.Error())
	}
}

// recordCloseBody wraps an io.Reader as an http.Response.Body that records
// whether Close was called — F5's regression proof.
type recordCloseBody struct {
	io.Reader
	closed *bool
}

func (b recordCloseBody) Close() error {
	*b.closed = true
	return nil
}

// TestGatewayReadBodyClosesOnUnsupportedEncoding pins F5: net.go's
// decodedResponseBody returns a LIVE closer on its unsupported-encoding
// error arm, but gatewayReadBody used to check err before registering the
// defer that calls it — the body (and the connection) leaked on every hit.
// Watched FAILING before the fix (closed stayed false).
func TestGatewayReadBodyClosesOnUnsupportedEncoding(t *testing.T) {
	closed := false
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Encoding": {"compress"}},
		Body:       recordCloseBody{Reader: strings.NewReader("body"), closed: &closed},
	}
	_, _, _, err := gatewayReadBody(resp, 1024, true)
	if err == nil {
		t.Fatal("gatewayReadBody(unsupported encoding) returned a nil error")
	}
	if !closed {
		t.Fatal("gatewayReadBody(unsupported encoding) never closed the response body — F5 leak")
	}
}

// TestReadDOIMirrorResponseClosesOnUnsupportedEncoding is doi_mirror.go's
// twin of the F5 fix above (readDOIMirrorResponse's own defer ordering).
func TestReadDOIMirrorResponseClosesOnUnsupportedEncoding(t *testing.T) {
	closed := false
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Encoding": {"compress"}},
		Body:       recordCloseBody{Reader: strings.NewReader("body"), closed: &closed},
	}
	_, _, _, err := readDOIMirrorResponse(resp, 1024)
	if err == nil {
		t.Fatal("readDOIMirrorResponse(unsupported encoding) returned a nil error")
	}
	if !closed {
		t.Fatal("readDOIMirrorResponse(unsupported encoding) never closed the response body — F5 leak")
	}
}

// TestGatewayClientStripsCredentialHeaderCrossHostRedirect pins F16: a
// credential header (x-api-key / X-Subscription-Token) rides gatewayAttempt
// with no cross-host stripping in the redirect policy, while Go's stdlib
// only strips Authorization/Cookie-class headers. Two fake origins (matching
// the package's own doi_mirror_test.go pattern: a roundTripFunc transport
// plus the lookupIP seam, so no literal-loopback address ever reaches the
// SSRF guard) stand in for httptest servers. Watched FAILING before the fix
// (the second host saw the header).
func TestGatewayClientStripsCredentialHeaderCrossHostRedirect(t *testing.T) {
	withPublicDNSForProviderTest(t)
	const credential = "SENTINEL-API-KEY"
	var sawCredentialCrossHost bool
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "origin.test":
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": {"https://target.test/landed"}},
				Body:       http.NoBody,
				Request:    r,
			}, nil
		case "target.test":
			if r.Header.Get("X-Api-Key") == credential {
				sawCredentialCrossHost = true
			}
			return response(r, http.StatusOK, "text/plain", "ok"), nil
		default:
			t.Fatalf("unexpected request host %s", r.URL.Host)
			return nil, nil
		}
	})
	result, err := gatewayAttempt(context.Background(), gatewayRequest{
		url:    "https://origin.test/start",
		client: &http.Client{Transport: transport},
		max:    1024,
		headers: http.Header{
			"X-Api-Key": {credential},
		},
	})
	if err != nil {
		t.Fatalf("gatewayAttempt: %v", err)
	}
	if result.status != http.StatusOK {
		t.Fatalf("gatewayAttempt status = %d, want 200 (redirect should have been followed)", result.status)
	}
	if sawCredentialCrossHost {
		t.Fatal("the cross-host redirect target received the X-Api-Key credential header — F16")
	}
}
