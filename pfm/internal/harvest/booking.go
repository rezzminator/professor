package harvest

import (
	"net/url"
	"regexp"

	"golang.org/x/net/html"
)

// The Booking.com hotel review gap. A hotel page (www.booking.com/hotel/{cc}/
// {slug}.html) and its review page (/reviews/{cc}/hotel/{slug}.html, which
// redirects to the hotel page) answer every anonymous HTTP rung with an AWS
// WAF challenge (HTTP 202); a reader service's copy states the hotel's review
// count ("Guest reviews (1,389)") but shows only a few guests' highlighted
// excerpts. The review list the page loads is the site's own GraphQL API
// (www.booking.com/dml/graphql, reviewListFrontend), which answers an
// anonymous request "Request not supported" and needs the hotel's numeric id,
// which only the walled page carries. Nothing is loaded, so the extractor
// renders nothing: every such page falls through to the generic path as a
// rung served it, the review list named — the count the stored page states
// against none loaded, never dropped silently.

var bookingHotelPath = regexp.MustCompile(`^/(?:hotel/[a-z]{2}|reviews/[a-z]{2}/hotel)/[^/]+\.html$`)

// bookingStatedCount reads the review count a Booking hotel page states: its
// "Guest reviews (N)" tab, else its score's "·N reviews".
var bookingStatedCount = []*regexp.Regexp{
	regexp.MustCompile(`Guest reviews \((\d[0-9,.]*)\)`),
	regexp.MustCompile(`·\s*(\d[0-9,.]*) reviews?\b`),
}

// isBookingHotel accepts an address naming a hotel's page or review page.
func isBookingHotel(page *url.URL) bool {
	return bookingHotelPath.MatchString(page.Path)
}

// extractBookingReviews never renders: the hotel's review list is not loaded,
// so it names that gap and leaves the page to the generic path.
func extractBookingReviews(_ *html.Node, page *url.URL) (siteExtraction, bool) {
	if !isBookingHotel(page) {
		return siteExtraction{}, false
	}
	return siteExtraction{
		unrendered: "the hotel's review list was not loaded: Booking's review list API refuses an anonymous " +
			"request and its hotel pages serve a non-browser reader an AWS WAF challenge (only the guests' " +
			"highlighted excerpts the page shows are stored)",
		stated: bookingReviewsStated,
	}, false
}

// bookingReviewsStated is the stated · loaded clause of the review list gap,
// read from the content a rung stores.
func bookingReviewsStated(content string) string {
	for _, pattern := range bookingStatedCount {
		if match := pattern.FindStringSubmatch(content); match != nil {
			return "booking reviews: " + match[1] + " stated · 0 loaded"
		}
	}
	return "booking reviews: the review count the page states was not read · 0 loaded"
}
