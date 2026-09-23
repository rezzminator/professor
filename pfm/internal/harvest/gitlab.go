package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The GitLab issue and merge request extractor. The page
// (<group>/<project>/-/issues/<n>, -/work_items/<n>, -/merge_requests/<n>)
// is an app shell: its notes are drawn in the browser. GitLab runs on
// gitlab.com and on self-hosted instances, so a page is known by its host or
// by its markup (og:site_name GitLab). The REST notes list refuses a reader
// signed out (401), so the extractor reads the thread from the instance's
// GraphQL API on the page's own host, unauthenticated — no credential is ever
// sent — by GET, through loaders.go's budget: one query per page of 100
// notes, each answer carrying the item's record (title, body, author, state
// and the stated userNotesCount) and one page of its notes, the next page
// asked after the last one's end cursor until a page states no next page.
// Each answer is checked to be this item's before it is kept in the page as a
// harvester-gitlab-answer element, so a later conversion replays it like any
// followed loader. An item whose first answer never loaded is not claimed:
// the page goes the generic path, the gap named in its partial marker. The
// extractor renders the user notes in time order, a discussion's replies
// nested under its first note, the system notes (label and state changes)
// counted apart, and reconciles the notes loaded against the stated count.

const (
	gitlabComHost   = "gitlab.com"
	gitlabAnswerTag = "harvester-gitlab-answer"
	gitlabKindNotes = "notes"
	gitlabPerPage   = 100
)

// gitlabItem is the issue or merge request a page is.
type gitlabItem struct {
	origin, project string
	iid             int
	mergeRequest    bool
}

func (item gitlabItem) noun() string {
	if item.mergeRequest {
		return "merge request"
	}
	return githubKindIssue
}

// gitlabItemPath reads /<project path>/-/(issues|work_items|merge_requests)/<n>,
// a trailing slash allowed.
func gitlabItemPath(path string) (gitlabItem, bool) {
	project, rest, found := strings.Cut(strings.TrimPrefix(path, "/"), "/-/")
	parts := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	if !found || strings.Count(project, "/") < 1 || len(parts) != 2 {
		return gitlabItem{}, false
	}
	iid, err := strconv.Atoi(parts[1])
	if err != nil || iid <= 0 {
		return gitlabItem{}, false
	}
	switch parts[0] {
	case gitlabIssuesSegment, "work_items":
		return gitlabItem{project: project, iid: iid}, true
	case "merge_requests":
		return gitlabItem{project: project, iid: iid, mergeRequest: true}, true
	}
	return gitlabItem{}, false
}

// isGitLab reports a page a GitLab instance served: og:site_name GitLab.
func isGitLab(doc *html.Node) bool {
	meta := firstWithAttr(doc, "property", "og:site_name", nil)
	return meta != nil && meta.DataAtom == atom.Meta && strings.TrimSpace(nodeAttr(meta, "content")) == "GitLab"
}

type gitlabUser struct {
	Username string `json:"username"`
}

type gitlabNote struct {
	ID         string      `json:"id"`
	System     bool        `json:"system"`
	Body       string      `json:"body"`
	CreatedAt  string      `json:"createdAt"`
	URL        string      `json:"url"`
	Author     *gitlabUser `json:"author"`
	Discussion *struct {
		ID string `json:"id"`
	} `json:"discussion"`
}

type gitlabRecord struct {
	IID            string      `json:"iid"`
	Title          string      `json:"title"`
	WebURL         string      `json:"webUrl"`
	CreatedAt      string      `json:"createdAt"`
	State          string      `json:"state"`
	Author         *gitlabUser `json:"author"`
	Description    *string     `json:"description"`
	UserNotesCount *int        `json:"userNotesCount"`
	Notes          *struct {
		PageInfo struct {
			HasNextPage bool   `json:"hasNextPage"`
			EndCursor   string `json:"endCursor"`
		} `json:"pageInfo"`
		Nodes []gitlabNote `json:"nodes"`
	} `json:"notes"`
}

type gitlabAnswer struct {
	Data *struct {
		Project *struct {
			Issue        *gitlabRecord `json:"issue"`
			MergeRequest *gitlabRecord `json:"mergeRequest"`
		} `json:"project"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// record is the item an answer holds; nil when it holds none.
func (answer gitlabAnswer) record(item gitlabItem) *gitlabRecord {
	if answer.Data == nil || answer.Data.Project == nil {
		return nil
	}
	if item.mergeRequest {
		return answer.Data.Project.MergeRequest
	}
	return answer.Data.Project.Issue
}

// gitlabThread is what the page holds of an item's API answers.
type gitlabThread struct {
	item gitlabItem
	// pages holds each kept answer's item by page number.
	pages   map[int]*gitlabRecord
	dropped map[string]bool
}

func gitlabThreadOf(doc *html.Node, page *url.URL) (gitlabThread, bool) {
	item, ok := gitlabItemPath(page.Path)
	if !ok || (page.Scheme != schemeHTTPS && page.Scheme != schemeHTTP) {
		return gitlabThread{}, false
	}
	item.origin = page.Scheme + "://" + page.Host
	thread := gitlabThread{item: item, pages: map[int]*gitlabRecord{}, dropped: map[string]bool{}}
	for _, node := range keptAnswers(doc, gitlabAnswerTag) {
		if nodeAttr(node, "dropped") != "" {
			thread.dropped[nodeAttr(node, "key")] = true
			continue
		}
		number, _ := strconv.Atoi(nodeAttr(node, "page"))
		var answer gitlabAnswer
		if err := json.Unmarshal([]byte(rawText(node)), &answer); err != nil || answer.record(item) == nil {
			// graft kept only answers that proved themselves; one that no
			// longer does is a bug, logged and left out (the reconciliation
			// names the page missing).
			obs.Logger(context.Background()).Warn("harvest: a kept GitLab answer no longer decodes; left out",
				"page", number, obs.FieldErr, fmt.Sprint(err))
			continue
		}
		thread.pages[number] = answer.record(item)
	}
	return thread, true
}

// query is the GraphQL query for one page of the item's notes.
func (item gitlabItem) query(after string) string {
	field := "issue"
	if item.mergeRequest {
		field = "mergeRequest"
	}
	cursor := ""
	if after != "" {
		cursor = `, after: ` + strconv.Quote(after)
	}
	return `{ project(fullPath: ` + strconv.Quote(item.project) + `) { ` + field + `(iid: "` + strconv.Itoa(item.iid) +
		`") { iid title webUrl createdAt state author { username } description userNotesCount notes(first: ` +
		strconv.Itoa(gitlabPerPage) + cursor + `) { pageInfo { hasNextPage endCursor } nodes { id system body ` +
		`createdAt url author { username } discussion { id } } } } } }`
}

// next is the page number and cursor of the notes page the thread still
// lacks; 0 when the list is read to its end (or its first page is wanted
// with no cursor, number 1).
func (thread gitlabThread) next() (number int, after string) {
	for number = 1; ; number++ {
		kept, ok := thread.pages[number]
		if !ok {
			return number, after
		}
		if kept.Notes == nil || !kept.Notes.PageInfo.HasNextPage || kept.Notes.PageInfo.EndCursor == "" {
			return 0, ""
		}
		after = kept.Notes.PageInfo.EndCursor
	}
}

func gitlabKey(target string) string { return "gitlab-api " + target }

// gitlabLoaders names the notes page the item still lacks, for loaders.go.
func gitlabLoaders(doc *html.Node, page *url.URL) []pageLoader {
	thread, ok := gitlabThreadOf(doc, page)
	if !ok {
		return nil
	}
	number, after := thread.next()
	if number == 0 {
		return nil
	}
	target := thread.item.origin + "/api/graphql?" + url.Values{"query": {thread.item.query(after)}}.Encode()
	key := gitlabKey(target)
	if thread.dropped[key] {
		return nil
	}
	label := fmt.Sprintf("notes page %d", number)
	if number == 1 {
		label = "the " + thread.item.noun() + "'s API record (notes page 1)"
	}
	item := thread.item
	return []pageLoader{{
		key:     key,
		label:   label,
		method:  http.MethodGet,
		target:  target,
		headers: map[string]string{headerAccept: mediaTypeJSON},
		graft: func(body []byte, contentType string) error {
			if err := item.check(label, body, contentType); err != nil {
				return err
			}
			keepAnswer(doc, gitlabAnswerTag, key, gitlabKindNotes, number, body)
			return nil
		},
		drop: func() { keepAnswer(doc, gitlabAnswerTag, key, gitlabKindNotes, number, nil) },
	}}
}

// check proves an API answer is one page of this item's notes.
func (item gitlabItem) check(label string, body []byte, contentType string) error {
	var answer gitlabAnswer
	if err := json.Unmarshal(body, &answer); err != nil || (answer.Data == nil && len(answer.Errors) == 0) {
		return fmt.Errorf("answered by a %s body that is not the API's JSON for %s",
			discourseContentType(contentType), label)
	}
	if len(answer.Errors) > 0 {
		return fmt.Errorf("answered by a GraphQL error: %s", answer.Errors[0].Message)
	}
	found := answer.record(item)
	switch {
	case answer.Data.Project == nil:
		return fmt.Errorf("answered with no project %s (private, moved or not found)", item.project)
	case found == nil:
		return fmt.Errorf("answered with no %s %d in %s", item.noun(), item.iid, item.project)
	case found.IID != strconv.Itoa(item.iid):
		return fmt.Errorf("answered by the record of %s %s, not %d", item.noun(), found.IID, item.iid)
	case found.UserNotesCount == nil:
		return fmt.Errorf("answered by a record of %s %d stating no note count", item.noun(), item.iid)
	case found.Notes == nil:
		return fmt.Errorf("answered by a record of %s %d listing no notes", item.noun(), item.iid)
	}
	return nil
}

func gitlabAuthor(user *gitlabUser) string {
	if user == nil || user.Username == "" {
		return unknownAuthor
	}
	return user.Username
}

// extractGitLabItem renders a GitLab issue or merge request from the API
// answers kept in its page.
func extractGitLabItem(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	thread, ok := gitlabThreadOf(doc, page)
	if !ok {
		return siteExtraction{}, false
	}
	noun := thread.item.noun()
	record := thread.pages[1]
	if record == nil {
		return siteExtraction{unrendered: "gitlab " + noun + ": the " + noun + "'s API record was not loaded " +
			"(its notes not read from the API; the page is stored as GitLab served it)"}, false
	}
	var gaps []string
	var notes []gitlabNote
	seen := map[string]bool{}
	system := 0
	for number := 1; ; number++ {
		kept, ok := thread.pages[number]
		if !ok {
			gaps = append(gaps, fmt.Sprintf("notes page %d not loaded (the list not read to its end)", number))
			break
		}
		for _, note := range kept.Notes.Nodes {
			switch {
			case seen[note.ID]:
			case note.System:
				system++
			default:
				notes = append(notes, note)
			}
			seen[note.ID] = true
		}
		if !kept.Notes.PageInfo.HasNextPage || kept.Notes.PageInfo.EndCursor == "" {
			break
		}
	}
	stated := *record.UserNotesCount
	countLine := fmt.Sprintf("**Notes:** %d stated · %d loaded (%d system note(s) not shown)", stated, len(notes),
		system)
	switch rest := stated - len(notes); {
	case rest > 0 && len(gaps) == 0:
		gaps = append(gaps, fmt.Sprintf("%d stated note(s) not in the API's list (deleted since the count, "+
			"or not visible to a reader signed out)", rest))
	case rest < 0:
		countLine += fmt.Sprintf(" · %d more loaded than stated", -rest)
	}
	partial := ""
	if len(gaps) > 0 {
		countLine += " · gaps: " + strings.Join(gaps, "; ")
		partial = fmt.Sprintf("gitlab %s: %d of %d notes loaded — %s", noun, len(notes), stated,
			strings.Join(gaps, "; "))
	}

	mark := "#"
	if thread.item.mergeRequest {
		mark = "!"
	}
	var out strings.Builder
	out.WriteString("# " + record.Title + " (" + mark + record.IID + ")\n\n")
	meta := []string{"**Author:** " + gitlabAuthor(record.Author)}
	if record.CreatedAt != "" {
		meta = append(meta, "**Opened:** "+githubPosted(record.CreatedAt))
	}
	if record.State != "" {
		meta = append(meta, "**State:** "+record.State)
	}
	out.WriteString(strings.Join(meta, " · ") + "  \n")
	out.WriteString("**Thread:** " + record.WebURL + "  \n")
	out.WriteString(countLine + "\n\n")
	if body := githubText(record.Description); body != "" {
		out.WriteString(body + "\n\n")
	}
	out.WriteString("---\n\n## Notes\n\n")
	if len(notes) == 0 {
		out.WriteString("*No notes are loaded.*\n")
	}
	// A discussion's notes render together, its replies under its first note.
	var order []string
	byDiscussion := map[string][]gitlabNote{}
	for _, note := range notes {
		discussion := note.ID
		if note.Discussion != nil && note.Discussion.ID != "" {
			discussion = note.Discussion.ID
		}
		if byDiscussion[discussion] == nil {
			order = append(order, discussion)
		}
		byDiscussion[discussion] = append(byDiscussion[discussion], note)
	}
	for _, discussion := range order {
		for index, note := range byDiscussion[discussion] {
			indent := ""
			if index > 0 {
				indent = "  "
			}
			number := note.ID[strings.LastIndex(note.ID, "/")+1:]
			out.WriteString(indent + "- **" + gitlabAuthor(note.Author) + "** · " + githubPosted(note.CreatedAt) +
				" · [#" + number + "](" + note.URL + ")\n")
			if body := githubText(&note.Body); body != "" {
				out.WriteString(prefixLines(body, indent+"  ", "") + "\n")
			}
		}
	}
	return siteExtraction{markdown: out.String(), partial: partial, apiRecord: true}, true
}

// gitlabIssuesSegment is the path segment of an issue's address.
const gitlabIssuesSegment = "issues"
