package harvest

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"

	"hostops/pfm/internal/obs"
)

// httpOutRecords returns the recorder's http.out.request records and fails
// on any record that is not one, so a stray record cannot hide in the count.
func httpOutRecords(t *testing.T, recorder *obs.Recorder) []obs.Record {
	t.Helper()
	records := recorder.Records()
	for _, record := range records {
		if record.Message != "http.out.request" {
			t.Fatalf("unexpected record %s: %v", record.Message, record.Fields)
		}
		if got, _ := record.Field(obs.FieldComp); got != "http.out" {
			t.Fatalf("record comp = %v, want http.out: %v", got, record.Fields)
		}
	}
	return records
}

// recordingTripper is a custom transport a caller injected: it must still be
// the one dialing under the http.out wrapper.
type recordingTripper struct {
	calls int
	next  http.RoundTripper
}

func (tripper *recordingTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	tripper.calls++
	return tripper.next.RoundTrip(request)
}

// TestGatewayAttemptWritesAnHTTPOutRecordWithoutTouchingTheSharedClient pins
// harvest's http.out door (spec § Middleware): every rung's single attempt
// runs through gatewayRequestClient, whose per-request clone is wrapped —
// one record per attempt — while the shared client's Transport stays exactly
// what configureProxy, setUserAgent and IsPinnedClient inspect.
func TestGatewayAttemptWritesAnHTTPOutRecordWithoutTouchingTheSharedClient(t *testing.T) {
	_, recorder := obs.Test(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		if _, err := writer.Write([]byte(`{"results":[]}`)); err != nil {
			t.Errorf("write: %v", err)
		}
	}))
	defer server.Close()
	custom := &recordingTripper{next: http.DefaultTransport}
	shared := &http.Client{Transport: custom}
	response, err := gatewayAttempt(context.Background(), gatewayRequest{
		url: server.URL + "/search?q=GATEWAYSECRET&format=json", client: shared, ua: searchUA,
		max: 64 * 1024, trustedOrigin: true,
	})
	if err != nil || response.status != http.StatusOK {
		t.Fatalf("gatewayAttempt = %+v, %v", response, err)
	}
	if shared.Transport != http.RoundTripper(custom) {
		t.Fatalf("the shared client's Transport changed to %T", shared.Transport)
	}
	if custom.calls != 1 {
		t.Fatalf("the injected transport was called %d times, want 1 under the wrapper", custom.calls)
	}
	records := httpOutRecords(t, recorder)
	if len(records) != 1 {
		t.Fatalf("records = %d, want one per attempt: %s", len(records), recorder.Raw())
	}
	for key, want := range map[string]any{
		"op": "request", "method": http.MethodGet, "host": strings.TrimPrefix(server.URL, "http://"),
		"path": "/search", "status": float64(http.StatusOK), "bytes": float64(len(`{"results":[]}`)),
	} {
		if got, _ := records[0].Field(key); got != want {
			t.Fatalf("http.out record %s = %v, want %v: %v", key, got, want, records[0].Fields)
		}
	}
	if _, found := records[0].Field(obs.FieldDur); !found {
		t.Fatalf("http.out record carries no %s: %v", obs.FieldDur, records[0].Fields)
	}
	if strings.Contains(recorder.Raw(), "GATEWAYSECRET") {
		t.Fatalf("the query string reached the activity log: %s", recorder.Raw())
	}
}

// TestGatewayRequestClientWrapsTheUntrustedCloneAndKeepsItsJar covers the
// SSRF-guarded branch: the DOI gateway clone is wrapped, carries the rung's
// jar and the redirect re-validation, and the base client is untouched.
func TestGatewayRequestClientWrapsTheUntrustedCloneAndKeepsItsJar(t *testing.T) {
	_, recorder := obs.Test(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	base := &http.Client{}
	for _, shared := range []*http.Client{nil, base} {
		clone := gatewayRequestClient(gatewayRequest{client: shared, jar: jar})
		if clone.Jar != jar {
			t.Fatal("the gateway clone lost the rung's cookie jar")
		}
		if clone.CheckRedirect == nil || clone.CheckRedirect(mustRequest("http://10.0.0.1/"), nil) == nil {
			t.Fatal("the gateway clone lost its redirect re-validation")
		}
		response, err := clone.Get(server.URL + "/gateway/10.1000/x?token=GWSECRET")
		if err != nil {
			t.Fatal(err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if base.Transport != nil {
		t.Fatal("cloning wrapped the base client's Transport in place")
	}
	records := httpOutRecords(t, recorder)
	if len(records) != 2 {
		t.Fatalf("records = %d, want one per clone: %s", len(records), recorder.Raw())
	}
	for _, record := range records {
		for key, want := range map[string]any{"path": "/gateway/10.1000/x", "status": float64(http.StatusNoContent)} {
			if got, _ := record.Field(key); got != want {
				t.Fatalf("http.out record %s = %v, want %v: %v", key, got, want, record.Fields)
			}
		}
	}
	if strings.Contains(recorder.Raw(), "GWSECRET") {
		t.Fatalf("the query string reached the activity log: %s", recorder.Raw())
	}
}
