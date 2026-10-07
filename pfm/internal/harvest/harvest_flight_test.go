package harvest

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestFetchLeaderCancelIsNeitherSharedNorCached: one caller's cancel (an MCP
// client that gave up) ended the shared walk, whose failure — the cancelled
// rungs read as a connection failure — was handed to every caller joined on
// that flight and then served from the negative cache to the callers after
// it. A joined caller whose own context is alive walks again, and a later
// caller walks instead of reading the cancelled walk's failure.
func TestFetchLeaderCancelIsNeitherSharedNorCached(t *testing.T) {
	t.Parallel()
	var blocking atomic.Bool
	var calls, liveCalls atomic.Int32
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		if err := request.Context().Err(); err != nil {
			return nil, err
		}
		if blocking.Load() {
			<-request.Context().Done()
			return nil, request.Context().Err()
		}
		liveCalls.Add(1)
		return nil, errors.New("fixture connection failure")
	})
	client := func() *http.Client { return &http.Client{Transport: transport} }
	h := mustNew(t, Options{
		ContactEmail: "test@example.org", CacheDir: t.TempDir(),
		Client: client(), Chrome: client(), Jina: client(), OA: client(),
		Converter: legacyConverterFunc(func(context.Context, string, string, []byte) (string, error) {
			return "", nil
		}),
		BrowserRung: browserOff(),
	})
	// cancelledWalk starts a fetch of source, holds it in its first rung, runs
	// during (which may join it), then cancels it and waits for its answer.
	cancelledWalk := func(source string, during func()) {
		blocking.Store(true)
		before := calls.Load()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		leader := make(chan Result, 1)
		go func() { leader <- h.Fetch(ctx, source) }()
		deadline := time.Now().Add(5 * time.Second)
		for calls.Load() == before && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		during()
		blocking.Store(false)
		cancel()
		<-leader
	}

	joined := make(chan Result, 1)
	cancelledWalk("https://leader.example.test/joined", func() {
		go func() { joined <- h.Fetch(context.Background(), "https://leader.example.test/joined") }()
		time.Sleep(20 * time.Millisecond) // let the twin reach the flight
	})
	if twin := <-joined; twin.Error == "" || liveCalls.Load() == 0 {
		t.Fatalf("joined caller answered %q from the cancelled walk; want a walk of its own", twin.Error)
	}

	cancelledWalk("https://leader.example.test/later", func() {})
	if stats, err := os.ReadFile(filepath.Join(h.options.CacheDir, statsFilename)); err == nil &&
		strings.Contains(string(stats), "/later") {
		t.Fatalf("the cancelled walk was scored against the source: %s", stats)
	}
	walked := liveCalls.Load()
	if later := h.Fetch(context.Background(), "https://leader.example.test/later"); later.Error == "" ||
		liveCalls.Load() == walked {
		t.Fatalf("a later caller answered %q from the cancelled walk's cache entry; want a walk", later.Error)
	}
}

// TestFetchConverterLoadRefusalIsNotCached: a burst that saturated the
// converter pool answered its refused reads with a convert failure, and the
// negative cache then served that failure for the full NegativeTTL after
// the workers freed up — on a URL and on a local file alike. A walk refused
// for load (NoteRefusedForLoad) is the server's, not the source's: the next
// read walks again, and neither it nor a refresh is scored. A converter that
// fails on the document itself is still cached and scored.
func TestFetchConverterLoadRefusalIsNotCached(t *testing.T) {
	t.Parallel()
	body := "<html><body>" + strings.Repeat("real article content ", 80) + "</body></html>"
	page := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host == "defuddle.md" {
			return response(r, http.StatusForbidden, "text/html", "<html><body>access denied</body></html>"), nil
		}
		return response(r, http.StatusOK, "text/html", body), nil
	})
	noNetwork := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("no live network in tests")
	})
	cases := map[string]struct {
		forLoad    bool
		wantCached bool
	}{
		"load refusal walks again":      {true, false},
		"document failure stays cached": {false, true},
	}
	sources := map[string]func(t *testing.T) string{
		"url": func(*testing.T) string { return "https://example.test/burst" },
		"local file": func(t *testing.T) string {
			path := filepath.Join(t.TempDir(), "burst.html")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			return path
		},
	}
	for name, tc := range cases {
		for sourceName, sourceOf := range sources {
			t.Run(name+"/"+sourceName, func(t *testing.T) {
				t.Parallel()
				var refusing atomic.Bool
				refusing.Store(true)
				h := mustNew(t, Options{
					CacheDir: t.TempDir(),
					Client:   &http.Client{Transport: page}, Chrome: &http.Client{Transport: page},
					Jina: &http.Client{Transport: noNetwork}, OA: &http.Client{Transport: noNetwork},
					Converter: legacyConverterFunc(func(ctx context.Context, _, _ string, _ []byte) (string, error) {
						if refusing.Load() {
							if tc.forLoad {
								NoteRefusedForLoad(ctx)
							}
							return "", errors.New("converter refused")
						}
						return strings.Repeat("real article content ", 80), nil
					}),
					BrowserRung: browserOff(),
				})
				source := sourceOf(t)
				if first := h.Fetch(context.Background(), source); first.Error == "" {
					t.Fatal("Fetch() with a refusing converter returned no error")
				}
				if sourceName == "url" {
					h.FetchWithOptions(context.Background(), source, FetchOptions{Refresh: true})
					stats, _ := os.ReadFile(filepath.Join(h.options.CacheDir, statsFilename))
					if scored := strings.Contains(string(stats), "/burst"); scored != tc.wantCached {
						t.Fatalf("refused walk and refresh scored = %v, want %v: %s", scored, tc.wantCached, stats)
					}
				}
				refusing.Store(false)
				second := h.Fetch(context.Background(), source)
				if cached := second.Error != ""; cached != tc.wantCached {
					t.Fatalf("read after the refusal: cached failure = %v (Error=%q), want %v",
						cached, second.Error, tc.wantCached)
				}
			})
		}
	}
}
