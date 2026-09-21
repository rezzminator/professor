package harvest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestSearXNGSearchWritesAnHTTPOutRecordAndStillRefusesRedirects pins the
// SearXNG rung's http.out door (spec § Middleware): the pinned-origin client's
// search attempt is recorded through the gateway, and its refuse-every-redirect
// policy is intact.
func TestSearXNGSearchWritesAnHTTPOutRecordAndStillRefusesRedirects(t *testing.T) {
	_, recorder := obs.Test(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if _, err := writer.Write([]byte(`{"results":[]}`)); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	defer server.Close()
	client, err := searxngClient(server.URL, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	results, status, err := searchSearXNG(context.Background(), "SEARCHSECRET", SearchOptions{
		SearXNGURL: server.URL, SearXNG: client,
	})
	if err != nil || status != http.StatusOK || len(results) != 0 {
		t.Fatalf("searchSearXNG = %v, %d, %v", results, status, err)
	}
	if client.CheckRedirect(mustRequest("http://elsewhere.example.test/"), nil) == nil {
		t.Fatal("the SearXNG client followed a redirect under the wrapper")
	}
	records := httpOutRecords(t, recorder)
	if len(records) != 1 {
		t.Fatalf("records = %d, want one: %s", len(records), recorder.Raw())
	}
	for key, want := range map[string]any{
		"host": strings.TrimPrefix(server.URL, "http://"), "path": "/search", "status": float64(http.StatusOK),
		"bytes": float64(len(`{"results":[]}`)),
	} {
		if got, _ := records[0].Field(key); got != want {
			t.Fatalf("http.out record %s = %v, want %v: %v", key, got, want, records[0].Fields)
		}
	}
	if strings.Contains(recorder.Raw(), "SEARCHSECRET") {
		t.Fatalf("the query reached the activity log: %s", recorder.Raw())
	}
}

// TestProbeSearchWithoutAClientWritesAnHTTPOutRecord pins the probe's own
// constructed client: the healthz request is recorded.
func TestProbeSearchWithoutAClientWritesAnHTTPOutRecord(t *testing.T) {
	_, recorder := obs.Test(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/healthz" {
			http.NotFound(writer, request)
			return
		}
		if _, err := writer.Write([]byte("OK")); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	defer server.Close()
	probe := ProbeSearch(context.Background(), SearchOptions{SearXNGURL: server.URL}, nil)
	if probe.State == SearchProbeOff {
		t.Fatalf("probe = %+v, want a reachable SearXNG", probe)
	}
	records := httpOutRecords(t, recorder)
	if len(records) != 1 {
		t.Fatalf("records = %d, want one: %s", len(records), recorder.Raw())
	}
	for key, want := range map[string]any{"path": "/healthz", "status": float64(http.StatusOK)} {
		if got, _ := records[0].Field(key); got != want {
			t.Fatalf("http.out record %s = %v, want %v: %v", key, got, want, records[0].Fields)
		}
	}
}
