package updatecheck

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

func TestUnreachable(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	url := closed.URL
	closed.Close()
	answered := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer answered.Close()
	untagged := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "/releases/latest")
		w.WriteHeader(http.StatusFound)
	}))
	defer untagged.Close()
	release := make(chan struct{})
	timeout := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-release
	}))
	t.Cleanup(func() {
		close(release)
		timeout.Close()
	})
	tls := httptest.NewTLSServer(http.NotFoundHandler())
	defer tls.Close()

	for _, tc := range []struct {
		name, url, current string
		unwritable, want   bool
		client             *http.Client
	}{
		{name: "transport", url: url, current: "v1.0.0", want: true},
		{
			name: "timeout", url: timeout.URL, current: "v1.0.0", want: true,
			client: &http.Client{Timeout: 50 * time.Millisecond},
		},
		{name: "TLS failure", url: tls.URL, current: "v1.0.0"},
		{name: "unsupported URL", url: "ftp://127.0.0.1/latest", current: "v1.0.0"},
		{name: "answered", url: answered.URL, current: "v1.0.0"},
		{name: "untagged", url: untagged.URL, current: "v1.0.0"},
		{name: "bad current", url: url, current: "broken"},
		{name: "marker write failed", url: url, current: "v1.0.0", unwritable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := filepath.Join(t.TempDir(), "update.json")
			if tc.unwritable {
				if err := os.Mkdir(cache+".failure", 0o700); err != nil {
					t.Fatal(err)
				}
			}
			err := CheckForUpdate(context.Background(), cache, tc.current, tc.url, tc.client)
			if tc.want || tc.name == "TLS failure" {
				marker, found, markerErr := ReadFailure(cache)
				if markerErr != nil || !found || marker.Class != failureNetwork || marker.Reason == "" {
					t.Fatalf("failure marker = %#v, %t, %v", marker, found, markerErr)
				}
			}
			if err == nil || Unreachable(err) != tc.want {
				t.Fatalf("err=%v unreachable=%t want=%t", err, Unreachable(err), tc.want)
			}
			if tc.want {
				var netErr interface{ Timeout() bool }
				if !errors.As(err, &netErr) {
					t.Fatalf("transport error not wrapped: %v", err)
				}
			}
		})
	}
	if Unreachable(nil) {
		t.Fatal("nil error is unreachable")
	}
}

// TestCheckForUpdateWritesAnHTTPOutRecord proves the HEAD lookup's client is
// wrapped with obs.WrapClient (item 8): a real round trip through
// CheckForUpdate writes one http.out.request record, host and path only,
// never the query string. hookentry's own caller is proved separately by
// TestUpdateCheckWritesAnHTTPOutRecord in internal/hookentry/update_check_test.go.
func TestCheckForUpdateOfflineRecordsWarn(t *testing.T) {
	ctx, recorder := obs.Test(t)
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()
	cache := filepath.Join(t.TempDir(), "update.json")
	if err := CheckForUpdate(ctx, cache, "v0.61.1", url, nil); err == nil {
		t.Fatal("wanted network failure")
	}
	if _, err := os.Stat(failurePath(cache)); err != nil {
		t.Fatalf("failure marker: %v", err)
	}
	records := 0
	for _, record := range recorder.Records() {
		if record.Message == "http.out.request" {
			records++
			if record.Level != "WARN" {
				t.Fatalf("level=%s: %s", record.Level, recorder.Raw())
			}
		}
	}
	if records == 0 {
		t.Fatalf("no http.out request: %s", recorder.Raw())
	}
}

func TestCheckForUpdateWritesAnHTTPOutRecord(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", "/mreza0100/professor/releases/tag/v0.61.2")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	_, recorder := obs.Test(t)
	cache := filepath.Join(t.TempDir(), "update.json")
	if err := CheckForUpdate(
		context.Background(), cache, "v0.61.1", server.URL+"?token=CHECKSECRET", server.Client(),
	); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	var outbound *obs.Record
	for _, record := range recorder.Records() {
		if record.Message == "http.out.request" {
			outbound = &record
		}
	}
	if outbound == nil {
		t.Fatalf("no http.out.request record: %s", recorder.Raw())
	}
	for key, want := range map[string]any{obs.FieldComp: "http.out", "method": http.MethodHead} {
		if got, _ := outbound.Field(key); got != want {
			t.Fatalf("http.out record %s = %v, want %v: %v", key, got, want, outbound.Fields)
		}
	}
	if strings.Contains(recorder.Raw(), "CHECKSECRET") {
		t.Fatalf("the query token reached the activity log: %s", recorder.Raw())
	}
}

// TestCheckForUpdateSucceedsWhenTheStaleFailureMarkerCannotBeCleared pins
// pfm-update-7#F1: a check whose lookup and cache write both succeeded is a
// success even when removing the stale failure marker fails — the removal
// failure is logged with the marker path and the error, never returned. The
// marker is a non-empty directory so os.Remove fails for root too (the fence).
func TestCheckForUpdateSucceedsWhenTheStaleFailureMarkerCannotBeCleared(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", "/mreza0100/professor/releases/tag/v0.61.2")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	ctx, recorder := obs.Test(t)
	cache := filepath.Join(t.TempDir(), "update.json")
	if err := os.MkdirAll(filepath.Join(failurePath(cache), "pinned"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CheckForUpdate(ctx, cache, "v0.61.1", server.URL, server.Client()); err != nil {
		t.Fatalf("CheckForUpdate() with an unremovable stale marker returned %v, want success", err)
	}
	notice, found, err := Read(cache, "v0.61.1")
	if err != nil || !found || notice.Latest != "v0.61.2" {
		t.Fatalf("Read() notice=%#v found=%t err=%v", notice, found, err)
	}
	raw := recorder.Raw()
	if !strings.Contains(raw, "failure marker") || !strings.Contains(raw, failurePath(cache)) {
		t.Fatalf("the unremovable marker was not logged with its path: %s", raw)
	}
}

// TestCheckForUpdateLeavesTheCallersClientUnchanged pins pfm-update-7#F3:
// the no-follow copy is wrapped, never the caller's client — its Transport
// and CheckRedirect stay exactly what the caller set.
func TestCheckForUpdateLeavesTheCallersClientUnchanged(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", "/mreza0100/professor/releases/tag/v0.61.2")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	client := server.Client()
	transport := client.Transport
	cache := filepath.Join(t.TempDir(), "update.json")
	if err := CheckForUpdate(context.Background(), cache, "v0.61.1", server.URL, client); err != nil {
		t.Fatalf("CheckForUpdate() error = %v", err)
	}
	if client.Transport != transport {
		t.Fatalf("CheckForUpdate replaced the caller's Transport: got %T, want %T", client.Transport, transport)
	}
	if client.CheckRedirect != nil {
		t.Fatal("CheckForUpdate set the caller's CheckRedirect")
	}
}
