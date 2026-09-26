package harvest

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
)

// movingOrigin answers every address in hops with status and a Location of
// its value, and any other address with the article page.
func movingOrigin(status int, hops map[string]string) roundTripFunc {
	return func(request *http.Request) (*http.Response, error) {
		if next, ok := hops[request.URL.String()]; ok {
			answer := response(request, status, "text/html", "")
			answer.Header.Set("Location", next)
			return answer, nil
		}
		return response(request, http.StatusOK, "text/html; charset=utf-8",
			"<html><body><article>"+landingArticle+"</article></body></html>"), nil
	}
}

// TestSameHostPermanentMoveIsTheMovedPage: a same-host 301/308 whose landing
// keeps the requested page's last path segment is the page, moved — stored with
// no partial flag and a note naming where it moved. A permanent redirect to
// another segment, or a temporary one, stays a named "different page" partial
// with no moved note.
func TestSameHostPermanentMoveIsTheMovedPage(t *testing.T) {
	const (
		requested = "https://docs.example.org/3/library/functions.html"
		moved     = "https://docs.example.org/3/builtins/functions.html"
		other     = "https://docs.example.org/3/builtins/constants.html"
	)
	different := " — a different page (the requested page may no longer exist)"
	note := movedNotePrefix + requested + " moved permanently to " + moved
	for _, tc := range []struct {
		name    string
		status  int
		landing string
		partial string // "" when the page stores clean
		note    string // "" when the stored page carries no moved note
	}{
		{"301 to the moved page", http.StatusMovedPermanently, moved, "", note},
		{"308 to the moved page", http.StatusPermanentRedirect, moved, "", note},
		{
			"301 to another page", http.StatusMovedPermanently, other,
			"the site redirected " + requested + " to " + other + different, "",
		},
		{
			"302 to the same name", http.StatusFound, moved,
			"the site redirected " + requested + " to " + moved + different, "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := landingHarvester(t, movingOrigin(tc.status, map[string]string{requested: tc.landing}), nil)
			result := h.FetchWithOptions(context.Background(), requested, FetchOptions{Refresh: true})
			if result.Error != "" {
				t.Fatalf("the redirect failed: %q", result.Error)
			}
			if tc.partial == "" && result.Partial != "" {
				t.Fatalf("a permanent move to the same page is flagged partial: %q", result.Partial)
			}
			if tc.partial != "" && !strings.Contains(result.Partial, tc.partial) {
				t.Fatalf("partial %q, want it to name %q", result.Partial, tc.partial)
			}
			stored, err := os.ReadFile(result.Path)
			if err != nil {
				t.Fatalf("the stored artifact %q could not be read: %v", result.Path, err)
			}
			if tc.note != "" && !strings.Contains(string(stored), tc.note+"\n") {
				t.Fatalf("the stored artifact does not note the move %q:\n%.400s", tc.note, stored)
			}
			if tc.note == "" && strings.Contains(string(stored), "moved permanently") {
				t.Fatalf("a redirect that is no move is noted as one:\n%.400s", stored)
			}
			if !strings.Contains(string(stored), landingArticle) {
				t.Fatalf("the stored artifact lost the page:\n%.400s", stored)
			}
		})
	}
}
