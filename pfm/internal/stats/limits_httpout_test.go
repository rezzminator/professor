package stats

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestLimitsSamplerClientsWriteHTTPOutRecords pins the two http.out doors of
// the limits sampler (spec § Middleware): the Claude usage client and the
// Codex usage client each leave one comp=http.out record per request, the
// Codex client keeps its no-redirect policy, and an injected client is wrapped
// in place rather than replaced.
func TestLimitsSamplerClientsWriteHTTPOutRecords(t *testing.T) {
	_, recorder := obs.Test(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	sampler := NewLimitsSampler(nil)
	for _, client := range []*http.Client{sampler.client(), sampler.codexClient()} {
		response, err := client.Get(server.URL + "/usage?key=LIMITSECRET")
		if err != nil {
			t.Fatal(err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
	records := recorder.Records()
	if len(records) != 2 {
		t.Fatalf("records = %d, want one http.out record per client: %s", len(records), recorder.Raw())
	}
	for _, record := range records {
		for key, want := range map[string]any{
			obs.FieldComp: "http.out", "op": "request", "path": "/usage", "status": float64(http.StatusNoContent),
		} {
			if got, _ := record.Field(key); got != want {
				t.Fatalf("http.out record %s = %v, want %v: %v", key, got, want, record.Fields)
			}
		}
	}
	if strings.Contains(recorder.Raw(), "LIMITSECRET") {
		t.Fatalf("the query string reached the activity log: %s", recorder.Raw())
	}
	if err := sampler.codexClient().CheckRedirect(nil, nil); err != http.ErrUseLastResponse {
		t.Fatalf("the Codex client's redirect policy changed: %v", err)
	}
	injected := &http.Client{}
	sampler.Client = injected
	if sampler.client() != injected {
		t.Fatal("an injected client was replaced instead of wrapped in place")
	}
	if _, wrapped := injected.Transport.(interface {
		RoundTrip(*http.Request) (*http.Response, error)
	}); !wrapped {
		t.Fatal("the injected client's transport was not wrapped")
	}
}
