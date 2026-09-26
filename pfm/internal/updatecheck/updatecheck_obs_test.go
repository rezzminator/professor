package updatecheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestCheckForUpdateWritesAnHTTPOutRecord proves the HEAD lookup's client is
// wrapped with obs.WrapClient (item 8): a real round trip through
// CheckForUpdate writes one http.out.request record, host and path only,
// never the query string. hookentry's own caller is proved separately by
// TestUpdateCheckWritesAnHTTPOutRecord in internal/hookentry/update_check_test.go.
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
