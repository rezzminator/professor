package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The Steam store extractor. An app's store page (store.steampowered.com/app/
// {id}) renders its name, developer, release date and description, but none
// of its reviews: those load after the page from the store's review API on
// the same host, /appreviews/{id}?json=1, a page of up to 100 reviews per
// request, the next page named by the cursor each answer carries. The first
// answer states the app's total (every language, every purchase type — the
// store page states only its own language's count). The extractor reads the
// most recent reviews through loaders.go's budget, at most steamReviewPages
// pages: an app with hundreds of thousands of reviews cannot be read whole,
// so the rest are named, stated · loaded, never dropped silently. Each answer
// is checked to be the API's review list before it is kept in the page as a
// harvester-steam-answer element, so a later conversion replays it like any
// followed loader. The API answer names no app id, so the proof is its shape:
// success 1, a review list, each review with an id, and on the first page the
// query_summary total.

const (
	steamHost      = "store.steampowered.com"
	steamAnswerTag = "harvester-steam-answer"
	// steamReviewPages bounds the review pages read: 5 pages of 100.
	steamReviewPages = 5
	steamPageSize    = 100
)

var steamAppPath = regexp.MustCompile(`^/app/(\d+)(?:/|$)`)

type steamReview struct {
	ID     string `json:"recommendationid"`
	Author struct {
		SteamID     string `json:"steamid"`
		PersonaName string `json:"personaname"`
		PlaytimeAt  int    `json:"playtime_at_review"`
	} `json:"author"`
	Language string `json:"language"`
	Review   string `json:"review"`
	Created  int64  `json:"timestamp_created"`
	VotedUp  bool   `json:"voted_up"`
	VotesUp  int    `json:"votes_up"`
}

type steamAnswer struct {
	Success int `json:"success"`
	Summary struct {
		Total *int `json:"total_reviews"`
	} `json:"query_summary"`
	Cursor  string         `json:"cursor"`
	Reviews *[]steamReview `json:"reviews"`
}

// steamPage is what an app page holds: its app id and the review pages kept
// for it, in page order.
type steamPage struct {
	id      string
	pages   []steamAnswer
	dropped bool
}

// isSteamApp accepts a store address naming an app.
func isSteamApp(page *url.URL) bool {
	return steamAppPath.MatchString(page.Path)
}

// steamPageOf reads the app a store page names and the answers kept for it;
// false for any other page (an age gate, a region wall).
func steamPageOf(doc *html.Node, page *url.URL) (steamPage, bool) {
	match := steamAppPath.FindStringSubmatch(page.Path)
	if match == nil || firstWithAttr(doc, "id", "appHubAppName", nil) == nil {
		return steamPage{}, false
	}
	state := steamPage{id: match[1]}
	state.pages, state.dropped = keptPages[steamAnswer](doc, steamAnswerTag, "Steam")
	return state, true
}

// more reports whether another review page is to be read: none read yet, or
// the last one named a new cursor, served reviews, and the bound and the
// stated total are not reached.
func (state *steamPage) more() bool {
	count := len(state.pages)
	switch {
	case count == 0:
		return true
	case count >= steamReviewPages:
		return false
	}
	last := &state.pages[count-1]
	if len(*last.Reviews) == 0 || last.Cursor == "" ||
		(count > 1 && last.Cursor == state.pages[count-2].Cursor) {
		return false
	}
	loaded := 0
	for index := range state.pages {
		loaded += len(*state.pages[index].Reviews)
	}
	return loaded < *state.pages[0].Summary.Total
}

// steamLoaders names the next review page while the page lacks it.
func steamLoaders(doc *html.Node, page *url.URL) []pageLoader {
	state, ok := steamPageOf(doc, page)
	if !ok || state.dropped || !state.more() {
		return nil
	}
	number := len(state.pages)
	cursor := "*"
	if number > 0 {
		cursor = state.pages[number-1].Cursor
	}
	query := url.Values{
		"json": {"1"}, "filter": {"recent"}, "language": {"all"}, "purchase_type": {"all"},
		"num_per_page": {strconv.Itoa(steamPageSize)}, "cursor": {cursor},
	}
	target := (&url.URL{
		Scheme: page.Scheme, Host: page.Host, Path: "/appreviews/" + state.id, RawQuery: query.Encode(),
	}).String()
	key := fmt.Sprintf("steam-reviews %s %d", state.id, number)
	return []pageLoader{{
		key:     key,
		label:   fmt.Sprintf("the app's review page %d", number+1),
		method:  http.MethodGet,
		target:  target,
		headers: map[string]string{headerAccept: mediaTypeJSON},
		graft: func(body []byte, contentType string) error {
			if err := checkSteam(body, contentType, number == 0); err != nil {
				return err
			}
			keepAnswer(doc, steamAnswerTag, key, "reviews", number, body)
			return nil
		},
		drop: func() { keepAnswer(doc, steamAnswerTag, key, "reviews", number, nil) },
	}}
}

// checkSteam proves an answer is a page of the review API: success 1, a
// review list whose every review has an id, and — on the first page — the
// app's stated total.
func checkSteam(body []byte, contentType string, first bool) error {
	var answer steamAnswer
	if err := json.Unmarshal(body, &answer); err != nil || answer.Success != 1 || answer.Reviews == nil {
		return fmt.Errorf("answered by a %s body that is not a page of the store's review list",
			discourseContentType(contentType))
	}
	if first && answer.Summary.Total == nil {
		return fmt.Errorf("answered by a first review page that states no total")
	}
	for index := range *answer.Reviews {
		if (*answer.Reviews)[index].ID == "" {
			return fmt.Errorf("answered by a review page holding a review with no id")
		}
	}
	return nil
}

var (
	steamBBLink  = regexp.MustCompile(`(?s)\[url=([^\]]+)\](.*?)\[/url\]`)
	steamBBTag   = regexp.MustCompile(`\[/?(?:u|spoiler|noparse|list|olist|quote|code|table|tr|td|th)(?:=[^\]]*)?\]`)
	steamBBBlank = regexp.MustCompile(`\n{3,}`)
)

// steamMarkdown renders a review's BBCode as Markdown: bold, italics,
// strike-through, headings, list items and links; the tags with no Markdown
// form (underline, spoilers, quotes, tables) leave their text.
func steamMarkdown(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = steamBBLink.ReplaceAllString(text, "[$2]($1)")
	text = strings.NewReplacer(
		"[b]", "**", "[/b]", "**", "[i]", "*", "[/i]", "*", "[strike]", "~~", "[/strike]", "~~",
		"[h1]", "\n### ", "[/h1]", "\n", "[h2]", "\n### ", "[/h2]", "\n", "[h3]", "\n### ", "[/h3]", "\n",
		"[*]", "\n-", "[hr][/hr]", "\n---\n", "[hr]", "\n---\n",
	).Replace(text)
	text = steamBBTag.ReplaceAllString(text, "")
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		lines[index] = strings.TrimSpace(line)
	}
	return strings.TrimSpace(steamBBBlank.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

// steamPosts maps the kept review pages to socialPosts under the app, each
// review once (a cursor page may repeat one the previous page served).
func steamPosts(state *steamPage, parent string) []socialPost {
	var out []socialPost
	seen := map[string]bool{}
	for index := range state.pages {
		for _, review := range *state.pages[index].Reviews {
			if seen[review.ID] {
				continue
			}
			seen[review.ID] = true
			verdict := "**Recommended**"
			if !review.VotedUp {
				verdict = "**Not recommended**"
			}
			verdict += fmt.Sprintf(" · %.1f h on record at review · %d found it helpful · %s",
				float64(review.Author.PlaytimeAt)/60, review.VotesUp, review.Language)
			post := socialPost{
				id: review.ID, parent: parent, author: unknownAuthor,
				posted: time.Unix(review.Created, 0).UTC().Format("2006-01-02 15:04 UTC"),
				link:   "https://steamcommunity.com/profiles/" + review.Author.SteamID + "/recommended/" + parent + "/",
				body:   verdict + "\n\n" + steamMarkdown(review.Review),
			}
			if review.Author.PersonaName != "" {
				post.author = review.Author.PersonaName
			}
			out = append(out, post)
		}
	}
	return out
}

// steamText is an element's text, whitespace collapsed; "" when absent.
func steamText(doc *html.Node, key, value string) string {
	if node := firstWithAttr(doc, key, value, nil); node != nil {
		return strings.Join(strings.Fields(rawText(node)), " ")
	}
	return ""
}

// extractSteamApp renders a store page's app from its markup and its reviews
// from the review pages kept in its page.
func extractSteamApp(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	state, ok := steamPageOf(doc, page)
	if !ok {
		return siteExtraction{}, false
	}
	read := len(state.pages) > 0
	stated := 0
	if read {
		stated = *state.pages[0].Summary.Total
	} else if node := firstWithAttr(doc, "itemprop", "reviewCount", nil); node != nil && node.DataAtom == atom.Meta {
		// Unread, the stated count is the page's own (its language's reviews).
		count, err := strconv.Atoi(nodeAttr(node, "content"))
		if err != nil {
			obs.Logger(context.Background()).Warn("harvest: a Steam page's review count is not a number",
				obs.FieldErr, err.Error())
		}
		stated = count
	}
	thread := socialThread{
		kind:  "steam app",
		title: steamText(doc, "id", "appHubAppName"),
		post: socialPost{
			id: state.id, author: unknownAuthor, posted: unknownPosted,
			link: "https://" + steamHost + page.Path, stated: stated,
		},
		repliesRead: read,
		notServed: fmt.Sprintf("the harvester reads the most recent %d reviews (%d pages of the store's review "+
			"API); the rest were not requested", steamReviewPages*steamPageSize, steamReviewPages),
	}
	if developer := steamText(doc, "id", "developers_list"); developer != "" {
		thread.post.author = developer
	}
	if date := steamText(doc, "class", "date"); date != "" {
		thread.post.posted = date
	}
	if description := firstWithAttr(doc, "id", "game_area_description", nil); description != nil {
		renderer := markdownRenderer{base: page}
		thread.post.body = strings.Join(renderer.blocks(description), "\n\n")
	}
	if state.dropped && read && state.more() {
		thread.gaps = append(thread.gaps, "a further review page failed to load")
	}
	if read {
		thread.replies = steamPosts(&state, state.id)
	}
	markdown, partial := thread.render()
	return siteExtraction{markdown: markdown, partial: partial, apiRecord: read}, true
}
