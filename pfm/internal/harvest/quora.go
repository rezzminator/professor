package harvest

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// Quora question pages (www.quora.com/<Question-slug>). Every rung pfm runs
// itself — direct, chrome impersonation, the browser — is served Cloudflare's
// challenge, on the page and on the same-host /graphql/gql_para_POST its
// client pages the answer list through; the page's own relay state carries
// only the first answers. Only a reader rung (Jina) serves the page, as
// Markdown holding the answer cards its render loaded. quoraReaderPage
// renders those cards — author as shown, credential line, date, body —
// without the ads, "Related questions" lists and site chrome between them,
// and reconciles them against the count the page states ("All related
// (100+)"): the answers not served signed-out are a named gap, as is a card
// the reader served folded to its opening.

var (
	// quoraDateLine is a card's date, linked to the answer: "·[Updated 6y](…/answer/…)".
	quoraDateLine = regexp.MustCompile(`^·\[([^\]]+)\]\((https://[a-z]+\.quora\.com/[^)\s]+/answer/[^)\s]+)\)$`)
	// quoraAuthorLine is a card's author, as shown, linked to the profile.
	quoraAuthorLine = regexp.MustCompile(`^\[([^!\]][^\]]*)\]\(https://[a-z]+\.quora\.com/profile/[^)\s]+\)$`)
	// quoraStatedLine is the page's own count of its answer list.
	quoraStatedLine = regexp.MustCompile(`^All related \(([\d.,]+[KM]?\+?)\)$`)
	quoraOriginal   = regexp.MustCompile(`^Originally Answered: (.+?)(?:\s*·)?$`)
)

// quoraNotServed names where the answers a reader does not carry live.
const quoraNotServed = "the rest are not served signed-out — Quora pages its answer list through " +
	"/graphql/gql_para_POST, which answers an anonymous client with a Cloudflare challenge; " +
	"open the question signed in to read them"

// quoraPreviewNote follows a card the reader served folded to its opening.
const quoraPreviewNote = "*(a preview: the reader served this answer's opening; the full answer is at its link)*\n\n"

// quoraAnswer is one answer card a reader served.
type quoraAnswer struct {
	author, credential, date, link, related, original string
	body                                              []string
	preview                                           bool
}

// isQuoraQuestion reports a Quora question address: one path segment, a slug.
func isQuoraQuestion(page *url.URL) bool {
	host := strings.ToLower(page.Hostname())
	path := strings.Trim(page.Path, "/")
	return (host == "quora.com" || strings.HasSuffix(host, ".quora.com")) &&
		path != "" && !strings.Contains(path, "/") && strings.Contains(path, "-")
}

// quoraReaderPage renders a reader's Markdown of a Quora question page as its
// answers, reconciled; any other page, or one holding no answer card the
// extractor knows, passes unchanged to the generic path.
func quoraReaderPage(source, markdown string) string {
	page, err := url.Parse(source)
	if err != nil {
		obs.Logger(context.Background()).Warn("harvest: a fetch source could not be parsed; Quora reader left out",
			"source", logSource(source), obs.FieldErr, err.Error())
		return markdown
	}
	if !isQuoraQuestion(page) {
		return markdown
	}
	lines := strings.Split(markdown, "\n")
	for index := range lines {
		lines[index] = strings.TrimSpace(lines[index])
	}
	title, stated := "", ""
	var answers []quoraAnswer
	cardStart := 0
	for index, line := range lines {
		if title == "" && strings.HasPrefix(line, "# ") {
			title = strings.TrimPrefix(line, "# ")
		}
		if match := quoraStatedLine.FindStringSubmatch(line); match != nil && stated == "" {
			stated = match[1]
		}
		match := quoraDateLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		answer, end := quoraCard(lines, cardStart, index, match[1], match[2])
		answers = append(answers, answer)
		cardStart = end
	}
	if len(answers) == 0 {
		return markdown
	}
	return quoraRender(title, stated, answers)
}

// quoraCard reads the card whose date line is at dateAt: its author and
// credential after the previous card's end (start), its body up to the
// card's footer; end is where the next card may begin.
func quoraCard(lines []string, start, dateAt int, date, link string) (quoraAnswer, int) {
	answer := quoraAnswer{author: unknownAuthor, date: date, link: link}
	authorAt := -1
	for index := start; index < dateAt; index++ {
		if match := quoraAuthorLine.FindStringSubmatch(lines[index]); match != nil {
			answer.author, authorAt = match[1], index
			break
		}
		if lines[index] == "Related" && index+1 < dateAt {
			answer.related = nextText(lines, index+1, dateAt)
		}
	}
	if authorAt >= 0 {
		var credential []string
		for _, line := range lines[authorAt+1 : dateAt] {
			if line != "" {
				credential = append(credential, line)
			}
		}
		answer.credential = strings.ReplaceAll(strings.Join(credential, " "), " , ", ", ")
	}
	end := dateAt + 1
	var body []string
	for ; end < len(lines) && !quoraCardEnds(lines[end]); end++ {
		line := lines[end]
		switch match := quoraOriginal.FindStringSubmatch(line); {
		case match != nil && len(body) == 0:
			answer.original = match[1]
		case line == "Continue Reading":
			body = nil // the folded opening; the full answer follows
		case line == "(more)":
			answer.preview = true
		default:
			body = append(body, line)
		}
	}
	answer.body = strings.Split(strings.Trim(strings.Join(body, "\n"), "\n"), "\n")
	return answer, end
}

// quoraCardEnds reports the first line past a card's body: its vote footer,
// or a block Quora sets between cards.
func quoraCardEnds(line string) bool {
	return strings.HasPrefix(line, "Upvote ·") || line == "Related" || line == "Related questions" ||
		line == "Ad" || quoraAuthorLine.MatchString(line) || quoraDateLine.MatchString(line)
}

// nextText is the first non-empty line in lines[from:to] that is not an image
// or link line — a "Related" label's question title.
func nextText(lines []string, from, to int) string {
	for _, line := range lines[from:to] {
		if line != "" && !strings.HasPrefix(line, "[") && !strings.HasPrefix(line, "!") {
			return line
		}
	}
	return ""
}

// quoraRender writes the answers and the partial marker naming what the
// reader did not carry.
func quoraRender(title, stated string, answers []quoraAnswer) string {
	var out strings.Builder
	if title != "" {
		out.WriteString("# " + title + "\n\n")
	}
	previews := 0
	for index := range answers {
		answer := &answers[index]
		out.WriteString("### " + answer.author + "\n\n")
		if answer.credential != "" {
			out.WriteString(answer.credential + "\n\n")
		}
		meta := answer.date + " · [answer](" + answer.link + ")"
		if answer.related != "" {
			meta += " · answer to a related question: " + answer.related
		}
		if answer.original != "" {
			meta += " · originally answered: " + answer.original
		}
		out.WriteString(meta + "\n\n")
		if body := collapseBlankLines(strings.Join(answer.body, "\n")); body != "" {
			out.WriteString(body + "\n\n")
		}
		if answer.preview {
			previews++
			out.WriteString(quoraPreviewNote)
		}
	}
	loaded := len(answers)
	var count, gap string
	switch {
	case stated == "":
		count = fmt.Sprintf("Quora answers: the page states no answer count · %d loaded", loaded)
		gap = count + "; more may exist — " + quoraNotServed
	case quoraExactCount(stated) > 0 && loaded >= quoraExactCount(stated):
		count = fmt.Sprintf("Quora answers: %s stated · %d loaded", stated, loaded)
	default:
		count = fmt.Sprintf("Quora answers: %s stated · %d loaded", stated, loaded)
		gap = count + "; " + quoraNotServed
	}
	if previews > 0 {
		verb := "are previews"
		if previews == 1 {
			verb = "is a preview"
		}
		gap = joinReasons(gap, fmt.Sprintf(
			"%d of the loaded answers %s (the reader served its opening only; the full answer is at its link)",
			previews, verb))
	}
	out.WriteString(count + "\n")
	if gap == "" {
		return out.String()
	}
	return withPartial(out.String(), gap)
}

// quoraExactCount is a stated count that names an exact number ("12",
// "1,240"); 0 for a bound ("100+") or an abbreviation ("1.2K").
func quoraExactCount(stated string) int {
	number, err := strconv.Atoi(strings.ReplaceAll(stated, ",", ""))
	if err != nil {
		return 0
	}
	return number
}

// collapseBlankLines keeps at most one blank line between paragraphs.
func collapseBlankLines(text string) string {
	var kept []string
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if line == "" && len(kept) > 0 && kept[len(kept)-1] == "" {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}
