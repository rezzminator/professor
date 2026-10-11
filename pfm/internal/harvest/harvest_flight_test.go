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

// TestFetchFailureIsCachedBeforeItsFlightEnds: the leader of a failed walk
// removed its in-flight entry before writing the failure to the negative
// cache, so a caller in that window found neither and walked the source that
// had just failed a second time. Each case holds one party in its window
// through fetchShared's seams: the leader once it has removed its flight, or
// a caller that missed the negative cache while the walk was still running.
// Either way the caller answers the cached failure and walks nothing.
func TestFetchFailureIsCachedBeforeItsFlightEnds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		holdLeader bool // hold the leader past its flight's removal; else hold the caller at the flight table
	}{
		{"caller arriving as the leader removes its flight", true},
		{"caller that missed the cache while the walk ran", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			held, release, walking := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var calls, entered atomic.Int32
			transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
				if calls.Add(1) == 1 && !tc.holdLeader {
					close(walking)
					<-held // the caller has missed the cache and waits at the flight table
				}
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
			hold := func(string) {
				close(held)
				<-release
			}
			if tc.holdLeader {
				var once atomic.Bool
				h.hooks.left = func(key string) {
					if once.CompareAndSwap(false, true) {
						hold(key)
					}
				}
			} else {
				h.hooks.entering = func(key string) {
					if entered.Add(1) == 2 {
						hold(key)
					}
				}
			}
			const source = "https://window.example.test/source"
			leader, caller := make(chan Result, 1), make(chan Result, 1)
			go func() { leader <- h.Fetch(context.Background(), source) }()
			var walked int32
			if tc.holdLeader {
				<-held // the leader walked, failed and removed its flight
				walked = calls.Load()
				caller <- h.Fetch(context.Background(), source)
				close(release)
				<-leader
			} else {
				<-walking
				go func() { caller <- h.Fetch(context.Background(), source) }()
				first := <-leader // the walk ends while the caller waits past its cache miss
				walked = calls.Load()
				close(release)
				if first.Error == "" {
					t.Fatalf("leader answered no failure: %#v", first)
				}
			}
			got := <-caller
			if !strings.Contains(got.Error, "recently failed; cached") || calls.Load() != walked {
				t.Fatalf("caller answered %q after %d transport calls (the walk made %d); "+
					"want the cached failure and no second walk", got.Error, calls.Load(), walked)
			}
		})
	}
}

// TestFetchSizeOnlyLeaderSharesTheWholePage: a size-only caller that led a
// walk blanked the content of the result its flight shared, so a caller
// joined on it for the page answered a success with no content — absence
// where the page had been read. The joiner answers the page; the leader its
// size alone.
func TestFetchSizeOnlyLeaderSharesTheWholePage(t *testing.T) {
	t.Parallel()
	walking, joined := make(chan struct{}), make(chan struct{})
	words := strings.Repeat("shared page words ", 120)
	var calls atomic.Int32
	page := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(walking)
			<-joined // the full-content caller waits on this walk
		}
		return response(
			request,
			http.StatusOK,
			"text/html",
			"<html><body><article><p>"+words+"</p></article></body></html>",
		), nil
	})
	h := mustNew(t, Options{
		CacheDir: t.TempDir(), Client: &http.Client{Transport: page}, Chrome: &http.Client{Transport: page},
		Converter: legacyConverterFunc(func(context.Context, string, string, []byte) (string, error) {
			return "# Shared\n\n" + words, nil
		}),
		BrowserRung: browserOff(),
	})
	var once atomic.Bool
	h.hooks.joined = func(string) {
		if once.CompareAndSwap(false, true) {
			close(joined)
		}
	}
	const source = "https://shared.example.test/page"
	leader := make(chan Result, 1)
	go func() { leader <- h.FetchWithOptions(context.Background(), source, FetchOptions{SizeOnly: true}) }()
	<-walking
	full := h.Fetch(context.Background(), source)
	sized := <-leader
	if full.Error != "" || !strings.Contains(full.Content, "shared page words") {
		t.Fatalf(
			"caller joined on a size-only walk answered error=%q content=%q; want the page",
			full.Error,
			full.Content,
		)
	}
	if sized.Error != "" || sized.Content != "" || sized.Tokens == 0 {
		t.Fatalf("size-only leader answered error=%q content=%d chars tokens=%d; want its size alone",
			sized.Error, len(sized.Content), sized.Tokens)
	}
}

// TestFetchRefreshThatReadsClearsTheCachedFailure: a source that failed
// stayed in the negative cache after a refresh read it, so the next plain
// read answered the old failure — an error where the page had just been read
// — until the entry expired. A refresh that reads the source drops its cached
// failure, and a plain walk the refresh overtook — begun before it, failed
// after it — never caches its failure over that read.
func TestFetchRefreshThatReadsClearsTheCachedFailure(t *testing.T) {
	t.Parallel()
	type refreshKey struct{}
	for _, tc := range []struct {
		name      string
		overtaken bool // the plain walk is still in flight while the refresh reads
	}{
		{"failure cached before the refresh", false},
		{"failure of a walk the refresh overtook", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var failing, held atomic.Bool
			failing.Store(true)
			walking, release := make(chan struct{}), make(chan struct{})
			words := strings.Repeat("recovered page words ", 120)
			transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.Context().Value(refreshKey{}) == nil {
					if tc.overtaken && held.CompareAndSwap(false, true) {
						close(walking)
						<-release // the plain walk hangs while the refresh reads the page
					}
					if failing.Load() {
						return nil, errors.New("fixture connection failure")
					}
				}
				return response(
					request,
					http.StatusOK,
					"text/html",
					"<html><body><article><p>"+words+"</p></article></body></html>",
				), nil
			})
			client := func() *http.Client { return &http.Client{Transport: transport} }
			h := mustNew(t, Options{
				CacheDir: t.TempDir(), Client: client(), Chrome: client(), Jina: client(), OA: client(),
				Converter: legacyConverterFunc(func(context.Context, string, string, []byte) (string, error) {
					return "# Recovered\n\n" + words, nil
				}),
				BrowserRung: browserOff(),
			})
			const source = "https://recovered.example.test/page"
			plain := make(chan Result, 1)
			if tc.overtaken {
				go func() { plain <- h.Fetch(context.Background(), source) }()
				<-walking
			} else {
				plain <- h.Fetch(context.Background(), source)
			}
			if refreshed := h.FetchWithOptions(
				context.WithValue(context.Background(), refreshKey{}, true),
				source,
				FetchOptions{Refresh: true},
			); refreshed.Error != "" {
				t.Fatalf("refresh of the recovered source failed: %q", refreshed.Error)
			}
			close(release)
			if first := <-plain; first.Error == "" {
				t.Fatalf("plain read of the failing source answered no error: %#v", first)
			}
			failing.Store(false)
			if after := h.Fetch(
				context.Background(),
				source,
			); after.Error != "" ||
				!strings.Contains(after.Content, "recovered page words") {
				t.Fatalf("read after a refresh that read the page answered error=%q; want the page", after.Error)
			}
		})
	}
}

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
