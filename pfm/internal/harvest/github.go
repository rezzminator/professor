package harvest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The GitHub issue and pull request extractor. Neither page carries its whole
// conversation: an issue page (github.com/<owner>/<repo>/issues/<n>) is
// rendered in the browser from an embedded JSON payload holding only the
// first and last few comments, the rest fetched by GraphQL persisted queries
// whose ids GitHub rotates; a pull request's conversation page is rendered on
// the server but folds its middle behind a "Load more…" hidden-items form.
// Neither is a loader Go can follow stably, so the extractor reads the thread
// from GitHub's public REST API instead (api.github.com, a subdomain the
// extractor owns), unauthenticated — no credential is ever sent — through
// loaders.go's budget like any loader: the issue's record (title, body,
// author, state and the stated comment count), then every page of its
// comments (per_page=100); for a pull request also its record (the stated
// count of review comments on diff lines), every page of those review
// comments, and its reviews, read until a short page ends the list (the API
// states no review count). Each answer is checked to be this thread's before
// it is kept in the page as a harvester-github-answer element, so a later
// conversion (the browser rung's page) replays it like any followed loader.
// The unauthenticated API allows 60 requests an hour per address and answers
// its exhaustion with 403 (or 429) and "rate limit" in the body: that ends
// the following, named, never retried. A thread whose own record never loaded
// is not claimed: the page GitHub served goes the generic path, the record's
// gap named in its partial marker. The extractor otherwise renders the thread
// in time order — comments, review comments and reviews merged — and
// reconciles what it loaded against the stated counts: a page not loaded, a
// stated comment the API did not list, and a count not read each flag the
// artifact partial and are named.

const (
	githubHost = "github.com"
	// githubAPI is the REST API's origin; githubPerPage the page size asked
	// for, the API's maximum.
	githubAPI     = "https://api.github.com"
	githubPerPage = 100
	// githubAnswerTag is the element that keeps one API answer in the page.
	githubAnswerTag = "harvester-github-answer"
	// githubAPIVersion pins the REST API's version the answers are read by.
	githubAPIVersion = "2022-11-28"
	// githubPullSegment is the path segment of a pull request's address.
	githubPullSegment = "pull"
)

// The kinds of API answer the extractor keeps.
const (
	githubKindIssue          = "issue"
	githubKindComments       = "comments"
	githubKindPull           = "pull-request"
	githubKindReviewComments = "review-comments"
	githubKindReviews        = "reviews"
)

// githubItem is the issue or pull request a page is: its owner, repository
// and number as the page's canonical address (og:url) names them.
type githubItem struct {
	owner, repo string
	number      int
	pull        bool
}

func (item githubItem) threadURL() string {
	kind := "issues"
	if item.pull {
		kind = githubPullSegment
	}
	return "https://" + githubHost + "/" + item.owner + "/" + item.repo + "/" + kind + "/" + strconv.Itoa(item.number)
}

func (item githubItem) apiBase() string {
	return githubAPI + "/repos/" + url.PathEscape(item.owner) + "/" + url.PathEscape(item.repo)
}

// githubItemPath reads an issue or pull request conversation address:
// /<owner>/<repo>/(issues|pull)/<n>, a trailing slash allowed.
func githubItemPath(path string) (githubItem, bool) {
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(path, "/"), "/"), "/")
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" || (parts[2] != "issues" && parts[2] != githubPullSegment) {
		return githubItem{}, false
	}
	number, err := strconv.Atoi(parts[3])
	if err != nil || number <= 0 {
		return githubItem{}, false
	}
	return githubItem{owner: parts[0], repo: parts[1], number: number, pull: parts[2] == githubPullSegment}, true
}

// githubItemOf reads doc as the page of a GitHub issue or pull request's
// conversation: the requested address is one, and the page's og:url names the
// same number (an issue address answered by its pull request's page, or a
// moved repository's, is the item the page names). false for any other page
// (a repository, a pull request's files, a sign-in wall, a 404).
func githubItemOf(doc *html.Node, page *url.URL) (githubItem, bool) {
	requested, ok := githubItemPath(page.Path)
	if !ok {
		return githubItem{}, false
	}
	meta := firstWithAttr(doc, "property", "og:url", nil)
	if meta == nil || meta.DataAtom != atom.Meta {
		return githubItem{}, false
	}
	canonical, err := url.Parse(strings.TrimSpace(nodeAttr(meta, "content")))
	if err != nil || !strings.EqualFold(canonical.Hostname(), githubHost) {
		return githubItem{}, false
	}
	item, ok := githubItemPath(canonical.Path)
	if !ok || item.number != requested.number {
		return githubItem{}, false
	}
	return item, true
}

type githubUser struct {
	Login string `json:"login"`
}

type githubIssue struct {
	URL       string      `json:"url"`
	HTMLURL   string      `json:"html_url"`
	Number    int         `json:"number"`
	Title     string      `json:"title"`
	State     string      `json:"state"`
	Body      *string     `json:"body"`
	User      *githubUser `json:"user"`
	CreatedAt string      `json:"created_at"`
	Comments  *int        `json:"comments"`
	// PullRequest is set when the issue is a pull request.
	PullRequest *struct {
		URL string `json:"url"`
	} `json:"pull_request"`
}

type githubPull struct {
	URL            string `json:"url"`
	Number         int    `json:"number"`
	ReviewComments *int   `json:"review_comments"`
}

// githubEntry is one comment, review comment or review as the API lists it.
type githubEntry struct {
	ID        int64       `json:"id"`
	HTMLURL   string      `json:"html_url"`
	User      *githubUser `json:"user"`
	Body      *string     `json:"body"`
	CreatedAt string      `json:"created_at"`
	// IssueURL is a comment's issue; PullRequestURL a review comment's or
	// review's pull request.
	IssueURL       string `json:"issue_url"`
	PullRequestURL string `json:"pull_request_url"`
	// Minimized is set on a comment hidden by a maintainer.
	Minimized *struct {
		Reason string `json:"reason"`
	} `json:"minimized"`
	// A review comment's file and line, and the comment it answers.
	Path         string `json:"path"`
	Line         *int   `json:"line"`
	OriginalLine *int   `json:"original_line"`
	InReplyToID  int64  `json:"in_reply_to_id"`
	// A review's verdict and time.
	State       string `json:"state"`
	SubmittedAt string `json:"submitted_at"`
}

// githubThread is what the page holds of a thread's API answers.
type githubThread struct {
	item  githubItem
	issue *githubIssue
	pull  *githubPull
	// pages holds each kind's listed pages by page number.
	pages map[string]map[int][]githubEntry
	// dropped holds the keys of loaders dropped without an answer kept.
	dropped map[string]bool
}

// githubThreadOf reads the thread doc is and the API answers kept in it.
func githubThreadOf(doc *html.Node, page *url.URL) (githubThread, bool) {
	item, ok := githubItemOf(doc, page)
	if !ok {
		return githubThread{}, false
	}
	thread := githubThread{item: item, pages: map[string]map[int][]githubEntry{}, dropped: map[string]bool{}}
	for _, node := range keptAnswers(doc, githubAnswerTag) {
		if nodeAttr(node, "dropped") != "" {
			thread.dropped[nodeAttr(node, "key")] = true
			continue
		}
		body := []byte(rawText(node))
		kind := nodeAttr(node, "kind")
		var err error
		switch kind {
		case githubKindIssue:
			var issue githubIssue
			if err = json.Unmarshal(body, &issue); err == nil {
				thread.issue = &issue
			}
		case githubKindPull:
			var pull githubPull
			if err = json.Unmarshal(body, &pull); err == nil {
				thread.pull = &pull
			}
		default:
			var entries []githubEntry
			number, _ := strconv.Atoi(nodeAttr(node, "page"))
			if err = json.Unmarshal(body, &entries); err == nil {
				if thread.pages[kind] == nil {
					thread.pages[kind] = map[int][]githubEntry{}
				}
				thread.pages[kind][number] = entries
			}
		}
		if err != nil {
			// graft kept only answers that decoded; one that no longer does is
			// a bug, logged and left out (the reconciliation names what is
			// missing).
			obs.Logger(context.Background()).Warn("harvest: a kept GitHub answer no longer decodes; left out",
				"kind", kind, obs.FieldErr, err.Error())
		}
	}
	return thread, true
}

// githubPageCount is how many pages of githubPerPage list count entries.
func githubPageCount(count int) int {
	return (count + githubPerPage - 1) / githubPerPage
}

// githubListTarget is the address of one page of a list under the API base.
func githubListTarget(base, path string, number int) string {
	return fmt.Sprintf("%s%s?per_page=%d&page=%d", base, path, githubPerPage, number)
}

// githubWanted is one API answer the thread still lacks.
type githubWanted struct {
	kind   string
	number int
	target string
	label  string
}

// wanted lists the answers the thread still lacks, in the order they are
// requested: the issue's record, its comment pages, then for a pull request
// its record, its review comment pages and its review pages. A page that
// depends on a record not yet kept (its count) is not wanted yet.
func (thread githubThread) wanted() []githubWanted {
	base := thread.item.apiBase()
	issueNumber := strconv.Itoa(thread.item.number)
	var out []githubWanted
	add := func(kind string, number int, target, label string) {
		if thread.dropped[githubKey(target)] {
			return
		}
		if number > 0 {
			if _, kept := thread.pages[kind][number]; kept {
				return
			}
		}
		out = append(out, githubWanted{kind: kind, number: number, target: target, label: label})
	}
	if thread.issue == nil {
		add(githubKindIssue, 0, base+"/issues/"+issueNumber, "the issue's API record")
		return out
	}
	if thread.issue.Comments != nil {
		pages := githubPageCount(*thread.issue.Comments)
		for number := 1; number <= pages; number++ {
			add(githubKindComments, number, githubListTarget(base, "/issues/"+issueNumber+"/comments", number),
				fmt.Sprintf("comments page %d of %d", number, pages))
		}
	}
	if thread.issue.PullRequest == nil {
		return out
	}
	if thread.pull == nil {
		add(githubKindPull, 0, base+"/pulls/"+issueNumber, "the pull request's API record")
		return out
	}
	if thread.pull.ReviewComments != nil {
		pages := githubPageCount(*thread.pull.ReviewComments)
		for number := 1; number <= pages; number++ {
			add(githubKindReviewComments, number, githubListTarget(base, "/pulls/"+issueNumber+"/comments", number),
				fmt.Sprintf("review comments page %d of %d", number, pages))
		}
	}
	// Reviews have no stated count: the list ends at its first short page.
	for number := 1; ; number++ {
		entries, kept := thread.pages[githubKindReviews][number]
		if !kept {
			add(githubKindReviews, number, githubListTarget(base, "/pulls/"+issueNumber+"/reviews", number),
				fmt.Sprintf("reviews page %d", number))
			break
		}
		if len(entries) < githubPerPage {
			break
		}
	}
	return out
}

func githubKey(target string) string { return "github-api " + target }

// githubLoaders names the API answers the thread still lacks, for loaders.go.
func githubLoaders(doc *html.Node, page *url.URL) []pageLoader {
	thread, ok := githubThreadOf(doc, page)
	if !ok {
		return nil
	}
	var loaders []pageLoader
	for _, want := range thread.wanted() {
		loaders = append(loaders, githubLoader(doc, thread, want))
	}
	return loaders
}

// githubRateLimited reads an error answer as the API's rate limit: 403 or 429
// with "rate limit" in its message (primary or secondary limit).
func githubRateLimited(status int, body []byte) bool {
	return (status == http.StatusForbidden || status == http.StatusTooManyRequests) &&
		bytes.Contains(bytes.ToLower(body), []byte("rate limit"))
}

// githubLoader requests one API answer and keeps it in the page once it
// proves itself this thread's.
func githubLoader(doc *html.Node, thread githubThread, want githubWanted) pageLoader {
	key := githubKey(want.target)
	return pageLoader{
		key:    key,
		label:  want.label,
		method: http.MethodGet,
		target: want.target,
		headers: map[string]string{
			headerAccept:           "application/vnd.github+json",
			"X-GitHub-Api-Version": githubAPIVersion,
		},
		rateLimited: githubRateLimited,
		graft: func(body []byte, contentType string) error {
			if err := thread.check(want, body, contentType); err != nil {
				return err
			}
			keepAnswer(doc, githubAnswerTag, key, want.kind, want.number, body)
			return nil
		},
		drop: func() { keepAnswer(doc, githubAnswerTag, key, want.kind, want.number, nil) },
	}
}

// check proves an API answer is the one want asked for, of this thread.
func (thread githubThread) check(want githubWanted, body []byte, contentType string) error {
	notJSON := fmt.Errorf("answered by a %s body that is not the API's JSON for %s",
		discourseContentType(contentType), want.label)
	switch want.kind {
	case githubKindIssue:
		var issue githubIssue
		if err := json.Unmarshal(body, &issue); err != nil || issue.Number == 0 {
			return notJSON
		}
		if issue.Number != thread.item.number {
			return fmt.Errorf("answered by the record of #%d, not #%d", issue.Number, thread.item.number)
		}
		if issue.Comments == nil {
			return fmt.Errorf("answered by a record of #%d stating no comment count", issue.Number)
		}
		if issue.URL == "" {
			return fmt.Errorf("answered by a record of #%d naming no API address, not verifiable", issue.Number)
		}
		return nil
	case githubKindPull:
		var pull githubPull
		if err := json.Unmarshal(body, &pull); err != nil || pull.Number == 0 {
			return notJSON
		}
		switch {
		case thread.issue == nil || thread.issue.PullRequest == nil || pull.URL != thread.issue.PullRequest.URL:
			return fmt.Errorf("answered by the record of another pull request (#%d)", pull.Number)
		case pull.ReviewComments == nil:
			return fmt.Errorf("answered by a record of #%d stating no review comment count", pull.Number)
		}
		return nil
	}
	var entries []githubEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return notJSON
	}
	if len(entries) == 0 && (want.kind != githubKindReviews || want.number > 1) {
		return fmt.Errorf(
			"answered by an empty page (entries deleted since the count, or the list shorter than stated)",
		)
	}
	owner := ""
	switch {
	case want.kind == githubKindComments && thread.issue != nil:
		owner = thread.issue.URL
	case thread.pull != nil:
		owner = thread.pull.URL
	}
	for index := range entries {
		entry := &entries[index]
		of := entry.PullRequestURL
		if want.kind == githubKindComments {
			of = entry.IssueURL
		}
		if entry.ID == 0 || owner == "" || of != owner {
			return fmt.Errorf("answered by a list holding an entry (#%d) of another thread", entry.ID)
		}
	}
	return nil
}

// githubPosted renders an API time ("2025-09-24T05:51:22Z") as "2006-01-02
// 15:04 UTC"; the raw text when unreadable.
func githubPosted(raw string) string {
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return raw
	}
	return parsed.UTC().Format("2006-01-02 15:04 UTC")
}

func githubAuthor(user *githubUser) string {
	if user == nil || user.Login == "" {
		return unknownAuthor
	}
	return user.Login
}

// githubText is an API body as Markdown: line endings normalized, trimmed.
func githubText(body *string) string {
	if body == nil {
		return ""
	}
	return strings.TrimSpace(strings.ReplaceAll(*body, "\r\n", "\n"))
}

// githubTimelineEntry is one rendered entry of the conversation.
type githubTimelineEntry struct {
	at     string
	order  int
	header string
	body   string
}

// listed gathers the kept pages 1..pages of one kind: their entries in page
// order, one per id, and the page numbers not kept.
func (thread githubThread) listed(kind string, pages int) (entries []githubEntry, missing []int) {
	seen := map[int64]bool{}
	for number := 1; number <= pages; number++ {
		page, kept := thread.pages[kind][number]
		if !kept {
			missing = append(missing, number)
			continue
		}
		for index := range page {
			if !seen[page[index].ID] {
				seen[page[index].ID] = true
				entries = append(entries, page[index])
			}
		}
	}
	return entries, missing
}

// reviews gathers the kept review pages up to the list's end, a short page;
// gap names the first page not kept before that end ("" when the list was
// read to its end).
func (thread githubThread) reviews() (entries []githubEntry, gap string) {
	for number := 1; ; number++ {
		page, kept := thread.pages[githubKindReviews][number]
		if !kept {
			return entries, fmt.Sprintf("reviews page %d not loaded (the list not read to its end)", number)
		}
		entries = append(entries, page...)
		if len(page) < githubPerPage {
			return entries, ""
		}
	}
}

// githubReconcile names the gap between a stated count and what was loaded.
func githubReconcile(noun string, stated *int, loaded int, missing []int, pages int, gaps *[]string) string {
	for _, number := range missing {
		first := (number-1)*githubPerPage + 1
		last := number * githubPerPage
		if stated != nil && last > *stated {
			last = *stated
		}
		*gaps = append(
			*gaps,
			fmt.Sprintf("%s page %d of %d (%s %d–%d) not loaded", noun, number, pages, noun, first, last),
		)
	}
	if stated == nil {
		*gaps = append(*gaps, "the stated "+noun+" count was not read")
		return fmt.Sprintf("count not read · %d loaded", loaded)
	}
	rest := *stated - loaded
	switch {
	case rest > 0 && len(missing) == 0:
		*gaps = append(*gaps, fmt.Sprintf("%d stated %s(s) not in the API's list (deleted since the count, "+
			"or shifted past a page boundary by a deletion while paging)", rest, noun))
	case rest < 0:
		return fmt.Sprintf("%d stated · %d loaded · %d more loaded than stated", *stated, loaded, -rest)
	}
	return fmt.Sprintf("%d stated · %d loaded", *stated, loaded)
}

// extractGitHubIssue renders a GitHub issue or pull request from the API
// answers kept in its page: its header, the count line reconciling what was
// loaded against the stated counts, its body, and the conversation in time
// order with each entry's author and time.
func extractGitHubIssue(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	thread, ok := githubThreadOf(doc, page)
	if !ok {
		return siteExtraction{}, false
	}
	noun := "issue"
	if thread.item.pull {
		noun = "pull request"
	}
	issue := thread.issue
	if issue == nil {
		// Nothing of the thread is proved without its record: the page GitHub
		// served goes the generic path, this gap named in its partial marker.
		return siteExtraction{unrendered: "github " + noun + ": the " + noun + "'s API record was not loaded " +
			"(its comments not read from the API; the page is stored as GitHub served it)"}, false
	}
	var gaps []string
	var out strings.Builder
	if issue.PullRequest != nil {
		noun = "pull request"
	}
	commentPages := githubPageCount(*issue.Comments)
	comments, missing := thread.listed(githubKindComments, commentPages)
	countLine := "**Comments:** " + githubReconcile(
		"comment",
		issue.Comments,
		len(comments),
		missing,
		commentPages,
		&gaps,
	)

	var timeline []githubTimelineEntry
	for index := range comments {
		comment := &comments[index]
		header := "**" + githubAuthor(comment.User) + "** · " + githubPosted(comment.CreatedAt) +
			" · [#" + strconv.FormatInt(comment.ID, 10) + "](" + comment.HTMLURL + ")"
		if comment.Minimized != nil {
			header += " · *hidden by a maintainer (" + comment.Minimized.Reason + ")*"
		}
		timeline = append(
			timeline,
			githubTimelineEntry{at: comment.CreatedAt, header: header, body: githubText(comment.Body)},
		)
	}
	if issue.PullRequest != nil {
		if thread.pull == nil {
			gaps = append(
				gaps,
				"the pull request's API record was not loaded (its stated review comment count not read)",
			)
			countLine += " · **Review comments:** count not read · 0 loaded"
		} else {
			reviewPages := githubPageCount(*thread.pull.ReviewComments)
			reviewComments, missingReview := thread.listed(githubKindReviewComments, reviewPages)
			countLine += " · **Review comments:** " + githubReconcile("review comment", thread.pull.ReviewComments,
				len(reviewComments), missingReview, reviewPages, &gaps)
			for index := range reviewComments {
				comment := &reviewComments[index]
				header := "**" + githubAuthor(comment.User) + "** · " + githubPosted(comment.CreatedAt) +
					" · review comment on `" + comment.Path + "`"
				if line := comment.Line; line != nil || comment.OriginalLine != nil {
					if line == nil {
						line = comment.OriginalLine
					}
					header += " line " + strconv.Itoa(*line)
				}
				if comment.InReplyToID != 0 {
					header += " · reply to #" + strconv.FormatInt(comment.InReplyToID, 10)
				}
				header += " · [#" + strconv.FormatInt(comment.ID, 10) + "](" + comment.HTMLURL + ")"
				timeline = append(timeline, githubTimelineEntry{
					at: comment.CreatedAt, order: 1, header: header, body: githubText(comment.Body),
				})
			}
			reviews, reviewsGap := thread.reviews()
			if reviewsGap != "" {
				gaps = append(gaps, reviewsGap)
			}
			shown := 0
			for index := range reviews {
				review := &reviews[index]
				body := githubText(review.Body)
				verdict := strings.ToLower(strings.ReplaceAll(review.State, "_", " "))
				if body == "" && review.State == "COMMENTED" {
					// An empty "commented" review only holds review comments,
					// rendered on their own.
					continue
				}
				shown++
				header := "**" + githubAuthor(review.User) + "** · " + githubPosted(review.SubmittedAt) +
					" · review: " + verdict + " · [#" + strconv.FormatInt(review.ID, 10) + "](" + review.HTMLURL + ")"
				timeline = append(
					timeline,
					githubTimelineEntry{at: review.SubmittedAt, order: 2, header: header, body: body},
				)
			}
			reach := "the list read to its end"
			if reviewsGap != "" {
				reach = "the list not read to its end"
			}
			countLine += fmt.Sprintf(" · **Reviews:** %d loaded, %s (the API states no review count; %d empty "+
				"\"commented\" review(s) holding only review comments, not shown)", len(reviews), reach, len(reviews)-shown)
		}
	}
	if len(gaps) > 0 {
		countLine += " · gaps: " + strings.Join(gaps, "; ")
	}
	sort.SliceStable(timeline, func(a, b int) bool {
		if timeline[a].at != timeline[b].at {
			return timeline[a].at < timeline[b].at
		}
		return timeline[a].order < timeline[b].order
	})

	title := issue.Title
	if title == "" {
		title = pageTitle(doc)
	}
	out.WriteString("# " + title + " (#" + strconv.Itoa(issue.Number) + ")\n\n")
	meta := []string{"**Author:** " + githubAuthor(issue.User)}
	if issue.CreatedAt != "" {
		meta = append(meta, "**Opened:** "+githubPosted(issue.CreatedAt))
	}
	if issue.State != "" {
		meta = append(meta, "**State:** "+issue.State)
	}
	out.WriteString(strings.Join(meta, " · ") + "  \n")
	threadURL := issue.HTMLURL
	if threadURL == "" {
		threadURL = thread.item.threadURL()
	}
	out.WriteString("**Thread:** " + threadURL + "  \n")
	out.WriteString(countLine + "\n\n")
	if body := githubText(issue.Body); body != "" {
		out.WriteString(body + "\n\n")
	}
	out.WriteString("---\n\n## Comments\n\n")
	if len(timeline) == 0 {
		out.WriteString("*No comments are loaded.*\n")
	}
	for _, entry := range timeline {
		out.WriteString("- " + entry.header + "\n")
		if entry.body != "" {
			out.WriteString(prefixLines(entry.body, "  ", "") + "\n")
		}
	}

	partial := ""
	if len(gaps) > 0 {
		partial = fmt.Sprintf("github %s: %d of %d comments loaded — %s", noun, len(comments), *issue.Comments,
			strings.Join(gaps, "; "))
	}
	return siteExtraction{markdown: out.String(), partial: partial, apiRecord: true}, true
}
