package mcpserv

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

func shortenDaemonProbeTimeout(t *testing.T) {
	t.Helper()
	previous := DaemonProbeTimeoutOverride
	DaemonProbeTimeoutOverride = 100 * time.Millisecond
	t.Cleanup(func() { DaemonProbeTimeoutOverride = previous })
}

func TestProbeDaemonSilentListenerIsUnresponsive(t *testing.T) {
	shortenDaemonProbeTimeout(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := listener.Close(); closeErr != nil {
			t.Errorf("close silent listener: %v", closeErr)
		}
	})
	address := listener.Addr().String()
	started := time.Now()
	_, err = ProbeDaemon(address)
	if !errors.Is(err, ErrDaemonUnresponsive) || errors.Is(err, ErrDaemonAbsent) {
		t.Fatalf("silent-listener probe error = %v, want unresponsive outside absent chain", err)
	}
	if !strings.Contains(err.Error(), address) || !strings.Contains(err.Error(), "100ms") {
		t.Fatalf("silent-listener probe error = %v, want address and shortened deadline", err)
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("silent-listener probe error = %v, want wrapped transport timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("silent-listener probe took %s, want shortened deadline", elapsed)
	}
}

func TestProbeDaemonProductionDeadlineAllowsSlowHealthyAnswer(t *testing.T) {
	if daemonProbeTimeout != 2*time.Second {
		t.Fatalf("production probe deadline = %s, want 2s", daemonProbeTimeout)
	}
	previous := DaemonProbeTimeoutOverride
	DaemonProbeTimeoutOverride = 0
	t.Cleanup(func() { DaemonProbeTimeoutOverride = previous })
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(400 * time.Millisecond)
		if err := json.NewEncoder(writer).Encode(DaemonStatus{PID: 4242}); err != nil {
			t.Errorf("encode delayed status: %v", err)
		}
	}))
	t.Cleanup(server.Close)
	status, err := ProbeDaemon(strings.TrimPrefix(server.URL, "http://"))
	if err != nil || status.PID != 4242 {
		t.Fatalf("slow healthy probe = %+v, %v; want the status document", status, err)
	}
}

// TestProbeDaemonWritesAnHTTPOutRecord pins the http.out door in the daemon
// probe: its client is wrapped, so the loopback status read leaves one
// comp=http.out record with the probe's shape (spec § Middleware).
func TestProbeDaemonWritesAnHTTPOutRecord(t *testing.T) {
	// obs.Test replaces the process logger, which other tests also use.
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

func TestProbeDaemonPreservesOpaqueChatRuntimeIdentity(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(NewDaemonHandler(DaemonOptions{
		Version: "test", Chat: http.NotFoundHandler(), ChatRuntimeIdentity: "sha256:opaque",
	}))
	defer server.Close()
	status, err := ProbeDaemon(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	if status.ChatRuntimeIdentity != "sha256:opaque" {
		t.Fatalf("chat runtime identity = %q, want opaque digest", status.ChatRuntimeIdentity)
	}
}

func TestDaemonStatusOmitsChatRuntimeIdentityWithoutChat(t *testing.T) {
	t.Parallel()
	handler := NewDaemonHandler(DaemonOptions{ChatRuntimeIdentity: "sha256:must-not-leak"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/status", http.NoBody))
	if strings.Contains(response.Body.String(), "chatRuntimeIdentity") {
		t.Fatalf("status without mounted chat exposed identity: %s", response.Body.String())
	}
}

// TestProbeDaemonUnreachableIsAbsentAndAWarnRecord: a refused probe reports
// ErrDaemonAbsent — nothing is listening, the one state in which binding the
// port is the right next move — and the log says the request failed rather
// than that the daemon answered nothing.
func TestProbeDaemonUnreachableIsAbsentAndAWarnRecord(t *testing.T) {
	// obs.Test replaces the process logger, which other tests also use.
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
	if errors.Is(err, ErrDaemonUnresponsive) {
		t.Fatalf("closed-port probe error = %v, must not be unresponsive", err)
	}
	if !strings.Contains(err.Error(), "no service answered at "+address+":") {
		t.Fatalf("closed-port probe error = %v, want original absent message", err)
	}
	if !strings.Contains(err.Error(), address) {
		t.Fatalf("closed-port probe error = %v, want the dial failure's own cause", err)
	}
	records := recorder.Records()
	if len(records) != 1 || records[0].Level != "WARN" {
		t.Fatalf("want one WARN http.out record: %s", recorder.Raw())
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
	t.Parallel()
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
