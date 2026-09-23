package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The Stack Exchange question extractor, for every site of the network
// (stackoverflow.com and its language sites, superuser.com, serverfault.com,
// askubuntu.com, mathoverflow.net, stackapps.com, *.stackexchange.com). A
// question page lists its answers 30 to a page and shows each post's top
// comments behind "Show N more comments" — but the network serves every
// question page, to any client that is not a real reader's browser (Go's, the
// headless and the headed browser rung's alike), a Cloudflare challenge
// instead, so neither the page nor its loaders are there to follow. The
// extractor is therefore registered by host, not by the network's shared
// markup (a walled page carries none of it), knows the question by its
// address (/questions/<id>[/slug[/answer-id]], /q/<id>), and reads it from the
// network's public API (api.stackexchange.com, a subdomain it owns),
// unauthenticated — no credential or key is ever sent — through loaders.go's
// budget like any loader: the question's record (title, body, score, author,
// its comments and the stated answer and comment counts), then every page of
// its answers (pagesize=100, oldest first so paging is stable while votes
// move), each answer with its score, accepted mark, author, stated comment
// count and comments. The API's filter (seFilter) embeds each post's whole
// comment list, so no request per post is spent. Each answer is checked to be
// this question's before it is kept in the page as a
// harvester-stackexchange-answer element, so a later conversion replays it.
// The API allows 300 requests a day per address without a key, answers its
// exhaustion with HTTP 400 and error_id 502 (throttle_violation) — which ends
// the following, named, never retried — reports the quota left on every
// answer (quota_remaining: at 0 the following ends, named, before another
// request), and may ask for a back-off, which the following honours
// (loaders.go). A question whose own record never loaded is not claimed: the
// walled page goes on down the ladder to the browser rung, the record's gap
// named in whatever it renders. The extractor renders the question and its
// answers by score, and reconciles what it loaded against the stated counts:
// an answer page not loaded, a stated answer or comment the API did not list,
// and a count not read each flag the artifact partial and are named.

const (
	// seAPI is the API's origin and version; sePageSize the page size asked
	// for, the API's maximum.
	seAPI      = "https://api.stackexchange.com/2.3"
	sePageSize = 100
	// seFilter is the API filter (created once by /filters/create, immutable)
	// naming the fields read: the wrapper's items, has_more, page, backoff,
	// quota and error fields; a question's id, title, link, body, score, date,
	// owner, tags, closed reason, accepted answer, answer and comment counts
	// and comments; an answer's id, question id, body, score, date, owner,
	// accepted mark, comment count and comments; a comment's id, post id,
	// body, score, date and owner; a user's name, id and link.
	seFilter = "!*LhrqUZrT(Hy7EQUpnoBTvH*5RLAP3Xb7u_609axG2a2vmoyazJbzx6BG92HxtMI5"
	// seAnswerTag is the element that keeps one API answer in the page.
	seAnswerTag = "harvester-stackexchange-answer"
	// seThrottleViolation is the API's error_id for an exhausted quota or too
	// many requests.
	seThrottleViolation = 502
)

// The kinds of API answer the extractor keeps.
const (
	seKindQuestion = "question"
	seKindAnswers  = "answers"
)

// stackExchangeHosts are the network's domains; every site is one of them or
// a subdomain (math.stackexchange.com, ru.stackoverflow.com, meta.…).
var stackExchangeHosts = []string{
	"stackoverflow.com", "stackexchange.com", "superuser.com", "serverfault.com",
	"askubuntu.com", "mathoverflow.net", "stackapps.com",
}

// seQuestionPathRe reads a question's address: /questions/<id> or /q/<id>,
// with up to two segments after it (a slug, an answer id or a user id).
var seQuestionPathRe = regexp.MustCompile(`^/(?:questions|q)/(\d+)(?:/[^/]+){0,2}/?$`)

// seQuestionRef is the question a page is: its site's host (the API's site
// parameter) and its id.
type seQuestionRef struct {
	site string
	id   int64
}

// seQuestionOf reads page's address as a Stack Exchange question's; false for
// any other address (a tag listing, a user, the network's own hub and API).
func seQuestionOf(page *url.URL) (seQuestionRef, bool) {
	host := strings.TrimPrefix(strings.ToLower(page.Hostname()), "www.")
	owned := false
	for _, domain := range stackExchangeHosts {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			owned = true
			break
		}
	}
	if !owned || host == "stackexchange.com" || host == "api.stackexchange.com" ||
		strings.HasPrefix(host, "chat.") {
		return seQuestionRef{}, false
	}
	match := seQuestionPathRe.FindStringSubmatch(page.Path)
	if match == nil {
		return seQuestionRef{}, false
	}
	id, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil || id <= 0 {
		return seQuestionRef{}, false
	}
	return seQuestionRef{site: host, id: id}, true
}

func (ref seQuestionRef) questionURL() string {
	return "https://" + ref.site + "/questions/" + strconv.FormatInt(ref.id, 10)
}

// target is the API address of the question's record (number 0) or of one
// page of its answers.
func (ref seQuestionRef) target(number int) string {
	query := url.Values{"site": {ref.site}, "filter": {seFilter}}
	path := "/questions/" + strconv.FormatInt(ref.id, 10)
	if number > 0 {
		path += "/answers"
		query.Set("pagesize", strconv.Itoa(sePageSize))
		query.Set("page", strconv.Itoa(number))
		query.Set("sort", "creation")
		query.Set("order", "asc")
	}
	return seAPI + path + "?" + query.Encode()
}

type seOwner struct {
	DisplayName string `json:"display_name"`
	UserID      int64  `json:"user_id"`
}

type seComment struct {
	CommentID    int64    `json:"comment_id"`
	PostID       int64    `json:"post_id"`
	Body         string   `json:"body"`
	Score        int      `json:"score"`
	CreationDate int64    `json:"creation_date"`
	Owner        *seOwner `json:"owner"`
}

type seAnswer struct {
	AnswerID     int64       `json:"answer_id"`
	QuestionID   int64       `json:"question_id"`
	Body         string      `json:"body"`
	Score        int         `json:"score"`
	CreationDate int64       `json:"creation_date"`
	Owner        *seOwner    `json:"owner"`
	IsAccepted   bool        `json:"is_accepted"`
	CommentCount *int        `json:"comment_count"`
	Comments     []seComment `json:"comments"`
}

type seQuestion struct {
	QuestionID       int64       `json:"question_id"`
	Title            string      `json:"title"`
	Link             string      `json:"link"`
	Body             string      `json:"body"`
	Score            int         `json:"score"`
	CreationDate     int64       `json:"creation_date"`
	Owner            *seOwner    `json:"owner"`
	Tags             []string    `json:"tags"`
	ClosedReason     string      `json:"closed_reason"`
	AcceptedAnswerID int64       `json:"accepted_answer_id"`
	AnswerCount      *int        `json:"answer_count"`
	CommentCount     *int        `json:"comment_count"`
	Comments         []seComment `json:"comments"`
}

// seWrapper is the API's common answer: its items and, on an error, the
// error's id and name; backoff asks the next request to wait that many
// seconds; quota_remaining is the requests the address has left today (nil
// when the answer states none).
type seWrapper struct {
	Items          json.RawMessage `json:"items"`
	HasMore        bool            `json:"has_more"`
	Page           int             `json:"page"`
	Backoff        int             `json:"backoff"`
	QuotaRemaining *int            `json:"quota_remaining"`
	ErrorID        int             `json:"error_id"`
	ErrorName      string          `json:"error_name"`
}

// seAnswersPage is one kept page of the question's answers.
type seAnswersPage struct {
	answers []seAnswer
	hasMore bool
}

// seThread is what the page holds of a question's API answers.
type seThread struct {
	ref      seQuestionRef
	question *seQuestion
	pages    map[int]seAnswersPage
	dropped  map[string]bool
}

// seThreadOf reads the question page is and the API answers kept in it.
func seThreadOf(doc *html.Node, page *url.URL) (seThread, bool) {
	ref, ok := seQuestionOf(page)
	if !ok {
		return seThread{}, false
	}
	thread := seThread{ref: ref, pages: map[int]seAnswersPage{}, dropped: map[string]bool{}}
	for _, node := range keptAnswers(doc, seAnswerTag) {
		if nodeAttr(node, "dropped") != "" {
			thread.dropped[nodeAttr(node, "key")] = true
			continue
		}
		kind := nodeAttr(node, "kind")
		var wrapper seWrapper
		err := json.Unmarshal([]byte(rawText(node)), &wrapper)
		if err == nil {
			switch kind {
			case seKindQuestion:
				var questions []seQuestion
				if err = json.Unmarshal(wrapper.Items, &questions); err == nil && len(questions) > 0 {
					thread.question = &questions[0]
				}
			case seKindAnswers:
				var answers []seAnswer
				number, _ := strconv.Atoi(nodeAttr(node, "page"))
				if err = json.Unmarshal(wrapper.Items, &answers); err == nil {
					thread.pages[number] = seAnswersPage{answers: answers, hasMore: wrapper.HasMore}
				}
			}
		}
		if err != nil {
			// graft kept only answers that decoded; one that no longer does is
			// a bug, logged and left out (the reconciliation names what is
			// missing).
			obs.Logger(context.Background()).Warn("harvest: a kept Stack Exchange answer no longer decodes; left out",
				"kind", kind, obs.FieldErr, err.Error())
		}
	}
	return thread, true
}

// seWanted is one API answer the thread still lacks: the question's record
// (number 0) or a page of its answers.
type seWanted struct {
	kind   string
	number int
	target string
	label  string
}

// wanted lists the answers the thread still lacks, in request order: the
// question's record, then its answer pages, each wanted once the one before
// it says more follow.
func (thread seThread) wanted() []seWanted {
	add := func(out []seWanted, kind string, number int, label string) []seWanted {
		target := thread.ref.target(number)
		if thread.dropped[seKey(target)] {
			return out
		}
		return append(out, seWanted{kind: kind, number: number, target: target, label: label})
	}
	if thread.question == nil {
		return add(nil, seKindQuestion, 0, "the question's API record")
	}
	if *thread.question.AnswerCount == 0 {
		return nil
	}
	for number := 1; ; number++ {
		page, kept := thread.pages[number]
		if !kept {
			return add(nil, seKindAnswers, number, fmt.Sprintf("answers page %d", number))
		}
		if !page.hasMore {
			return nil
		}
	}
}

func seKey(target string) string { return "stackexchange-api " + target }

// stackExchangeLoaders names the API answers the question still lacks, for
// loaders.go.
func stackExchangeLoaders(doc *html.Node, page *url.URL) []pageLoader {
	thread, ok := seThreadOf(doc, page)
	if !ok {
		return nil
	}
	var loaders []pageLoader
	for _, want := range thread.wanted() {
		loaders = append(loaders, stackExchangeLoader(doc, thread, want))
	}
	return loaders
}

// seRateLimited reads an error answer as the API's throttle: error_id 502,
// whatever the HTTP status it came with.
func seRateLimited(_ int, body []byte) bool {
	var wrapper seWrapper
	return json.Unmarshal(body, &wrapper) == nil && wrapper.ErrorID == seThrottleViolation
}

// seBackoff reads the back-off an answer asks of the next request.
func seBackoff(body []byte) time.Duration {
	var wrapper seWrapper
	if json.Unmarshal(body, &wrapper) != nil || wrapper.Backoff <= 0 {
		return 0
	}
	return time.Duration(wrapper.Backoff) * time.Second
}

// seQuotaSpent reads an answer stating the address's daily quota spent
// (quota_remaining 0); an answer stating no quota is not spent.
func seQuotaSpent(body []byte) bool {
	var wrapper seWrapper
	return json.Unmarshal(body, &wrapper) == nil && wrapper.QuotaRemaining != nil && *wrapper.QuotaRemaining <= 0
}

// stackExchangeLoader requests one API answer and keeps it in the page once
// it proves itself this question's.
func stackExchangeLoader(doc *html.Node, thread seThread, want seWanted) pageLoader {
	key := seKey(want.target)
	return pageLoader{
		key:         key,
		label:       want.label,
		method:      http.MethodGet,
		target:      want.target,
		headers:     map[string]string{headerAccept: "application/json"},
		rateLimited: seRateLimited,
		backoff:     seBackoff,
		quotaSpent:  seQuotaSpent,
		graft: func(body []byte, contentType string) error {
			if err := thread.check(want, body, contentType); err != nil {
				return err
			}
			keepAnswer(doc, seAnswerTag, key, want.kind, want.number, body)
			return nil
		},
		drop: func() { keepAnswer(doc, seAnswerTag, key, want.kind, want.number, nil) },
	}
}

// check proves an API answer is the one want asked for, of this question.
func (thread seThread) check(want seWanted, body []byte, contentType string) error {
	notJSON := fmt.Errorf("answered by a %s body that is not the API's JSON for %s",
		discourseContentType(contentType), want.label)
	var wrapper seWrapper
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return notJSON
	}
	if wrapper.ErrorID != 0 {
		return fmt.Errorf("answered by the API's error %d (%s)", wrapper.ErrorID, wrapper.ErrorName)
	}
	id := thread.ref.id
	if want.kind == seKindQuestion {
		var questions []seQuestion
		switch err := json.Unmarshal(wrapper.Items, &questions); {
		case err != nil:
			return notJSON
		case len(questions) == 0:
			return fmt.Errorf("answered by no question #%d on %s (deleted, or not on this site)", id, thread.ref.site)
		case questions[0].QuestionID != id:
			return fmt.Errorf("answered by the record of question #%d, not #%d", questions[0].QuestionID, id)
		case questions[0].AnswerCount == nil || questions[0].CommentCount == nil:
			return fmt.Errorf("answered by a record of #%d stating no answer or comment count", id)
		}
		return nil
	}
	var answers []seAnswer
	if err := json.Unmarshal(wrapper.Items, &answers); err != nil {
		return notJSON
	}
	if wrapper.Page != want.number {
		return fmt.Errorf("answered by answers page %d, not page %d", wrapper.Page, want.number)
	}
	for index := range answers {
		if answers[index].AnswerID == 0 || answers[index].QuestionID != id {
			return fmt.Errorf("answered by a list holding an answer (#%d) of another question (#%d)",
				answers[index].AnswerID, answers[index].QuestionID)
		}
	}
	return nil
}

// sePosted renders an API time (Unix seconds) as "2006-01-02 15:04 UTC".
func sePosted(seconds int64) string {
	return time.Unix(seconds, 0).UTC().Format("2006-01-02 15:04 UTC")
}

// seAuthor is a post's or comment's author; the API escapes names as HTML.
func seAuthor(owner *seOwner) string {
	if owner == nil || owner.DisplayName == "" {
		return unknownAuthor
	}
	return html.UnescapeString(owner.DisplayName)
}

// seMarkdown renders an API body (HTML) as Markdown blocks.
func seMarkdown(body string, renderer markdownRenderer) []string {
	container := &html.Node{Type: html.ElementNode, Data: divTag, DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(body), container)
	if err != nil {
		// x/net/html recovers from any malformed markup; an error here is a
		// reader failure, named in the rendering rather than dropped.
		obs.Logger(context.Background()).Warn("harvest: a Stack Exchange body did not parse; rendered as text",
			obs.FieldErr, err.Error())
		return []string{html.UnescapeString(body)}
	}
	for _, node := range nodes {
		container.AppendChild(node)
	}
	return renderer.blocks(container)
}

// seComments renders a post's comments, oldest first, each with its author,
// time, score and permalink.
func seComments(out *strings.Builder, comments []seComment, site string, renderer markdownRenderer, indent string) {
	sorted := append([]seComment(nil), comments...)
	sort.SliceStable(sorted, func(a, b int) bool {
		if sorted[a].CreationDate != sorted[b].CreationDate {
			return sorted[a].CreationDate < sorted[b].CreationDate
		}
		return sorted[a].CommentID < sorted[b].CommentID
	})
	for _, comment := range sorted {
		id := strconv.FormatInt(comment.CommentID, 10)
		out.WriteString(indent + "- **" + seAuthor(comment.Owner) + "** · " + sePosted(comment.CreationDate) +
			" · score " + strconv.Itoa(comment.Score) + " · [comment #" + id + "](https://" + site +
			"/posts/comments/" + id + ")\n")
		if text := strings.Join(seMarkdown(comment.Body, renderer), "\n\n"); text != "" {
			out.WriteString(prefixLines(text, indent+"  ", "") + "\n")
		}
	}
}

// seCommentGap names a post whose loaded comments fall short of its stated
// count, or pass it; "" when they agree.
func seCommentGap(post string, stated, loaded int) string {
	switch {
	case loaded < stated:
		return fmt.Sprintf("%s: %d stated comment(s) · %d loaded (%d not in the API's list — deleted since "+
			"the count)", post, stated, loaded, stated-loaded)
	case loaded > stated:
		return fmt.Sprintf("%s: %d stated comment(s) · %d loaded · %d more loaded than stated",
			post, stated, loaded, loaded-stated)
	}
	return ""
}

// extractStackExchangeQuestion renders a Stack Exchange question from the API
// answers kept in its page: its header, the count line reconciling what was
// loaded against the stated counts, its body and comments, then every answer
// by score with its accepted mark, author, time and comments.
func extractStackExchangeQuestion(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	thread, ok := seThreadOf(doc, page)
	if !ok {
		return siteExtraction{}, false
	}
	question := thread.question
	if question == nil {
		// Nothing of the question is proved without its record: the page goes
		// on down the ladder (a wall is refused, the browser rung may pass it),
		// this gap named in whatever path renders it.
		return siteExtraction{unrendered: "stackexchange question: the question's API record was not loaded " +
			"(its answers, comments and stated counts not read from the API; the page is stored as rendered)"}, false
	}
	var out strings.Builder
	base, err := url.Parse(thread.ref.questionURL())
	if err != nil {
		base = page
	}
	renderer := markdownRenderer{base: base}

	var answers []seAnswer
	var gaps []string
	seen := map[int64]bool{}
	for number := 1; ; number++ {
		kept, ok := thread.pages[number]
		if !ok {
			if *question.AnswerCount > 0 {
				gaps = append(gaps, fmt.Sprintf("answers page %d not loaded (answers from #%d on, and their "+
					"comments, not read)", number, (number-1)*sePageSize+1))
			}
			break
		}
		for _, answer := range kept.answers {
			if !seen[answer.AnswerID] {
				seen[answer.AnswerID] = true
				answers = append(answers, answer)
			}
		}
		if !kept.hasMore {
			break
		}
	}
	stated := *question.AnswerCount
	answerLine := fmt.Sprintf("%d stated · %d loaded", stated, len(answers))
	switch rest := stated - len(answers); {
	case rest > 0 && len(gaps) == 0:
		gaps = append(gaps, fmt.Sprintf("%d stated answer(s) not in the API's list (deleted since the count)", rest))
	case rest < 0:
		answerLine += fmt.Sprintf(" · %d more loaded than stated", -rest)
	}

	commentsStated := *question.CommentCount
	commentsLoaded := len(question.Comments)
	if gap := seCommentGap("the question", *question.CommentCount, len(question.Comments)); gap != "" {
		gaps = append(gaps, gap)
	}
	// The stated and loaded totals cover the same posts: an answer whose count
	// was not read is left out of both, its comments counted apart.
	uncountedPosts, uncountedComments := 0, 0
	for _, answer := range answers {
		post := "answer #" + strconv.FormatInt(answer.AnswerID, 10)
		if answer.CommentCount == nil {
			gaps = append(gaps, post+": the stated comment count was not read")
			uncountedPosts++
			uncountedComments += len(answer.Comments)
			continue
		}
		commentsLoaded += len(answer.Comments)
		commentsStated += *answer.CommentCount
		if gap := seCommentGap(post, *answer.CommentCount, len(answer.Comments)); gap != "" {
			gaps = append(gaps, gap)
		}
	}
	countLine := "**Answers:** " + answerLine + " · **Comments:** " +
		fmt.Sprintf("%d stated · %d loaded, on the question and the answers loaded", commentsStated, commentsLoaded)
	if uncountedPosts > 0 {
		countLine += fmt.Sprintf(" (and %d loaded on %d answer(s) whose count was not read)",
			uncountedComments, uncountedPosts)
	}
	if len(gaps) > 0 {
		countLine += " · gaps: " + strings.Join(gaps, "; ")
	}

	out.WriteString("# " + html.UnescapeString(question.Title) + "\n\n")
	meta := []string{
		"**Asked by:** " + seAuthor(question.Owner),
		"**Asked:** " + sePosted(question.CreationDate),
		"**Score:** " + strconv.Itoa(question.Score),
	}
	if len(question.Tags) > 0 {
		meta = append(meta, "**Tags:** "+html.UnescapeString(strings.Join(question.Tags, ", ")))
	}
	if question.ClosedReason != "" {
		meta = append(meta, "**Closed:** "+html.UnescapeString(question.ClosedReason))
	}
	out.WriteString(strings.Join(meta, " · ") + "  \n")
	link := question.Link
	if link == "" {
		link = thread.ref.questionURL()
	}
	out.WriteString("**Question:** " + link + "  \n")
	out.WriteString(countLine + "\n\n")
	if body := strings.Join(seMarkdown(question.Body, renderer), "\n\n"); body != "" {
		out.WriteString(body + "\n\n")
	}
	if len(question.Comments) > 0 {
		out.WriteString("**Comments on the question:**\n\n")
		seComments(&out, question.Comments, thread.ref.site, renderer, "")
		out.WriteString("\n")
	}
	out.WriteString("---\n\n## Answers\n\n")
	if len(answers) == 0 {
		out.WriteString("*No answers are loaded.*\n")
	}
	sort.SliceStable(answers, func(a, b int) bool {
		if answers[a].Score != answers[b].Score {
			return answers[a].Score > answers[b].Score
		}
		return answers[a].CreationDate < answers[b].CreationDate
	})
	for _, answer := range answers {
		id := strconv.FormatInt(answer.AnswerID, 10)
		header := "### Answer [#" + id + "](https://" + thread.ref.site + "/a/" + id + ") · **" +
			seAuthor(answer.Owner) + "** · " + sePosted(answer.CreationDate) + " · score " + strconv.Itoa(answer.Score)
		if answer.IsAccepted {
			header += " · **accepted**"
		}
		out.WriteString(header + "\n\n")
		if body := strings.Join(seMarkdown(answer.Body, renderer), "\n\n"); body != "" {
			out.WriteString(body + "\n\n")
		}
		if len(answer.Comments) > 0 {
			seComments(&out, answer.Comments, thread.ref.site, renderer, "")
			out.WriteString("\n")
		}
	}

	partial := ""
	if len(gaps) > 0 {
		partial = fmt.Sprintf("stackexchange question: %d of %d answers, %d of %d stated comments loaded — %s",
			len(answers), stated, commentsLoaded, commentsStated, strings.Join(gaps, "; "))
	}
	return siteExtraction{markdown: out.String(), partial: partial, apiRecord: true}, true
}
