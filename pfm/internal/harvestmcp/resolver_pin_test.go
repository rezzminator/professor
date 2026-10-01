package harvestmcp

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestResolverClientRefusesALoopbackTarget proves the resolver client and its
// pinned base transport reject private destinations before a request arrives.
func TestResolverClientRefusesALoopbackTarget(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := newHTTPClient(Runtime{UserAgent: "ua-x"})
	if err != nil {
		t.Fatal(err)
	}
	outer, ok := client.Transport.(userAgentTransport)
	if !ok {
		t.Fatalf("client.Transport = %T, want the MCP User-Agent wrapper", client.Transport)
	}
	if outer.value != "ua-x" {
		t.Fatalf("outer User-Agent = %q, want ua-x", outer.value)
	}
	inner := &http.Client{Transport: outer.base}
	for _, test := range []struct {
		name   string
		client *http.Client
	}{
		{name: "MCP wrapper", client: client},
		{name: "pinned base", client: inner},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, err := test.client.Get(server.URL)
			if response != nil {
				_ = response.Body.Close()
				t.Fatalf(
					"loopback request returned response %v; MCP resolver client must dial only through harvest's pinned, SSRF-checked transport",
					response.Status,
				)
			}
			if err == nil {
				t.Fatal(
					"loopback request returned no error; MCP resolver client must dial only through harvest's pinned, SSRF-checked transport",
				)
			}
			if got := requests.Load(); got != 0 {
				t.Fatalf(
					"loopback server received %d requests; MCP resolver client must dial only through harvest's pinned, SSRF-checked transport",
					got,
				)
			}
		})
	}
}
