package harvest

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRecoverItemCapturesPanicIntoError is recoverItem's own unit test
// (F1): a panic inside the deferred recover must reach set, and a normal
// (non-panicking) defer run must never call set at all.
func TestRecoverItemCapturesPanicIntoError(t *testing.T) {
	var recovered error
	func() {
		defer recoverItem(func(e error) { recovered = e })
		panic("boom")
	}()
	if recovered == nil || !strings.Contains(recovered.Error(), "boom") {
		t.Fatalf("recovered = %v, want an error naming the panic value", recovered)
	}

	var untouched error
	func() {
		defer recoverItem(func(e error) { untouched = e })
	}()
	if untouched != nil {
		t.Fatalf("recoverItem set an error with no panic: %v", untouched)
	}
}

// TestResolveDOIFanOutRecoversPanickingProvider pins F1 at resolver.go:214:
// one provider's goroutine panicking must not crash the whole ResolveDOI
// fan-out (let alone the daemon process serving every other chat). Watched
// FAILING before the fix: the unrecovered goroutine panic terminated the
// whole test binary rather than failing gracefully.
func TestResolveDOIFanOutRecoversPanickingProvider(t *testing.T) {
	withPublicDNSForProviderTest(t)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Host, "api.unpaywall.org") {
			panic("simulated provider panic")
		}
		return nil, errors.New("no candidate from this provider")
	})}
	resolver := &Resolver{Client: client, ContactEmail: "test@example.test"}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = resolver.ResolveDOI(context.Background(), "10.1000/panic-test")
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("ResolveDOI never returned — a provider panic likely hung the fan-out")
	}
}

// TestFindWorksFanOutRecoversPanickingProvider pins F1 at find_works.go:36.
func TestFindWorksFanOutRecoversPanickingProvider(t *testing.T) {
	withPublicDNSForProviderTest(t)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.Contains(r.URL.Host, "api.openalex.org") {
			panic("simulated provider panic")
		}
		return nil, errors.New("no candidate from this provider")
	})}
	resolver := &Resolver{Client: client}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = resolver.FindWorks(context.Background(), "a panic-inducing query", 5)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("FindWorks never returned — a provider panic likely hung the fan-out")
	}
}

// TestLocalizeImagesRecoversPanickingDownload pins F1 at images.go:65.
func TestLocalizeImagesRecoversPanickingDownload(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		panic("simulated image download panic")
	})}
	h := mustNew(t, Options{CacheDir: t.TempDir(), Client: client, Chrome: client})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = h.LocalizeImages(
			context.Background(),
			"![figure](https://example.test/panics.png)",
			"https://example.test/article",
		)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("LocalizeImages never returned — a download panic likely hung the fan-out")
	}
}

// TestDoHQueryRecoversPanickingQueryType pins F1 at doh.go:368.
func TestDoHQueryRecoversPanickingQueryType(t *testing.T) {
	resolver := &dohResolver{
		endpoint: "https://doh.example.test/dns-query",
		client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if strings.Contains(r.URL.RawQuery, "type=AAAA") {
				panic("simulated DoH query panic")
			}
			return nil, errors.New("no answer")
		})},
		cache:  make(map[string]dohEntry),
		warned: make(map[string]bool),
		fallback: func(context.Context, string) ([]net.IP, error) {
			return nil, errors.New("fallback also unavailable")
		},
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = resolver.query(context.Background(), "panics.example.test")
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("dohResolver.query never returned — a query panic likely hung the fan-out")
	}
}
