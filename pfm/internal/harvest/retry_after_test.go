package harvest

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRateLimitNamesTheServersRetryAfter: a 429 names the Retry-After the
// server gave — seconds or a date — in the error text and as RetryAfter, on
// the core result and the public failure alike; absent when it gave none.
func TestRateLimitNamesTheServersRetryAfter(t *testing.T) {
	cases := map[string]string{
		"120":                           "120 s",
		"Wed, 21 Oct 2026 07:28:00 GMT": "Wed, 21 Oct 2026 07:28:00 UTC",
		"":                              "",
		"in a while":                    "",
	}
	for header, want := range cases {
		ctx, note := withRetryAfterNote(context.Background())
		request := httptest.NewRequest(http.MethodGet, "https://slow.example/page", http.NoBody).WithContext(ctx)
		response := &http.Response{
			StatusCode: http.StatusTooManyRequests, Header: http.Header{}, Request: request,
			Body: io.NopCloser(strings.NewReader("slow down")),
		}
		if header != "" {
			response.Header.Set("Retry-After", header)
		}
		_, closeBody, err := decodedResponseBody(response)
		if err != nil {
			t.Fatalf("decodedResponseBody: %v", err)
		}
		_ = closeBody()
		got := note.apply(Result{
			Source: "https://slow.example/page", HTTPStatus: http.StatusTooManyRequests,
			ErrorKind: errorKindRateLimited, Error: "rate limited",
		})
		public := PublicFailure(got.Source, got)
		for surface, result := range map[string]Result{"core": got, "public": public} {
			if result.RetryAfter != want {
				t.Errorf("Retry-After %q: %s retry_after = %q, want %q", header, surface, result.RetryAfter, want)
			}
			if named := strings.Contains(result.Error, "retry after "+want); want != "" && !named {
				t.Errorf(
					"Retry-After %q: %s error %q does not name \"retry after %s\"",
					header,
					surface,
					result.Error,
					want,
				)
			}
			if want == "" && strings.Contains(result.Error, "retry after") {
				t.Errorf("no Retry-After: %s error %q names a wait the server never gave", surface, result.Error)
			}
		}
	}
}
