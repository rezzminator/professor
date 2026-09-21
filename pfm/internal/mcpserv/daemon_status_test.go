package mcpserv

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
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
	status, err := ProbeDaemon(address)
	if err != nil || status.PID != 4242 {
		t.Fatalf("ProbeDaemon = %+v, %v; want the served document", status, err)
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

// TestProbeDaemonUnreachableIsAbsentAndAnErrorRecord: a refused probe reports
// ErrDaemonAbsent — nothing is listening, the one state in which binding the
// port is the right next move — and the log says the request failed rather
// than that the daemon answered nothing.
func TestProbeDaemonUnreachableIsAbsentAndAnErrorRecord(t *testing.T) {
	_, recorder := obs.Test(t)
	server := httptest.NewServer(http.NotFoundHandler())
	address := strings.TrimPrefix(server.URL, "http://")
	server.Close()
	_, err := ProbeDaemon(address)
	if err == nil {
		t.Fatal("a closed server probed as healthy")
	}
	if !errors.Is(err, ErrDaemonAbsent) {
		t.Fatalf("closed-port probe error = %v, want it to be ErrDaemonAbsent", err)
	}
	if !strings.Contains(err.Error(), address) {
		t.Fatalf("closed-port probe error = %v, want the dial failure's own cause", err)
	}
	records := recorder.Records()
	if len(records) != 1 || records[0].Level != "ERROR" {
		t.Fatalf("want one ERROR http.out record: %s", recorder.Raw())
	}
	if got, found := records[0].Field(obs.FieldErr); !found || got == "" {
		t.Fatalf("the failed probe's record names no error: %v", records[0].Fields)
	}
}

// TestProbeDaemonNamesAPortHeldBySomethingElse is the port-conflict case the
// single-instance gate turns on: a foreign service answering on pfm's port
// must never come back as "nothing is listening," because that answer sends
// `pfm mcp serve` on to bind a port that is already taken. Each way the
// answer can fail to be pfm's status document is named separately.
func TestProbeDaemonNamesAPortHeldBySomethingElse(t *testing.T) {
	for _, test := range []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{
			name: "html page on our port",
			handler: func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "text/html")
				if _, err := writer.Write([]byte("<html>somebody else's app</html>")); err != nil {
					t.Errorf("write body: %v", err)
				}
			},
			want: "not pfm's status document",
		},
		{
			name:    "an error status",
			handler: func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusTeapot) },
			want:    "HTTP 418",
		},
		{
			name: "a status document with no pid",
			handler: func(writer http.ResponseWriter, _ *http.Request) {
				if err := json.NewEncoder(writer).Encode(DaemonStatus{PFMVersion: "test"}); err != nil {
					t.Errorf("encode status: %v", err)
				}
			},
			want: "pid",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(test.handler)
			defer server.Close()
			_, err := ProbeDaemon(strings.TrimPrefix(server.URL, "http://"))
			if err == nil {
				t.Fatal("a foreign service probed as pfm's own daemon")
			}
			if errors.Is(err, ErrDaemonAbsent) {
				t.Fatalf("a service that ANSWERED reported as absent: %v", err)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("probe error = %v, want it to name %q", err, test.want)
			}
		})
	}
}
