package harvest

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"
)

// startProxyForTest starts one proxy, fails the test if it cannot start, and
// registers its stop. The fixture is here so every case below exercises the
// SAME start path production uses.
func startProxyForTest(t *testing.T) string {
	t.Helper()
	proxyURL, stop, err := StartBrowserProxy(context.Background(), func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("93.184.216.34")}, nil
	})
	if err != nil {
		t.Fatalf("StartBrowserProxy() error = %v", err)
	}
	t.Cleanup(stop)
	if !strings.HasPrefix(proxyURL, "http://127.0.0.1:") {
		t.Fatalf("proxy URL = %q, want a loopback http:// URL", proxyURL)
	}
	return proxyURL
}

// TestBrowserProxyPassesAnAllowedHostThrough is the pass-through arm: a
// request for an allowed host reaches the upstream server and its bytes come
// back verbatim. A fixture server can only live on loopback, which the pinned
// dialer refuses by design, so the dial seam (browserProxyDialer, the ONLY
// relaxation in this file) is swapped for one that reaches it; every refusal
// case below keeps the production dialer.
func TestBrowserProxyPassesAnAllowedHostThrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "upstream saw %s%s", r.Host, r.URL.Path)
	}))
	defer upstream.Close()
	target := upstream.Listener.Addr().String()

	previous := browserProxyDialer
	t.Cleanup(func() { browserProxyDialer = previous })
	browserProxyDialer = func(func(context.Context, string) ([]net.IP, error)) func(context.Context, string, string) (net.Conn, error) {
		return func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, target)
		}
	}
	previousLookup := lookupIP
	t.Cleanup(func() { lookupIP = previousLookup })
	lookupIP = func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("93.184.216.34")}, nil }

	proxyURL := startProxyForTest(t)
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(parsed)},
		Timeout:   10 * time.Second,
	}
	response, err := client.Get("http://allowed.example.test/page")
	if err != nil {
		t.Fatalf("request through the proxy failed: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read proxied body: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("proxied status = %d, want 200 (body %q)", response.StatusCode, body)
	}
	if want := "upstream saw allowed.example.test/page"; string(body) != want {
		t.Fatalf("proxied body = %q, want %q", body, want)
	}
}

// TestBrowserProxyRefusesAPrivateTarget is L2-F7's core: the browser rung must
// not be able to reach a private address even when the page asks for one. The
// production dialer is in place, so the refusal is publicIPs' own rule — the
// same one every other client in this package dials under — and it is NAMED
// on the wire, not delivered as a bare connection failure.
func TestBrowserProxyRefusesAPrivateTarget(t *testing.T) {
	proxyURL := startProxyForTest(t)
	for _, target := range []string{"127.0.0.1:80", "169.254.169.254:80", "10.0.0.5:8080"} {
		status, reason := proxyConnect(t, proxyURL, target)
		if status != http.StatusForbidden {
			t.Fatalf("CONNECT %s status = %d %q, want 403 (a refusal, not an outage)", target, status, reason)
		}
		if !strings.Contains(reason, browserProxyRefused) {
			t.Fatalf("CONNECT %s reason = %q, want it to name %q", target, reason, browserProxyRefused)
		}
	}
}

// TestBrowserProxyRefusesARebindingHost covers the address the FINDING is
// about: the host resolves publicly for the guard and privately for the dial.
// The proxy resolves it itself at dial time and refuses.
func TestBrowserProxyRefusesARebindingHost(t *testing.T) {
	previousLookup := lookupIP
	t.Cleanup(func() { lookupIP = previousLookup })
	lookupIP = func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("93.184.216.34")}, nil }

	proxyURL, stop, err := StartBrowserProxy(context.Background(), func(context.Context, string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("127.0.0.1")}, nil
	})
	if err != nil {
		t.Fatalf("StartBrowserProxy() error = %v", err)
	}
	t.Cleanup(stop)
	status, reason := proxyConnect(t, proxyURL, "rebind.attacker.test:443")
	if status != http.StatusForbidden || !strings.Contains(reason, browserProxyRefused) {
		t.Fatalf("CONNECT to a rebinding host = %d %q, want a named 403 refusal", status, reason)
	}
}

// TestBrowserProxyStopClosesTheListener proves stop is real: the port stops
// answering, and no goroutine the proxy started is still running.
func TestBrowserProxyStopClosesTheListener(t *testing.T) {
	before := runtime.NumGoroutine()
	proxyURL, stop, err := StartBrowserProxy(context.Background(), nil)
	if err != nil {
		t.Fatalf("StartBrowserProxy() error = %v", err)
	}
	address := strings.TrimPrefix(proxyURL, "http://")
	if conn, dialErr := net.DialTimeout("tcp", address, 5*time.Second); dialErr != nil {
		t.Fatalf("proxy did not accept before stop: %v", dialErr)
	} else if closeErr := conn.Close(); closeErr != nil {
		t.Fatalf("close probe connection: %v", closeErr)
	}
	stop()
	if conn, dialErr := net.DialTimeout("tcp", address, 2*time.Second); dialErr == nil {
		_ = conn.Close()
		t.Fatal("proxy still accepted a connection after stop")
	}
	stop() // idempotent: a second stop must not panic or block.
	assertGoroutinesSettled(t, before)
}

// TestBrowserProxyContextCancelStopsIt pins the other half of the lifetime
// contract: the caller's context ending is the same shutdown as stop.
func TestBrowserProxyContextCancelStopsIt(t *testing.T) {
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	proxyURL, stop, err := StartBrowserProxy(ctx, nil)
	if err != nil {
		cancel()
		t.Fatalf("StartBrowserProxy() error = %v", err)
	}
	address := strings.TrimPrefix(proxyURL, "http://")
	cancel()
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, dialErr := net.DialTimeout("tcp", address, time.Second)
		if dialErr != nil {
			break
		}
		_ = conn.Close()
		if time.Now().After(deadline) {
			stop()
			t.Fatal("proxy still accepted a connection after its context was cancelled")
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	assertGoroutinesSettled(t, before)
}

// assertGoroutinesSettled fails when the goroutine count has not returned to
// its pre-start value: the proxy's contract is that stop leaves nothing
// running. It polls, because the runtime reports a goroutine as live for a
// moment after its final statement.
func assertGoroutinesSettled(t *testing.T, before int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		now := runtime.NumGoroutine()
		if now <= before {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutines after stop = %d, want <= %d: a proxy goroutine outlived stop", now, before)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// proxyConnect sends one raw CONNECT through the proxy and returns the status
// code and the whole answer (status line plus body), so a test can assert the
// refusal is NAMED rather than merely non-200.
func proxyConnect(t *testing.T, proxyURL, target string) (int, string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(proxyURL, "http://"), 5*time.Second)
	if err != nil {
		t.Fatalf("dial the proxy: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("read the proxy answer: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read the proxy answer body: %v", err)
	}
	return response.StatusCode, response.Status + " " + string(body)
}
