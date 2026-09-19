package harvest

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestSafeURLStripsQueryAndUserinfo(t *testing.T) {
	cases := map[string]string{
		"https://api.example.test/v1/volumes?q=numpy&key=SENTINEL-KEY-1234": "https://api.example.test/v1/volumes",
		"http://user:pass@internal.example.test/path?token=abc":             "http://internal.example.test/path",
		"https://example.test/a/b#frag":                                     "https://example.test/a/b",
		"not a url at all":                                                  "<invalid-url>",
	}
	for raw, want := range cases {
		if got := safeURL(raw); got != want {
			t.Fatalf("safeURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestSafeURLNeverContainsSentinelKey(t *testing.T) {
	const sentinel = "SENTINEL-KEY-1234"
	raw := "https://www.googleapis.com/books/v1/volumes?q=numpy&country=US&key=" + sentinel
	if got := safeURL(raw); strings.Contains(got, sentinel) {
		t.Fatalf("safeURL(%q) = %q, still carries the sentinel key", raw, got)
	}
}

func TestSanitizeTransportErrorStripsURLErrorButKeepsCause(t *testing.T) {
	const sentinel = "SENTINEL-KEY-1234"
	raw := "https://api.example.test/lookup?key=" + sentinel
	inner := errors.New("connection refused")
	urlErr := &url.Error{Op: "Get", URL: raw, Err: inner}
	sanitized := sanitizeTransportError(urlErr, raw)
	if sanitized == nil {
		t.Fatal("sanitizeTransportError returned nil for a non-nil error")
	}
	if strings.Contains(sanitized.Error(), sentinel) {
		t.Fatalf("sanitizeTransportError(%v) = %q, still carries the sentinel key", urlErr, sanitized.Error())
	}
	if !errors.Is(sanitized, inner) {
		t.Fatalf("sanitizeTransportError(%v) lost the wrapped cause: %v", urlErr, sanitized)
	}
}

func TestSanitizeTransportErrorLeavesNonURLErrorUntouched(t *testing.T) {
	plain := errors.New("some other failure")
	if got := sanitizeTransportError(plain, "https://example.test/?key=x"); got != plain {
		t.Fatalf("sanitizeTransportError changed a non-*url.Error: got %v, want the same error back", got)
	}
	if sanitizeTransportError(nil, "https://example.test/") != nil {
		t.Fatal("sanitizeTransportError(nil, ...) != nil")
	}
}
