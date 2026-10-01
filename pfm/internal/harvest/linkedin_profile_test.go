package harvest

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// linkedInInline parses a synthetic LinkedIn page whose JSON-LD blocks are
// blocks, each its own <script type="application/ld+json">, around body.
func linkedInInline(t *testing.T, body string, blocks ...string) *html.Node {
	t.Helper()
	var page strings.Builder
	page.WriteString("<!doctype html><html><head><title>LinkedIn</title>")
	for _, block := range blocks {
		page.WriteString(`<script type="application/ld+json">` + block + "</script>")
	}
	page.WriteString("</head><body>" + body + "</body></html>")
	doc, err := html.Parse(strings.NewReader(page.String()))
	if err != nil {
		t.Fatalf("parse the inline LinkedIn page: %v", err)
	}
	return doc
}

const linkedInTestPerson = `{"@type":"Person","name":"Avery Example",` +
	`"url":"https://www.linkedin.com/in/avery-example-0000"}`

// TestLinkedInProfileNamesWhatItDidNotRead: a profile whose Article entity or
// second JSON-LD block could not be read, or whose @graph holds an unreadable
// member before its Person, still renders the Person — and its partial names
// what was not read, never rendering the loss as absence.
func TestLinkedInProfileNamesWhatItDidNotRead(t *testing.T) {
	t.Parallel()
	const source = "https://www.linkedin.com/in/avery-example-0000"
	for _, tc := range []struct {
		name   string
		blocks []string
		gap    string
	}{
		{
			name: "undecodable article",
			blocks: []string{`{"@graph":[` + linkedInTestPerson +
				`,{"@type":"Article","headline":{"x":1}}]}`},
			gap: "Article",
		},
		{
			name:   "unreadable second block",
			blocks: []string{linkedInTestPerson, `{"@type":"DiscussionForumPosting","text":`},
			gap:    "JSON-LD",
		},
		{
			name:   "unreadable graph member before the person",
			blocks: []string{`{"@graph":["x",` + linkedInTestPerson + `]}`},
			gap:    "JSON-LD",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extraction, _, ok := extractForSite(source, linkedInInline(t, "", tc.blocks...))
			if !ok || !strings.Contains(extraction.markdown, "# Avery Example\n") {
				t.Fatalf("the profile was not rendered (ok %v, unrendered %q):\n%s",
					ok, extraction.unrendered, extraction.markdown)
			}
			if !strings.Contains(extraction.partial, loginWallReason) ||
				!strings.Contains(extraction.partial, "could not be read") ||
				!strings.Contains(extraction.partial, tc.gap) {
				t.Errorf("partial = %q, want the login wall and the unread %s named", extraction.partial, tc.gap)
			}
		})
	}
}

// TestLinkedInProfileAddress: the Person's own url replaces the rendered
// address only on linkedin.com; a section other than the root or the recent
// activity is named in the partial as not what was read.
func TestLinkedInProfileAddress(t *testing.T) {
	t.Parallel()
	foreign := `{"@type":"Person","name":"Avery Example","url":"https://other.example/in/avery-example-0000"}`
	extraction, _, ok := extractForSite("https://www.linkedin.com/in/avery-example-0000/recent-activity/all/",
		linkedInInline(t, "", foreign))
	if !ok {
		t.Fatal("the profile was not rendered")
	}
	if want := "**Profile:** https://www.linkedin.com/in/avery-example-0000/recent-activity/all/\n"; !strings.Contains(
		extraction.markdown, want) {
		t.Errorf("a foreign-host Person url replaced the address; want %q:\n%s", want, extraction.markdown)
	}
	if extraction.partial != loginWallReason {
		t.Errorf("recent activity: partial = %q, want the login wall alone", extraction.partial)
	}

	extraction, _, ok = extractForSite("https://www.linkedin.com/in/avery-example-0000/details/skills/",
		linkedInInline(t, "", linkedInTestPerson))
	if !ok {
		t.Fatal("the skills sub-page was not rendered")
	}
	if !strings.Contains(extraction.markdown, "**Profile:** https://www.linkedin.com/in/avery-example-0000\n") {
		t.Errorf("the Person's linkedin.com url did not name the profile read:\n%s", extraction.markdown)
	}
	if want := "details/skills"; !strings.Contains(extraction.partial, want) ||
		!strings.Contains(extraction.partial, "signed-out profile was read") {
		t.Errorf("partial = %q, want the asked-for %s section named as not read", extraction.partial, want)
	}
}

// TestLinkedInUnreadOnlyWhenTheEntityWasLost: "its JSON-LD could not be read"
// is the fall-through's cause only when the kind needs a JSON-LD entity and a
// block that could not be read may have held it; a markup-only kind and a
// wall with an unrelated broken block fall through clean.
func TestLinkedInUnreadOnlyWhenTheEntityWasLost(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, source, block, want string
	}{
		{
			name:   "markup-only newsletter",
			source: "https://www.linkedin.com/newsletters/contoso-weekly-7000000000000000009",
			block:  `{"@type":"Newsletter","name":`,
		},
		{
			name:   "wall with an unrelated broken block",
			source: "https://www.linkedin.com/in/avery-example-0000",
			block:  `{"@type":"BreadcrumbList","itemListElement":[`,
		},
		{
			name:   "profile whose person block is broken",
			source: "https://www.linkedin.com/in/avery-example-0000",
			block:  `{"@type":"Person","name":"Avery`,
			want:   linkedInUnread,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extraction, _, ok := extractForSite(tc.source, linkedInInline(t, "<p>Join LinkedIn</p>", tc.block))
			if ok {
				t.Fatalf("the page was rendered:\n%s", extraction.markdown)
			}
			if extraction.unrendered != tc.want {
				t.Errorf("unrendered = %q, want %q", extraction.unrendered, tc.want)
			}
		})
	}
}
