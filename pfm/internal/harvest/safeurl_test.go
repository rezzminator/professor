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

// D1 (hands-on stress test): a URL the caller wrote with credentials in it was
// echoed whole into the MCP answer and into daemon stderr.
func TestPublicSourceLabelDropsUserinfoAndKeepsWhatTellsSourcesApart(t *testing.T) {
	for _, row := range []struct{ name, raw, want string }{
		{"userinfo", "https://USER:PASSWORD@example.com/a?q=1", "https://example.com/a?q=1"},
		{"plain url", "https://example.com/a?q=1", "https://example.com/a?q=1"},
		{"doi", "10.1000/xyz123", "10.1000/xyz123"},
		{"title", "A title: with a colon", "A title: with a colon"},
		{"unparseable userinfo", "https://USER:PASS WORD@exa mple.com/", "<invalid-url>"},
	} {
		if got := PublicSourceLabel(row.raw); got != row.want {
			t.Errorf("%s: PublicSourceLabel(%q) = %q, want %q", row.name, row.raw, got, row.want)
		}
	}
}

func TestPublicFailureNeverEchoesURLCredentialsAndNamesAPolicyRefusal(t *testing.T) {
	const source = "https://USER:PASSWORD@example.com/?token=SECRET"
	out := PublicFailure(source, Result{Source: source, Error: "URL userinfo is not allowed"})
	if strings.Contains(out.Source, "PASSWORD") || strings.Contains(out.Source, "USER") {
		t.Fatalf("public failure echoed the URL credentials: %q", out.Source)
	}
	for _, refusal := range []string{
		"URL userinfo is not allowed",
		"refusing private/internal host 169.254.169.254",
		`Member name contains '..': "../escape.txt"`,
		`Member name is absolute path: "/etc/passwd"`,
	} {
		got := PublicFailure("x", Result{Error: refusal})
		if got.ErrorKind != errorKindRefused {
			t.Errorf("%q classed %q, want %q — a policy refusal must never read as a retryable failure",
				refusal, got.ErrorKind, errorKindRefused)
		}
	}
}
