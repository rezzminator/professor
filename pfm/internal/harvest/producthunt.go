package harvest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The Product Hunt reviews extractor. A product's reviews page
// (www.producthunt.com/products/{slug}/reviews) renders its header, an AI
// summary and the review tabs as markup, but the reviews themselves are
// client-hydrated: the page embeds its Apollo client's first answers — the
// product's stated review count and the first few reviews — in
// ApolloSSRDataTransport scripts, and the client reads the rest from the
// site's GraphQL endpoint on the same host, /frontend/graphql, one numbered
// page at a time. The endpoint answers anonymously a query sent with its
// persisted-query hash (a bare query is refused: "This request could not be
// processed") and caps a page at 100 reviews. The extractor reads the page's
// embedded state first, then the newest reviews through loaders.go's budget,
// at most productHuntReviewPages pages; the rest are named, stated · loaded,
// never dropped silently. Each answer is checked to be this product's review
// list before it is kept in the page as a harvester-producthunt-answer
// element, so a later conversion replays it like any followed loader. When
// the list does not load, the embedded reviews render and the rest are named.

const (
	productHuntAnswerTag = "harvester-producthunt-answer"
	// productHuntReviewPages bounds the review pages read: 20 pages of 100.
	productHuntReviewPages = 20
	productHuntPageSize    = 100
	productHuntSSRMarker   = `ApolloSSRDataTransport")] ??= []).push(`
	productHuntReviewQuery = "query DetailedReviewsFeedQuery($slug:String!$reviewsLimit:Int!$reviewsPage:Int" +
		"$reviewsOrder:DetailedReviewsOrder){product(slug:$slug){id slug name detailedReviewsCount " +
		"detailedReviews(first:$reviewsLimit page:$reviewsPage order:$reviewsOrder){totalCount " +
		"pageInfo{hasNextPage}edges{node{id reviewType overallRating createdAt overallExperience " +
		"positiveFeedback negativeFeedback alternativesFeedback user{name username}}}}}}"
)

var (
	productHuntReviewsPath = regexp.MustCompile(`^/products/([a-z0-9][a-z0-9-]*)/reviews/?$`)
	productHuntQueryHash   = func() string {
		sum := sha256.Sum256([]byte(productHuntReviewQuery))
		return hex.EncodeToString(sum[:])
	}()
)

type productHuntReview struct {
	ID            string  `json:"id"`
	ReviewType    string  `json:"reviewType"`
	OverallRating *int    `json:"overallRating"`
	CreatedAt     string  `json:"createdAt"`
	Overall       *string `json:"overallExperience"`
	Positive      *string `json:"positiveFeedback"`
	Negative      *string `json:"negativeFeedback"`
	Alternatives  *string `json:"alternativesFeedback"`
	User          *struct {
		Name     string `json:"name"`
		Username string `json:"username"`
	} `json:"user"`
}

type productHuntReviewList struct {
	TotalCount *int `json:"totalCount"`
	PageInfo   struct {
		HasNextPage bool `json:"hasNextPage"`
	} `json:"pageInfo"`
	Edges []struct {
		Node productHuntReview `json:"node"`
	} `json:"edges"`
}

type productHuntProduct struct {
	Slug            string                 `json:"slug"`
	Name            string                 `json:"name"`
	ReviewsCount    *int                   `json:"detailedReviewsCount"`
	DetailedReviews *productHuntReviewList `json:"detailedReviews"`
}

type productHuntAnswer struct {
	Data *struct {
		Product *productHuntProduct `json:"product"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// productHuntPage is what a reviews page holds: the product its embedded
// state names, the reviews and counts embedded there, and the review list
// pages kept for it, in page order.
type productHuntPage struct {
	slug, name string
	stated     int // the product's own review count, as the page states it
	listTotal  int // the embedded review list's total (its widest list)
	embedded   []productHuntReview
	pages      []productHuntReviewList
	dropped    bool
}

// isProductHuntReviews accepts a product's reviews address.
func isProductHuntReviews(page *url.URL) bool {
	return productHuntReviewsPath.MatchString(page.Path)
}

// productHuntUndefinedToNull turns the bare `undefined` values the transport
// script writes (JavaScript, not JSON) into null, leaving strings untouched.
func productHuntUndefinedToNull(script string) string {
	var out strings.Builder
	inString, escaped := false, false
	for index := 0; index < len(script); index++ {
		char := script[index]
		switch {
		case inString:
			inString = escaped || char != '"'
			escaped = !escaped && char == '\\'
		case char == '"':
			inString = true
		case strings.HasPrefix(script[index:], "undefined"):
			out.WriteString("null")
			index += len("undefined") - 1
			continue
		}
		out.WriteByte(char)
	}
	return out.String()
}

// productHuntEmbedded reads the products the page's Apollo transport scripts
// answered; a script that does not decode is logged and left out.
func productHuntEmbedded(doc *html.Node) []productHuntProduct {
	var products []productHuntProduct
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if isElement(node, "script") {
			text := rawText(node)
			if at := strings.Index(text, productHuntSSRMarker); at >= 0 {
				var push struct {
					Events []struct {
						Type  string `json:"type"`
						Value struct {
							Data struct {
								Product *productHuntProduct `json:"product"`
							} `json:"data"`
						} `json:"value"`
					} `json:"events"`
				}
				payload := productHuntUndefinedToNull(text[at+len(productHuntSSRMarker):])
				if err := json.NewDecoder(strings.NewReader(payload)).Decode(&push); err != nil {
					obs.Logger(context.Background()).Warn("harvest: a Product Hunt state script does not decode; "+
						"left out", obs.FieldErr, err.Error())
					return
				}
				for _, event := range push.Events {
					if event.Type == "next" && event.Value.Data.Product != nil {
						products = append(products, *event.Value.Data.Product)
					}
				}
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return products
}

// productHuntPageOf reads the product a reviews page's embedded state names
// and the answers kept for it; false for any other page (no embedded state,
// another product's).
func productHuntPageOf(doc *html.Node, page *url.URL) (productHuntPage, bool) {
	match := productHuntReviewsPath.FindStringSubmatch(page.Path)
	if match == nil {
		return productHuntPage{}, false
	}
	state := productHuntPage{slug: match[1]}
	seen := map[string]bool{}
	found := false
	for _, product := range productHuntEmbedded(doc) {
		if product.Slug != state.slug {
			continue
		}
		if product.ReviewsCount != nil {
			found, state.name, state.stated = true, product.Name, *product.ReviewsCount
		}
		if list := product.DetailedReviews; list != nil {
			if list.TotalCount != nil && *list.TotalCount > state.listTotal {
				state.listTotal = *list.TotalCount
			}
			for _, edge := range list.Edges {
				if edge.Node.ID != "" && !seen[edge.Node.ID] {
					seen[edge.Node.ID] = true
					state.embedded = append(state.embedded, edge.Node)
				}
			}
		}
	}
	if !found {
		return productHuntPage{}, false
	}
	for _, node := range keptAnswers(doc, productHuntAnswerTag) {
		if nodeAttr(node, "dropped") != "" {
			state.dropped = true
			continue
		}
		var answer productHuntAnswer
		if err := json.Unmarshal([]byte(rawText(node)), &answer); err != nil {
			obs.Logger(context.Background()).Warn("harvest: a kept Product Hunt answer no longer decodes; left out",
				"page", nodeAttr(node, "page"), obs.FieldErr, err.Error())
			break
		}
		if number, err := strconv.Atoi(nodeAttr(node, "page")); err != nil || number != len(state.pages) {
			obs.Logger(context.Background()).Warn("harvest: a kept Product Hunt answer is out of page order; left out",
				"page", nodeAttr(node, "page"))
			break
		}
		state.pages = append(state.pages, *answer.Data.Product.DetailedReviews)
	}
	return state, true
}

// more reports whether another review page is to be read: none read yet, or
// the last one served reviews and names a next page, and the bound and the
// stated total are not reached.
func (state *productHuntPage) more() bool {
	count := len(state.pages)
	switch {
	case count == 0:
		return true
	case count >= productHuntReviewPages:
		return false
	}
	last := &state.pages[count-1]
	if len(last.Edges) == 0 || !last.PageInfo.HasNextPage {
		return false
	}
	loaded := 0
	for index := range state.pages {
		loaded += len(state.pages[index].Edges)
	}
	return loaded < *state.pages[0].TotalCount
}

// productHuntLoaders names the next review page while the page lacks it.
func productHuntLoaders(doc *html.Node, page *url.URL) []pageLoader {
	state, ok := productHuntPageOf(doc, page)
	if !ok || state.dropped || !state.more() {
		return nil
	}
	number := len(state.pages)
	type persisted struct {
		Version int    `json:"version"`
		Hash    string `json:"sha256Hash"`
	}
	body, err := json.Marshal(map[string]any{
		"operationName": "DetailedReviewsFeedQuery",
		"variables": map[string]any{
			"slug": state.slug, "reviewsLimit": productHuntPageSize, "reviewsPage": number + 1,
			"reviewsOrder": "newest",
		},
		"query":      productHuntReviewQuery,
		"extensions": map[string]any{"persistedQuery": persisted{Version: 1, Hash: productHuntQueryHash}},
	})
	if err != nil {
		obs.Logger(context.Background()).Warn("harvest: a Product Hunt review request did not encode",
			obs.FieldErr, err.Error())
		return nil
	}
	key := fmt.Sprintf("producthunt-reviews %s %d", state.slug, number)
	return []pageLoader{{
		key:    key,
		label:  fmt.Sprintf("the product's review page %d", number+1),
		method: http.MethodPost,
		target: (&url.URL{Scheme: page.Scheme, Host: page.Host, Path: "/frontend/graphql"}).String(),
		body:   body,
		headers: map[string]string{
			headerAccept: mediaTypeJSON, headerContentType: mediaTypeJSON, "X-Requested-With": "XMLHttpRequest",
		},
		graft: func(answer []byte, contentType string) error {
			if err := checkProductHunt(answer, contentType, state.slug); err != nil {
				return err
			}
			keepAnswer(doc, productHuntAnswerTag, key, "reviews", number, answer)
			return nil
		},
		drop: func() { keepAnswer(doc, productHuntAnswerTag, key, "reviews", number, nil) },
	}}
}

// checkProductHunt proves an answer is a page of this product's review list:
// no GraphQL error, the product named by its slug, a stated total and a
// review list whose every review has an id.
func checkProductHunt(body []byte, contentType, slug string) error {
	var answer productHuntAnswer
	if err := json.Unmarshal(body, &answer); err != nil {
		return fmt.Errorf("answered by a %s body that is not a page of the site's review list",
			discourseContentType(contentType))
	}
	if len(answer.Errors) > 0 {
		return fmt.Errorf("answered with the GraphQL error %q", answer.Errors[0].Message)
	}
	if answer.Data == nil || answer.Data.Product == nil || answer.Data.Product.Slug != slug ||
		answer.Data.Product.DetailedReviews == nil || answer.Data.Product.DetailedReviews.TotalCount == nil {
		return fmt.Errorf("answered by a review page that is not this product's review list")
	}
	for _, edge := range answer.Data.Product.DetailedReviews.Edges {
		if edge.Node.ID == "" {
			return fmt.Errorf("answered by a review page holding a review with no id")
		}
	}
	return nil
}

// productHuntPost renders one review: its author as the page shows them, its
// rating, date and every written part.
func productHuntPost(review *productHuntReview, slug string) socialPost {
	post := socialPost{
		id: review.ID, parent: slug, author: unknownAuthor, posted: review.CreatedAt,
		link: "https://www.producthunt.com/products/" + slug + "/reviews?review=" + review.ID,
	}
	if review.User != nil && review.User.Name != "" {
		post.author = review.User.Name
		if review.User.Username != "" {
			post.author += " (@" + review.User.Username + ")"
		}
	}
	if posted, err := time.Parse(time.RFC3339, review.CreatedAt); err == nil {
		post.posted = posted.UTC().Format("2006-01-02 15:04 UTC")
	} else if review.CreatedAt == "" {
		post.posted = unknownPosted
	}
	parts := []string{"**Unrated**"}
	if review.OverallRating != nil {
		parts[0] = fmt.Sprintf("**%d/5**", *review.OverallRating)
	}
	if review.ReviewType != "" {
		parts[0] += " · " + review.ReviewType + " review"
	}
	for _, part := range []struct {
		label string
		text  *string
	}{
		{"Overall", review.Overall},
		{"What they like", review.Positive},
		{"What could be better", review.Negative},
		{"Compared with alternatives", review.Alternatives},
	} {
		if part.text != nil && strings.TrimSpace(*part.text) != "" {
			parts = append(parts, "**"+part.label+":**\n\n"+htmlMarkdown(*part.text))
		}
	}
	post.body = strings.Join(parts, "\n\n")
	return post
}

// extractProductHuntReviews renders a product's reviews from the review list
// pages kept in its page, or, when none loaded, from its embedded state.
func extractProductHuntReviews(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	state, ok := productHuntPageOf(doc, page)
	if !ok {
		return siteExtraction{}, false
	}
	read := len(state.pages) > 0
	thread := socialThread{
		kind:  "product hunt reviews",
		title: state.name + " reviews",
		post: socialPost{
			id: state.slug, author: unknownAuthor, posted: unknownPosted,
			link: "https://www.producthunt.com/products/" + state.slug + "/reviews",
		},
		notServed: fmt.Sprintf("the harvester reads the newest %d reviews (%d pages of the site's review "+
			"list); the rest were not requested", productHuntReviewPages*productHuntPageSize, productHuntReviewPages),
	}
	reviews := state.embedded
	thread.post.stated = max(state.stated, state.listTotal)
	if read {
		reviews = nil
		for index := range state.pages {
			for _, edge := range state.pages[index].Edges {
				reviews = append(reviews, edge.Node)
			}
		}
		thread.post.stated = *state.pages[0].TotalCount
	} else {
		thread.notServed = "the page embeds only its first reviews; the rest are in the site's review list, " +
			"which was not read"
	}
	if thread.post.stated != state.stated {
		thread.post.body = fmt.Sprintf("The page states %d reviews; the site's review list counts %d.",
			state.stated, thread.post.stated)
	}
	if state.dropped && (!read || state.more()) {
		thread.gaps = append(thread.gaps, "a further review page failed to load")
	}
	seen := map[string]bool{}
	for index := range reviews {
		if !seen[reviews[index].ID] {
			seen[reviews[index].ID] = true
			thread.replies = append(thread.replies, productHuntPost(&reviews[index], state.slug))
		}
	}
	thread.repliesRead = len(thread.replies) > 0 || read
	markdown, partial := thread.render()
	return siteExtraction{markdown: markdown, partial: partial, apiRecord: read}, true
}
