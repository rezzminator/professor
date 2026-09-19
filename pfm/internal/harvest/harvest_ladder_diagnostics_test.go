package harvest

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

// TestDefuddleRungTransportFailureReachesTerminalDiagnostic pins F11: the
// defuddle rung previously updated neither lastErr nor lastErrorKind on a
// transport failure, the way its Jina sibling already did — so the terminal
// receipt reported an empty ErrorKind for a rung that actually failed to
// reach the network. Watched FAILING before the fix (ErrorKind was "").
func TestDefuddleRungTransportFailureReachesTerminalDiagnostic(t *testing.T) {
	const source = "https://example.test/article"
	const challengeBody = "<html><body>Checking your browser before accessing this site</body></html>"
	client := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		switch r.URL.Host {
		case "example.test":
			// A challenge answer: sets lastChallenge but touches neither
			// lastErr nor lastErrorKind, isolating the defuddle transport
			// failure below as the only thing that can set ErrorKind.
			return response(r, http.StatusOK, "text/html", challengeBody), nil
		case "defuddle.md":
			return nil, errors.New("connection reset by peer")
		default:
			t.Fatalf("unexpected host %s", r.URL.Host)
			return nil, nil
		}
	})
	challenge := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusOK, "text/html", challengeBody), nil
	})
	noNetwork := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("no live network in tests")
	})
	h := mustNew(t, Options{
		CacheDir:  t.TempDir(),
		Client:    &http.Client{Transport: client},
		Chrome:    &http.Client{Transport: challenge},
		Jina:      &http.Client{Transport: challenge},
		OA:        &http.Client{Transport: noNetwork},
		Converter: &fakeConverter{},
	})
	got := h.Fetch(context.Background(), source)
	if got.Error == "" {
		t.Fatal("Fetch() over an all-rungs-failing ladder returned no error")
	}
	if got.ErrorKind == "" {
		t.Fatalf(
			"the defuddle rung's transport failure never reached the terminal diagnostic (F11): ErrorKind is empty, Error=%q",
			got.Error,
		)
	}
}

// TestStaticRungConverterFailureIsNamedToolOutage pins F12: a converter
// failure on the direct/chrome (static) rungs used to `continue` silently,
// while the browser rung already names its own converter failure as a tool
// outage. Watched FAILING before the fix (the message named neither a
// conversion failure nor a tool outage).
func TestStaticRungConverterFailureIsNamedToolOutage(t *testing.T) {
	const source = "https://example.test/article"
	// The direct rung answers with usable-looking HTML (long enough to pass
	// the thin-page floor) so the ladder reaches the converter at all; the
	// fake converter below then fails every call, exactly as a crashed
	// sidecar would (F3's sibling failure mode).
	body := "<html><body>" + strings.Repeat("real article content ", 80) + "</body></html>"
	client := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "defuddle.md" {
			// Walled: defuddle.md never invokes the converter (it treats its
			// own body as already-converted Markdown), so it must fail on
			// its own terms rather than accidentally succeeding and hiding
			// the static rungs' converter failure this test targets.
			return response(r, http.StatusForbidden, "text/html", "<html><body>access denied</body></html>"), nil
		}
		return response(r, http.StatusOK, "text/html", body), nil
	})
	noNetwork := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("no live network in tests")
	})
	h := mustNew(t, Options{
		CacheDir:  t.TempDir(),
		Client:    &http.Client{Transport: client},
		Chrome:    &http.Client{Transport: client},
		Jina:      &http.Client{Transport: noNetwork},
		OA:        &http.Client{Transport: noNetwork},
		Converter: failingConverter{},
	})
	got := h.Fetch(context.Background(), source)
	if got.Error == "" {
		t.Fatal("Fetch() with a converter that always fails returned no error")
	}
	if !strings.Contains(got.Error, "tool outage") {
		t.Fatalf("static-rung converter failure was not named a tool outage (F12): Error=%q", got.Error)
	}
}

// failingConverter always fails, standing in for a crashed converter
// backend (F12/F3's sibling failure mode) on every rung that calls it.
type failingConverter struct{}

func (failingConverter) Convert(_ context.Context, _, _ string, _ []byte) (string, error) {
	return "", errors.New("converter backend crashed")
}
