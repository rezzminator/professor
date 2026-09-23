package harvestpy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLiveBrowserCallerHeadersStayOnTheOrigin: the browser rung's route adds
// the caller's headers to the document request and a same-origin subresource,
// never to a cross-origin one. A local server stands in for Go's pinned proxy
// and answers for both origins (site-a.test, site-b.test), recording what
// each request carried. Runs in the sim fence (HARVESTER_BROWSER=1).
func TestLiveBrowserCallerHeadersStayOnTheOrigin(t *testing.T) {
	if os.Getenv("HARVESTER_BROWSER") != "1" {
		t.Skip("named gap: HARVESTER_BROWSER is not 1 — the opt-in real-browser rung is disabled on this host")
	}
	root := os.Getenv("PFM_HARVEST_ROOT")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("named gap: no resolvable HOME (%v)", err)
		}
		root = filepath.Join(home, ".local", "state", "pfm", "harvest-python")
	}
	live := liveBrowserRuntime(root)
	if _, err := os.Stat(live.Python); err != nil {
		t.Skipf("named gap: browser environment not provisioned at %s (%v)", live.Python, err)
	}
	const probe = "sentinel-7f3a9c"
	var mu sync.Mutex
	probes := map[string][]string{} // host+path → the X-Probe of each request, in order
	dests := map[string][]string{}  // host+path → the Sec-Fetch-Dest of each request, for the failure message
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			http.Error(w, "no tunnels here", http.StatusMethodNotAllowed)
			return
		}
		mu.Lock()
		key := r.Host + r.URL.Path
		probes[key] = append(probes[key], r.Header.Get("X-Probe"))
		dests[key] = append(dests[key], r.Header.Get("Sec-Fetch-Dest"))
		mu.Unlock()
		switch r.Host + r.URL.Path {
		case "site-a.test/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body><h1>Headers</h1>` +
				`<img src="/same.png"><img src="http://site-b.test/cross.png"><p>probe page</p>` +
				`<script>new Image().src = "/rtc-" + typeof RTCPeerConnection + ".png"</script></body></html>`))
		default:
			w.Header().Set("Content-Type", "image/gif")
			_, _ = w.Write([]byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"))
		}
	}))
	defer proxy.Close()
	// The script is this build's embedded worker, so the run proves the code
	// under test, never a copy an earlier provisioning left behind.
	script := filepath.Join(t.TempDir(), "browser.py")
	if err := os.WriteFile(script, BrowserWorkerSource(), 0o600); err != nil {
		t.Fatal(err)
	}
	worker := NewBrowserWorker(Runtime{Python: live.Python, Script: script})
	defer func() { _ = worker.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	_, status, _, err := worker.FetchPinned(ctx, "http://site-a.test/", proxy.URL, "", "", "",
		map[string]string{"X-Probe": probe}, "http://site-a.test", true, false, 45000,
		func(string) error { return nil })
	if err != nil {
		t.Fatalf("browser fetch: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Logf("status %d, probes %v, dests %v", status, probes, dests)
	// rtc-undefined.png: the WebRTC block still held in the document the caller route took.
	for _, path := range []string{"site-a.test/", "site-a.test/same.png", "site-b.test/cross.png", "site-a.test/rtc-undefined.png"} {
		if len(probes[path]) == 0 {
			t.Fatalf("the browser never requested %s (probes %v)", path, probes)
		}
	}
	// Every request to the caller's origin carries the probe; none to another does.
	for path, got := range probes {
		want := ""
		if strings.HasPrefix(path, "site-a.test/") {
			want = probe
		}
		for i, value := range got {
			if value != want {
				t.Errorf("%s request %d (dest %q) carried X-Probe %q, want %q", path, i+1, dests[path][i], value, want)
			}
		}
	}
}
