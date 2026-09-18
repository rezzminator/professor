package mcpserv

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hostops/pfm/internal/obs"
)

// TestProbeDaemonWritesAnHTTPOutRecord pins the http.out door in the daemon
// probe: its client is wrapped, so the loopback status read leaves one
// comp=http.out record with the probe's shape (spec § Middleware).
func TestProbeDaemonWritesAnHTTPOutRecord(t *testing.T) {
	_, recorder := obs.Test(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/status" {
			http.NotFound(writer, request)
			return
		}
		if err := json.NewEncoder(writer).Encode(DaemonStatus{PFMVersion: "test", PID: 4242}); err != nil {
			t.Errorf("encode status: %v", err)
		}
	}))
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	status, ok := ProbeDaemon(address)
	if !ok || status.PID != 4242 {
		t.Fatalf("ProbeDaemon = %+v, %t; want the served document", status, ok)
	}
	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want exactly one http.out record: %s", len(records), recorder.Raw())
	}
	for key, want := range map[string]any{
		obs.FieldComp: "http.out", "op": "request", "method": http.MethodGet, "host": address, "path": "/status",
		"status": float64(http.StatusOK),
	} {
		if got, _ := records[0].Field(key); got != want {
			t.Fatalf("http.out record %s = %v, want %v: %v", key, got, want, records[0].Fields)
		}
	}
	if _, found := records[0].Field(obs.FieldDur); !found {
		t.Fatalf("http.out record carries no %s: %v", obs.FieldDur, records[0].Fields)
	}
}

// TestProbeDaemonUnreachableIsFalseAndAnErrorRecord: a refused probe stays a
// plain false to the caller, and the log says the request failed rather than
// that the daemon answered nothing.
func TestProbeDaemonUnreachableIsFalseAndAnErrorRecord(t *testing.T) {
	_, recorder := obs.Test(t)
	server := httptest.NewServer(http.NotFoundHandler())
	address := strings.TrimPrefix(server.URL, "http://")
	server.Close()
	if _, ok := ProbeDaemon(address); ok {
		t.Fatal("a closed server probed as healthy")
	}
	records := recorder.Records()
	if len(records) != 1 || records[0].Level != "ERROR" {
		t.Fatalf("want one ERROR http.out record: %s", recorder.Raw())
	}
	if got, found := records[0].Field(obs.FieldErr); !found || got == "" {
		t.Fatalf("the failed probe's record names no error: %v", records[0].Fields)
	}
}
