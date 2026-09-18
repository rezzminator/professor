package updatecheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/obs"
)

// TestCheckForUpdateWritesAnHTTPOutRecord proves the HEAD lookup's client is
// wrapped with obs.WrapClient (item 8): a real round trip through
// CheckForUpdate writes one http.out.request record, host and path only,
// never the query string. hookentry's own caller is proved separately by
// TestUpdateCheckWritesAnHTTPOutRecord in internal/hookentry/update_check_test.go.
func TestCheckForUpdateWritesAnHTTPOutRecord(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
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
