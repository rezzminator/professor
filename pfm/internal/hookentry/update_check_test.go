package hookentry

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestUpdateCheckWritesAnHTTPOutRecord pins the http.out door of the
// update-check hook (spec § Middleware): the HEAD it sends for the latest
// release leaves one comp=http.out record with the redirect status, and the
// release cache it writes is unaffected by the wrapper.
func TestUpdateCheckWritesAnHTTPOutRecord(t *testing.T) {
	_, recorder := obs.Test(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodHead || request.URL.Path != "/professor-org/professor/releases/latest" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Location", "/professor-org/professor/releases/tag/v9.9.9")
		writer.WriteHeader(http.StatusFound)
	}))
	defer server.Close()
	cache := filepath.Join(t.TempDir(), "update.json")
	var stderr bytes.Buffer
	code := UpdateCheck([]string{
		"--cache", cache, "--current", "v1.0.0", "--url", server.URL + "/professor-org/professor/releases/latest",
	}, &stderr)
	if code != 0 {
		t.Fatalf("update-check exit = %d, stderr=%s", code, stderr.String())
	}
	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want one http.out record: %s", len(records), recorder.Raw())
	}
	for key, want := range map[string]any{
		obs.FieldComp: "http.out", "op": "request", "method": http.MethodHead,
		"host": strings.TrimPrefix(server.URL, "http://"), "path": "/professor-org/professor/releases/latest",
		"status": float64(http.StatusFound),
	} {
		if got, _ := records[0].Field(key); got != want {
			t.Fatalf("http.out record %s = %v, want %v: %v", key, got, want, records[0].Fields)
		}
	}
}
