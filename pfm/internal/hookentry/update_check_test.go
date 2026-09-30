package hookentry

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/updatecheck"
)

func TestUpdateCheckFailureOutcomes(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	answered := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer answered.Close()
	for _, tc := range []struct {
		name, url  string
		unwritable bool
		wantCode   int
		wantWarn   bool
	}{
		{name: "offline", url: closedURL, wantWarn: true},
		{name: "marker unwritable", url: closedURL, unwritable: true, wantCode: 1},
		{name: "answered failure", url: answered.URL, wantCode: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, recorder := obs.Test(t)
			cache := filepath.Join(t.TempDir(), "update.json")
			if tc.unwritable {
				if err := os.Mkdir(cache+".failure", 0o700); err != nil {
					t.Fatal(err)
				}
			}
			var stderr bytes.Buffer
			code := UpdateCheck([]string{"--cache", cache, "--current", "v1.0.0", "--url", tc.url}, &stderr)
			if code != tc.wantCode || !strings.Contains(stderr.String(), "pfm internal update-check: ") {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
			if tc.unwritable && !strings.Contains(stderr.String(), "record update check failure") {
				t.Fatalf("marker failure absent: %q", stderr.String())
			}
			if tc.name == "answered failure" &&
				!strings.Contains(stderr.String(), "latest Professor release returned 200") {
				t.Fatalf("status absent: %q", stderr.String())
			}
			warn, outbound, errors := 0, 0, 0
			for _, record := range recorder.Records() {
				if record.Message == "update check: the latest Professor release is unreachable" {
					warn++
					if record.Level != "WARN" {
						t.Fatalf("unreachable level=%s", record.Level)
					}
					if got, found := record.Field("cache"); !found || got == nil {
						t.Fatalf("cache field missing: %v", record.Fields)
					}
					if got, _ := record.Field(obs.FieldErr); got == nil {
						t.Fatal("missing error field")
					}
				}
				if record.Message == "http.out.request" {
					outbound++
					if tc.wantWarn && record.Level != "WARN" {
						t.Fatalf("outbound level=%s", record.Level)
					}
				}
				if record.Level == "ERROR" {
					errors++
				}
			}
			if outbound != 1 || (warn == 1) != tc.wantWarn || (tc.wantWarn && errors != 0) {
				t.Fatalf("outbound=%d warn=%d errors=%d records=%s", outbound, warn, errors, recorder.Raw())
			}
			if tc.wantWarn {
				marker, found, err := updatecheck.ReadFailure(cache)
				if err != nil || !found || string(marker.Class) != "network" || marker.Reason == "" {
					t.Fatalf("marker=%+v found=%t err=%v", marker, found, err)
				}
				if _, err := os.Stat(cache); !os.IsNotExist(err) {
					t.Fatalf("cache exists: %v", err)
				}
			}
		})
	}
}

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
