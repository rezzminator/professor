package usagehook

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/obs"
)

// TestFetchWritesAnHTTPOutRecord proves options.Client is wrapped with
// obs.WrapClient (item 9), both when normalize constructs the default client
// and when a caller injects its own: a real Fetch round trip writes one
// http.out.request record, host and path only, never the bearer token.
func TestFetchWritesAnHTTPOutRecord(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer fixture-token" {
			t.Errorf("authorization header = %q", request.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(writer, `{
			"five_hour":{"utilization":10,"resets_at":"2030-01-01T10:00:00Z"},
			"seven_day":{"utilization":10,"resets_at":"2030-01-03T08:00:00Z"}
		}`)
	}))
	defer server.Close()

	root := t.TempDir()
	configDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(configDir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"fixture-token"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	_, recorder := obs.Test(t)
	if _, err := Fetch(context.Background(), Options{
		ConfigDir: configDir, Endpoint: server.URL + "?token=FETCHSECRET",
	}); err != nil {
		t.Fatalf("Fetch() error = %v", err)
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
	for key, want := range map[string]any{obs.FieldComp: "http.out", "method": http.MethodGet} {
		if got, _ := outbound.Field(key); got != want {
			t.Fatalf("http.out record %s = %v, want %v: %v", key, got, want, outbound.Fields)
		}
	}
	if strings.Contains(recorder.Raw(), "FETCHSECRET") || strings.Contains(recorder.Raw(), "fixture-token") {
		t.Fatalf("the token reached the activity log: %s", recorder.Raw())
	}
}
