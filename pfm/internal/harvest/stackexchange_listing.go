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

	"golang.org/x/net/html"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The Stack Exchange tag listing extractor: /questions/tagged/<tags>, one page
// of a site's questions under its tags, sorted by the listing's tab. The
// network serves the listing, like its question pages, to a client that is not
// a real reader's browser under HTTP 403 — as a Cloudflare challenge, or as the
// page itself; a page under an error status is never stored (harvest.go), and
// the reader rungs' copies drop question cards the site restyles client-side.
// So the page is read from the network's public API (api.stackexchange.com,
// unauthenticated, through loaders.go's budget like stackexchange.go's
// question): one request, /questions with the listing's tags, sort, page and
// page size, answering that page's questions in the site's order. The answer
// is proved the listing's — every question carries every requested tag, and an
// empty page (a tag or page that does not exist) is refused, so a missing
// listing goes on down the ladder and is never stored as an empty one. The
// page reconciles what it loaded against the page size it states, and names
// the later pages it did not read.

const (
	// seListingTag is the element that keeps the listing's API answer in the page.
	seListingTag  = "harvester-stackexchange-listing"
	seKindListing = "listing"
	// seListingPageSize is the site's page size when the address names none;
	// seListingMaxPageSize the API's maximum.
	seListingPageSize    = 15
	seListingMaxPageSize = 100
)

// seLinkText escapes a title's brackets, which would end its link text early.
var seLinkText = strings.NewReplacer("[", `\[`, "]", `\]`)

// seListingPathRe reads a tag listing's address: /questions/tagged/<tags>.
var seListingPathRe = regexp.MustCompile(`^/questions/tagged/([^/]+)/?$`)

// seListingSorts maps a listing's tab to the API's sort; a tab missing here
// (unanswered, bounties, frequent) lists another set of questions than the
// API's /questions and is not claimed.
var seListingSorts = map[string]string{
	"":       "creation", // the listing's default tab, Newest
	"newest": "creation",
	"active": "activity",
	"votes":  "votes",
	"hot":    "hot",
	"week":   "week",
	"month":  "month",
}

// seListingRef is the listing a page is: its site, tags, sort, page and size.
type seListingRef struct {
	site     string
	tags     []string
	sort     string
	page     int
	pageSize int
}

// seListingOf reads page's address as a Stack Exchange tag listing's; false
// for any other address, or a tab or page the API's /questions cannot answer.
func seListingOf(page *url.URL) (seListingRef, bool) {
	site, owned := seSiteOf(page)
	if !owned {
		return seListingRef{}, false
	}
	match := seListingPathRe.FindStringSubmatch(page.EscapedPath())
	if match == nil {
		return seListingRef{}, false
	}
	joined, err := url.PathUnescape(match[1])
	if err != nil {
		return seListingRef{}, false
	}
	tags := strings.FieldsFunc(strings.ToLower(joined), func(r rune) bool { return r == '+' || r == ' ' })
	query := page.Query()
	sort, known := seListingSorts[strings.ToLower(query.Get("tab"))]
	number, size := seListingNumber(query.Get("page"), 1), seListingNumber(query.Get("pagesize"), seListingPageSize)
	if len(tags) == 0 || !known || number < 1 || size < 1 || size > seListingMaxPageSize {
		return seListingRef{}, false
	}
	return seListingRef{site: site, tags: tags, sort: sort, page: number, pageSize: size}, true
}

// seListingNumber reads a query number, fallback when absent; 0 when unreadable.
func seListingNumber(value string, fallback int) int {
	if value == "" {
		return fallback
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return number
}

func isStackExchangeListingAddress(page *url.URL) bool {
	_, ok := seListingOf(page)
	return ok
}

// target is the API address of the listing's page, in the API's default
// filter (a question's title, link, score, answer, view and accepted state,
// tags, owner and date; the wrapper's has_more, backoff, quota and errors).
func (ref seListingRef) target() string {
	query := url.Values{
		"site": {ref.site}, "tagged": {strings.Join(ref.tags, ";")}, "sort": {ref.sort}, "order": {"desc"},
		pageKey: {strconv.Itoa(ref.page)}, "pagesize": {strconv.Itoa(ref.pageSize)}, seFilterParam: {"default"},
	}
	return seAPI + "/questions?" + query.Encode()
}

func (ref seListingRef) label() string {
	return fmt.Sprintf("page %d of the [%s] listing", ref.page, strings.Join(ref.tags, "] ["))
}

// seListed is one question of a listing's API answer.
type seListed struct {
	QuestionID   int64    `json:"question_id"`
	Title        string   `json:"title"`
	Link         string   `json:"link"`
	Score        int      `json:"score"`
	AnswerCount  int      `json:"answer_count"`
	ViewCount    int      `json:"view_count"`
	IsAnswered   bool     `json:"is_answered"`
	AcceptedID   int64    `json:"accepted_answer_id"`
	CreationDate int64    `json:"creation_date"`
	Tags         []string `json:"tags"`
	Owner        *seOwner `json:"owner"`
}

// decode reads a listing's API answer and proves it the listing's.
func (ref seListingRef) decode(body []byte, contentType string) ([]seListed, bool, error) {
	notJSON := fmt.Errorf("answered by a %s body that is not the API's JSON for %s",
		discourseContentType(contentType), ref.label())
	var wrapper seWrapper
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, false, notJSON
	}
	if wrapper.ErrorID != 0 {
		return nil, false, fmt.Errorf("answered by the API's error %d (%s)", wrapper.ErrorID, wrapper.ErrorName)
	}
	var listed []seListed
	if err := json.Unmarshal(wrapper.Items, &listed); err != nil {
		return nil, false, notJSON
	}
	if len(listed) == 0 {
		return nil, false, fmt.Errorf("answered by no question on %s (the tag or the page does not exist)",
			ref.label())
	}
	for index := range listed {
		have := map[string]bool{}
		for _, tag := range listed[index].Tags {
			have[strings.ToLower(tag)] = true
		}
		for _, tag := range ref.tags {
			if listed[index].QuestionID == 0 || !have[tag] {
				return nil, false, fmt.Errorf("answered by a list holding question #%d, not tagged [%s]",
					listed[index].QuestionID, tag)
			}
		}
	}
	return listed, wrapper.HasMore, nil
}

// seListingKept reads the listing's API answer kept in doc: kept is false when
// none is, dropped true when its request failed.
func seListingKept(doc *html.Node) (body []byte, kept, dropped bool) {
	nodes := keptAnswers(doc, seListingTag)
	switch {
	case len(nodes) == 0:
		return nil, false, false
	case nodeAttr(nodes[0], "dropped") != "":
		return nil, false, true
	}
	return []byte(rawText(nodes[0])), true, false
}

// stackExchangeListingLoaders names the listing's API answer while the page
// lacks it, for loaders.go.
func stackExchangeListingLoaders(doc *html.Node, page *url.URL) []pageLoader {
	ref, ok := seListingOf(page)
	if !ok {
		return nil
	}
	if _, kept, dropped := seListingKept(doc); kept || dropped {
		return nil
	}
	target := ref.target()
	key := seKey(target)
	return []pageLoader{{
		key:         key,
		label:       "the API's " + ref.label(),
		method:      http.MethodGet,
		target:      target,
		headers:     map[string]string{headerAccept: mediaTypeJSON},
		rateLimited: seRateLimited,
		backoff:     seBackoff,
		quotaSpent:  seQuotaSpent,
		graft: func(body []byte, contentType string) error {
			if _, _, err := ref.decode(body, contentType); err != nil {
				return err
			}
			keepAnswer(doc, seListingTag, key, seKindListing, ref.page, body)
			return nil
		},
		drop: func() { keepAnswer(doc, seListingTag, key, seKindListing, ref.page, nil) },
	}}
}

// extractStackExchangeListing renders the listing's page from its kept API
// answer: a header, the count line reconciling the questions loaded against
// the page size, and each question in the site's order.
func extractStackExchangeListing(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	ref, ok := seListingOf(page)
	if !ok {
		return siteExtraction{}, false
	}
	unrendered := siteExtraction{unrendered: "stackexchange listing: the API's " + ref.label() +
		" was not loaded (the page is stored as rendered)"}
	body, kept, _ := seListingKept(doc)
	if !kept {
		return unrendered, false
	}
	listed, hasMore, err := ref.decode(body, mediaTypeJSON)
	if err != nil {
		// graft kept only an answer that decoded; one that no longer does is a
		// bug, logged, and the page goes on down the ladder named unrendered.
		obs.Logger(context.Background()).Warn("harvest: the kept Stack Exchange listing no longer decodes",
			"listing", ref.label(), obs.FieldErr, err.Error())
		return unrendered, false
	}
	var out strings.Builder
	fmt.Fprintf(&out, "# [%s] questions on %s — page %d, sorted by %s\n\n",
		strings.Join(ref.tags, "] ["), ref.site, ref.page, ref.sort)
	stated := len(listed)
	var gaps []string
	if hasMore {
		stated = ref.pageSize
		gaps = append(gaps, fmt.Sprintf("later pages (from page %d) not read", ref.page+1))
	}
	countLine := fmt.Sprintf("**Questions on this page:** %d stated · %d loaded", stated, len(listed))
	if rest := stated - len(listed); rest > 0 {
		gaps = append([]string{fmt.Sprintf("%d of the page's %d not in the API's page", rest, stated)}, gaps...)
	}
	if len(gaps) > 0 {
		countLine += " · gaps: " + strings.Join(gaps, "; ")
	}
	out.WriteString(countLine + "\n\n")
	for index, question := range listed {
		link := question.Link
		if link == "" {
			link = "https://" + ref.site + "/questions/" + strconv.FormatInt(question.QuestionID, 10)
		}
		line := fmt.Sprintf("%d. [%s](%s) · score %d · %d answer(s)", (ref.page-1)*ref.pageSize+index+1,
			seLinkText.Replace(html.UnescapeString(question.Title)), link, question.Score, question.AnswerCount)
		if question.AcceptedID != 0 {
			line += " · accepted"
		}
		line += fmt.Sprintf(" · %d views · asked %s by **%s**", question.ViewCount,
			sePosted(question.CreationDate), seAuthor(question.Owner))
		if len(question.Tags) > 0 {
			line += " · tags: " + html.UnescapeString(strings.Join(question.Tags, ", "))
		}
		out.WriteString(line + "\n")
	}
	partial := ""
	if len(gaps) > 0 {
		partial = fmt.Sprintf("stackexchange listing: %s, %d of %d questions loaded — %s",
			ref.label(), len(listed), stated, strings.Join(gaps, "; "))
	}
	return siteExtraction{markdown: out.String(), partial: partial, apiRecord: true}, true
}
