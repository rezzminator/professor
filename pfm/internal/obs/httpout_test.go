package obs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeTripper is a RoundTripper the tests script: it answers with response
// or err and remembers the request it saw.
type fakeTripper struct {
	response *http.Response
	err      error
	seen     *http.Request
}

func (fake *fakeTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	fake.seen = request
	return fake.response, fake.err
}

// onlyRecord fails unless the recorder holds exactly one record and returns it.
func onlyRecord(t *testing.T, recorder *Recorder) Record {
	t.Helper()
	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1: %s", len(records), recorder.Raw())
	}
	return records[0]
}

// wantField fails unless record carries key with exactly want.
func wantField(t *testing.T, record Record, key string, want any) {
	t.Helper()
	got, found := record.Field(key)
	if !found {
		t.Fatalf("record has no %q: %v", key, record.Fields)
	}
	if got != want {
		t.Fatalf("record %s = %v (%T), want %v (%T)", key, got, got, want, want)
	}
}

func TestRoundTripperRecordsTheShapeAndNeverTheQueryHeadersOrBody(t *testing.T) {
	ctx, recorder := Test(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if _, err := io.Copy(io.Discard, request.Body); err != nil {
			t.Errorf("drain body: %v", err)
		}
		writer.WriteHeader(http.StatusCreated)
		if _, err := io.WriteString(writer, "twelve bytes"); err != nil {
			t.Errorf("write body: %v", err)
		}
	}))
	defer server.Close()
	client := &http.Client{Transport: RoundTripper(nil)}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, server.URL+"/things/1?api_key=QUERYSECRET42", strings.NewReader("sk-BODYSECRET"),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer HEADERSECRET7")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}

	record := onlyRecord(t, recorder)
	if record.Message != "http.out.request" || record.Level != slog.LevelInfo.String() {
		t.Fatalf("record = %s %s, want INFO http.out.request", record.Level, record.Message)
	}
	wantField(t, record, FieldComp, "http.out")
	wantField(t, record, "op", "request")
	wantField(t, record, "method", http.MethodPost)
	wantField(t, record, "host", strings.TrimPrefix(server.URL, "http://"))
	wantField(t, record, "path", "/things/1")
	wantField(t, record, "status", float64(http.StatusCreated))
	wantField(t, record, "bytes", float64(len("twelve bytes")))
	if _, found := record.Field(FieldDur); !found {
		t.Fatalf("record carries no %s: %v", FieldDur, record.Fields)
	}
	if _, found := record.Field("retries"); found {
		t.Fatalf("a first attempt carries retries: %v", record.Fields)
	}
	for _, secret := range []string{"QUERYSECRET42", "HEADERSECRET7", "BODYSECRET", "api_key", "Authorization"} {
		if strings.Contains(recorder.Raw(), secret) {
			t.Fatalf("%q reached the activity log: %s", secret, recorder.Raw())
		}
	}
}

func TestRoundTripperRecordsATransportErrorWithoutTheURL(t *testing.T) {
	ctx, recorder := Test(t)
	fake := &fakeTripper{err: &url.Error{
		Op: "Get", URL: "http://api.example.test/v1?token=URLSECRET", Err: errors.New("connection refused"),
	}}
	client := &http.Client{Transport: RoundTripper(fake)}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodGet, "http://api.example.test/v1?token=URLSECRET", http.NoBody,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(request); err == nil {
		t.Fatal("the wrapped error vanished")
	}
	if fake.seen == nil {
		t.Fatal("the wrapper never called the next RoundTripper")
	}
	record := onlyRecord(t, recorder)
	if record.Level != slog.LevelError.String() {
		t.Fatalf("a failed round trip logged at %s, want ERROR", record.Level)
	}
	wantField(t, record, FieldErr, "Get: connection refused")
	wantField(t, record, "host", "api.example.test")
	if _, found := record.Field("status"); found {
		t.Fatalf("a failed round trip carries a status: %v", record.Fields)
	}
	if strings.Contains(recorder.Raw(), "URLSECRET") {
		t.Fatalf("the URL's query reached the activity log: %s", recorder.Raw())
	}
}

func TestRoundTripperCapsALongErrorAtTheFieldLimit(t *testing.T) {
	ctx, recorder := Test(t)
	long := strings.Repeat("x", MaxValueBytes+952)
	client := &http.Client{Transport: RoundTripper(&fakeTripper{err: errors.New(long)})}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://cap.example.test/", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Do(request); err == nil {
		t.Fatal("the wrapped error vanished")
	}
	got, _ := onlyRecord(t, recorder).Field(FieldErr)
	text, _ := got.(string)
	if !strings.HasSuffix(text, "…(truncated 952 bytes)") || len(text) > MaxValueBytes+len("…(truncated 952 bytes)") {
		t.Fatalf("err was not cut at the cap: len=%d tail=%q", len(text), text[max(0, len(text)-40):])
	}
}

func TestRoundTripperWarnsOnAClientErrorStatusAndRecordsTheRetryOrdinal(t *testing.T) {
	ctx, recorder := Test(t)
	fake := &fakeTripper{response: &http.Response{
		StatusCode: http.StatusNotFound, ContentLength: -1, Body: http.NoBody, Header: http.Header{},
	}}
	client := &http.Client{Transport: RoundTripper(fake)}
	request, err := http.NewRequestWithContext(
		Retry(ctx, 2), http.MethodGet, "http://warn.example.test/missing", http.NoBody,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	record := onlyRecord(t, recorder)
	if record.Level != slog.LevelWarn.String() {
		t.Fatalf("a 404 logged at %s, want WARN", record.Level)
	}
	wantField(t, record, "status", float64(http.StatusNotFound))
	wantField(t, record, "bytes", float64(-1))
	wantField(t, record, "retries", float64(2))
}

func TestWrapClientKeepsEveryPolicyByIdentityAndWrapsOnce(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	redirect := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	custom := &fakeTripper{}
	client := &http.Client{Transport: custom, CheckRedirect: redirect, Jar: jar, Timeout: 7 * time.Second}

	wrapped := WrapClient(client)
	if wrapped != client {
		t.Fatal("WrapClient returned a different client: the caller's policy object was replaced")
	}
	if reflect.ValueOf(client.CheckRedirect).Pointer() != reflect.ValueOf(redirect).Pointer() {
		t.Fatal("CheckRedirect changed identity")
	}
	if client.Jar != jar {
		t.Fatal("Jar changed identity")
	}
	if client.Timeout != 7*time.Second {
		t.Fatalf("Timeout = %s, want 7s", client.Timeout)
	}
	tripper, ok := client.Transport.(*outboundTripper)
	if !ok {
		t.Fatalf("Transport = %T, want the http.out wrapper", client.Transport)
	}
	if tripper.next != http.RoundTripper(custom) {
		t.Fatal("the custom transport is not the one the wrapper calls")
	}
	if WrapClient(client).Transport != http.RoundTripper(tripper) {
		t.Fatal("a second WrapClient wrapped the wrapper: every request would log twice")
	}
	if RoundTripper(tripper) != http.RoundTripper(tripper) {
		t.Fatal("RoundTripper re-wrapped an already wrapped transport")
	}
	if fresh := WrapClient(nil); fresh == nil || fresh.Transport == nil {
		t.Fatal("WrapClient(nil) did not hand back a usable wrapped client")
	}
}

func TestRoundTripperWithNilNextUsesTheDefaultTransport(t *testing.T) {
	ctx, _ := Test(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	response, err := RoundTripper(nil).RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 through the default transport", response.StatusCode)
	}
}

// TestRoundTripperBeforeAnyLogIsOpenStaysSilent pins the unit-test posture:
// a package test that never opened a log gets a working client, not a panic.
func TestRoundTripperBeforeAnyLogIsOpenStaysSilent(t *testing.T) {
	fake := &fakeTripper{response: &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Header: http.Header{}}}
	request, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, "http://quiet.example.test/", http.NoBody,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := RoundTripper(fake).RoundTrip(request)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("round trip = %v, %v", response, err)
	}
}
