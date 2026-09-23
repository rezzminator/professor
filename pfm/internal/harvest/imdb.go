package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The IMDb user-review extractor. A title's review page (www.imdb.com/title/
// {tt id}/reviews/) answers every anonymous rung with an AWS WAF challenge
// (HTTP 202, an empty body, x-amzn-waf-action: challenge), and a reader
// service's copy holds only its few featured reviews. The review list the
// page shows is the site's own GraphQL API: api.graphql.imdb.com, the service
// imdb.com's web client queries (it names itself with the x-imdb-client-name
// header), on an imdb.com host, answering without login. A page of up to
// imdbPageSize reviews per request — the page's own list size; the API
// refuses a larger one — the next page named by the cursor each answer
// carries, each answer stating the title's total. The extractor reads the
// list through loaders.go's budget, at most imdbReviewPages pages: a title
// with thousands of reviews cannot be read whole, so the rest are named,
// stated · loaded, never dropped silently. Each answer is proved this title's
// list (no GraphQL error, the title's own id, a total, every review with an
// id) before it is kept in the page as a harvester-imdb-answer element, so a
// later conversion replays it like any followed loader. When no answer loads
// the page is not rendered: it falls through, the review list named unread.

const (
	imdbAPIHost    = "api.graphql.imdb.com"
	imdbAnswerTag  = "harvester-imdb-answer"
	imdbClientName = "imdb-web-next-localized"
	// imdbReviewPages bounds the review pages read: 8 pages of 25.
	imdbReviewPages = 8
	imdbPageSize    = 25
	imdbReviewQuery = `query TitleReviews($id: ID!, $first: Int!, $after: ID) { title(id: $id) { id ` +
		`titleText { text } reviews(first: $first, after: $after) { total pageInfo { endCursor hasNextPage } ` +
		`edges { node { id author { nickName } authorRating submissionDate spoiler summary { originalText } ` +
		`text { originalText { plainText } } } } } } }`
)

var imdbReviewsPath = regexp.MustCompile(`^/title/(tt\d+)/reviews/?$`)

type imdbReview struct {
	ID     string `json:"id"`
	Author *struct {
		NickName string `json:"nickName"`
	} `json:"author"`
	Rating    *int   `json:"authorRating"`
	Submitted string `json:"submissionDate"`
	Spoiler   bool   `json:"spoiler"`
	Summary   *struct {
		Text string `json:"originalText"`
	} `json:"summary"`
	Text *struct {
		Original *struct {
			Plain string `json:"plainText"`
		} `json:"originalText"`
	} `json:"text"`
}

type imdbAnswer struct {
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
	Data struct {
		Title *struct {
			ID        string `json:"id"`
			TitleText *struct {
				Text string `json:"text"`
			} `json:"titleText"`
			Reviews *struct {
				Total    *int `json:"total"`
				PageInfo struct {
					EndCursor   string `json:"endCursor"`
					HasNextPage bool   `json:"hasNextPage"`
				} `json:"pageInfo"`
				Edges []struct {
					Node imdbReview `json:"node"`
				} `json:"edges"`
			} `json:"reviews"`
		} `json:"title"`
	} `json:"data"`
}

// imdbPage is what a review page holds: its title id and the review pages
// kept for it, in page order.
type imdbPage struct {
	id      string
	pages   []imdbAnswer
	dropped bool
}

// isIMDbReviews accepts an address naming a title's review page.
func isIMDbReviews(page *url.URL) bool {
	return imdbReviewsPath.MatchString(page.Path)
}

// imdbPageOf reads the title a review address names and the answers kept for
// it. The page's markup is never read: every anonymous rung is served a wall.
func imdbPageOf(doc *html.Node, page *url.URL) (imdbPage, bool) {
	match := imdbReviewsPath.FindStringSubmatch(page.Path)
	if match == nil {
		return imdbPage{}, false
	}
	state := imdbPage{id: match[1]}
	state.pages, state.dropped = keptPages[imdbAnswer](doc, imdbAnswerTag, "IMDb")
	return state, true
}

// loaded counts the reviews the kept pages hold.
func (state *imdbPage) loaded() int {
	count := 0
	for index := range state.pages {
		count += len(state.pages[index].Data.Title.Reviews.Edges)
	}
	return count
}

// more reports whether another review page is to be read: none read yet, or
// the last one names a next page by a new cursor, and the bound and the
// stated total are not reached.
func (state *imdbPage) more() bool {
	count := len(state.pages)
	switch {
	case count == 0:
		return true
	case count >= imdbReviewPages:
		return false
	}
	last := state.pages[count-1].Data.Title.Reviews
	if !last.PageInfo.HasNextPage || last.PageInfo.EndCursor == "" || len(last.Edges) == 0 ||
		(count > 1 && last.PageInfo.EndCursor == state.pages[count-2].Data.Title.Reviews.PageInfo.EndCursor) {
		return false
	}
	return state.loaded() < *state.pages[0].Data.Title.Reviews.Total
}

// imdbLoaders names the next review page while the page lacks it.
func imdbLoaders(doc *html.Node, page *url.URL) []pageLoader {
	state, ok := imdbPageOf(doc, page)
	if !ok || state.dropped || !state.more() {
		return nil
	}
	number := len(state.pages)
	variables := map[string]any{"id": state.id, "first": imdbPageSize}
	if number > 0 {
		variables["after"] = state.pages[number-1].Data.Title.Reviews.PageInfo.EndCursor
	}
	body, err := json.Marshal(struct {
		Operation string         `json:"operationName"`
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}{"TitleReviews", imdbReviewQuery, variables})
	if err != nil {
		obs.Logger(context.Background()).Warn("harvest: an IMDb review request did not encode",
			obs.FieldErr, err.Error())
		return nil
	}
	key := fmt.Sprintf("imdb-reviews %s %d", state.id, number)
	return []pageLoader{{
		key:    key,
		label:  fmt.Sprintf("the title's review page %d", number+1),
		method: http.MethodPost,
		target: "https://" + imdbAPIHost + "/",
		body:   body,
		headers: map[string]string{
			headerAccept: mediaTypeJSON, headerContentType: mediaTypeJSON, "x-imdb-client-name": imdbClientName,
		},
		graft: func(answer []byte, contentType string) error {
			if err := checkIMDb(answer, contentType, state.id); err != nil {
				return err
			}
			keepAnswer(doc, imdbAnswerTag, key, "reviews", number, answer)
			return nil
		},
		drop: func() { keepAnswer(doc, imdbAnswerTag, key, "reviews", number, nil) },
	}}
}

// checkIMDb proves an answer is a page of this title's review list: no
// GraphQL error, the title named by its id, a stated total and a review list
// whose every review has an id.
func checkIMDb(body []byte, contentType, id string) error {
	var answer imdbAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		return fmt.Errorf("answered by a %s body that is not a page of the title's review list",
			discourseContentType(contentType))
	}
	if len(answer.Errors) > 0 {
		return fmt.Errorf("answered by a GraphQL error: %s", answer.Errors[0].Message)
	}
	title := answer.Data.Title
	if title == nil || title.ID != id || title.Reviews == nil || title.Reviews.Total == nil {
		return fmt.Errorf("answered by a body that is not title %s's review list with its total", id)
	}
	for index := range title.Reviews.Edges {
		if title.Reviews.Edges[index].Node.ID == "" {
			return fmt.Errorf("answered by a review page holding a review with no id")
		}
	}
	return nil
}

// imdbPosts maps the kept review pages to socialPosts under the title, each
// review once (a cursor page may repeat one the previous page served).
func imdbPosts(state *imdbPage) []socialPost {
	var out []socialPost
	seen := map[string]bool{}
	for index := range state.pages {
		for _, edge := range state.pages[index].Data.Title.Reviews.Edges {
			review := edge.Node
			if seen[review.ID] {
				continue
			}
			seen[review.ID] = true
			var parts []string
			if review.Summary != nil && review.Summary.Text != "" {
				parts = append(parts, "**"+strings.TrimSpace(review.Summary.Text)+"**")
			}
			verdict := "**Rating:** none given"
			if review.Rating != nil {
				verdict = fmt.Sprintf("**Rating:** %d/10", *review.Rating)
			}
			if review.Spoiler {
				verdict += " · **Contains spoilers**"
			}
			parts = append(parts, verdict)
			if review.Text != nil && review.Text.Original != nil && review.Text.Original.Plain != "" {
				parts = append(parts, strings.TrimSpace(review.Text.Original.Plain))
			}
			post := socialPost{
				id: review.ID, parent: state.id, author: unknownAuthor, posted: unknownPosted,
				link: "https://www.imdb.com/review/" + review.ID + "/", body: strings.Join(parts, "\n\n"),
			}
			if review.Author != nil && review.Author.NickName != "" {
				post.author = review.Author.NickName
			}
			if review.Submitted != "" {
				post.posted = review.Submitted
			}
			out = append(out, post)
		}
	}
	return out
}

// extractIMDbReviews renders a title's user reviews from the review pages
// kept in its page; with none kept it leaves the page unrendered, named.
func extractIMDbReviews(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	state, ok := imdbPageOf(doc, page)
	if !ok {
		return siteExtraction{}, false
	}
	if len(state.pages) == 0 {
		return siteExtraction{unrendered: "imdb reviews: the title's review list was not loaded from IMDb's " +
			"GraphQL API (its user reviews not read; the page is stored as another path served it)"}, false
	}
	first := state.pages[0].Data.Title
	title := state.id
	if first.TitleText != nil && first.TitleText.Text != "" {
		title = first.TitleText.Text
	}
	thread := socialThread{
		kind:  "imdb reviews",
		title: title + " — user reviews",
		post: socialPost{
			id: state.id, author: unknownAuthor, posted: unknownPosted,
			link: "https://www.imdb.com/title/" + state.id + "/reviews/", stated: *first.Reviews.Total,
		},
		repliesRead: true,
		notServed:   "IMDb's GraphQL API ended its review list (no further page) before its stated total",
		replies:     imdbPosts(&state),
	}
	last := state.pages[len(state.pages)-1].Data.Title.Reviews.PageInfo
	switch {
	case len(state.pages) >= imdbReviewPages && last.HasNextPage:
		thread.notServed = fmt.Sprintf("the harvester reads the first %d reviews (%d pages of %d from IMDb's "+
			"GraphQL API); the rest were not requested", imdbReviewPages*imdbPageSize, imdbReviewPages, imdbPageSize)
	case last.HasNextPage:
		// The list names a next page the budget did not load (its note says why).
		thread.notServed = "a further review page of IMDb's GraphQL API was not loaded"
	}
	markdown, partial := thread.render()
	return siteExtraction{markdown: markdown, partial: partial, apiRecord: true}, true
}
