package harvest

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// The fixtures are captured from www.booking.com: the page every anonymous
// HTTP rung is served in a hotel page's place (testdata/booking/challenge.html,
// the AWS WAF challenge, HTTP 202), and a reader service's copy of the hotel
// page (testdata/booking/reader.txt), trimmed and scrubbed: the hotel renamed,
// the guests' names placeholders. The copy states the hotel's review count
// ("Guest reviews (1,389)", "Excellent·1,389 reviews") and shows only a few
// guests' highlighted excerpts; the review list itself never loads.
const bookingPageHost = "www.booking.com"

func bookingHarvester(t *testing.T, reader string) *Harvester {
	t.Helper()
	challenge := socialFixture(t, "booking/challenge.html")
	h := (&socialSite{}).harvester(t)
	h.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Host == bookingPageHost {
			return response(request, http.StatusAccepted, "text/html", challenge), nil
		}
		return response(request, http.StatusNotFound, "text/html", "<html><body>not found</body></html>"), nil
	})}
	h.chrome = h.client
	h.options.Client, h.options.Chrome = h.client, h.client
	h.jina = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return response(request, http.StatusOK, "text/plain; charset=utf-8", reader), nil
	})}
	return h
}

// TestBookingReviewsUnreadNamedWithStatedCount: a Booking hotel page (or its
// review page), walled on every anonymous HTTP rung, is stored as the reader
// service served it, and its review list — never loaded, Booking's review API
// refusing an anonymous request — is a named gap carrying the count the page
// states against the none loaded; with no count in the stored page the gap
// says the count was not read. Another Booking page is not named.
func TestBookingReviewsUnreadNamedWithStatedCount(t *testing.T) {
	reader := socialFixture(t, "booking/reader.txt")
	uncounted := strings.NewReplacer("Guest reviews (1,389)", "Guest reviews",
		"Excellent·1,389 reviews", "Excellent").Replace(reader)
	const (
		hotel   = "https://www.booking.com/hotel/fr/hotelexamplebosquet.html"
		stated  = "booking reviews: 1,389 stated · 0 loaded"
		unknown = "booking reviews: the review count the page states was not read · 0 loaded"
	)
	for _, test := range []struct {
		name, target, reader, want string
	}{
		{"hotel page", hotel, reader, stated},
		{"review page", "https://www.booking.com/reviews/fr/hotel/hotelexamplebosquet.html", reader, stated},
		{"localized hotel page", "https://www.booking.com/hotel/fr/hotelexamplebosquet.en-gb.html", reader, stated},
		{"no stated count", hotel, uncounted, unknown},
		{"city page", "https://www.booking.com/city/fr/paris.html", reader, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := bookingHarvester(t, test.reader)
			result := h.FetchWithOptions(context.Background(), test.target, FetchOptions{Refresh: true})
			if result.Error != "" || !strings.Contains(result.Content, "Guest-A") {
				t.Fatalf("the page did not fall through to the reader's copy: error=%q\n%.1500s", result.Error,
					result.Content)
			}
			if test.want == "" {
				if strings.Contains(result.Partial, "booking reviews") {
					t.Fatalf("a page with no review list is named: partial=%q", result.Partial)
				}
				return
			}
			if !strings.Contains(result.Partial, test.want) ||
				!strings.Contains(result.Partial, "the hotel's review list was not loaded") {
				t.Fatalf("the unread review list is not named with its stated count: partial=%q", result.Partial)
			}
			again := h.FetchWithOptions(context.Background(), test.target, FetchOptions{Refresh: true})
			if again.Content != result.Content || again.Partial != result.Partial {
				t.Fatalf("a second harvest differs:\nfirst partial=%q\nagain partial=%q", result.Partial, again.Partial)
			}
		})
	}
}
