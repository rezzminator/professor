package harvest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The LinkedIn page extractor. A signed-out LinkedIn page states its main
// entity in schema.org JSON-LD. This file holds the router, the JSON-LD
// reading every kind shares, and the post (/posts/<slug>) and feed update
// (/feed/update/<urn>) kinds; linkedin_jobs.go the job view, linkedin_profile.go
// the member profile, linkedin_org.go the company family, products, ranking
// hubs and job-card lists, linkedin_collections.go newsletters, top content,
// Learning courses, embeds and the guest job posting. A post's entity is a
// SocialMediaPosting with its author, counts, text and the comments shown
// signed-out; the markup around it is navigation, a sign-in gate and a login
// form, so the extractor renders the JSON-LD. A post's author is the
// posting's own, never a comment's. A page without the entity its address
// promises — the /authwall "Join LinkedIn" page, the login page, a 999
// answer — is not the shape it knows and falls through to the generic path,
// which names the wall; the fall-through names the JSON-LD as unread only
// when a block that could not be read may have held that entity. Every page
// rendered is what LinkedIn shows a signed-out reader, so each carries the
// login wall's partial, and names any JSON-LD it could not read; a post's
// comments are reconciled against the count it states.

const linkedInHost = "linkedin.com"

// linkedInHeadingRunes caps a post's heading, its first line.
const linkedInHeadingRunes = 100

// linkedInUntitled heads a post that states neither text nor headline.
const linkedInUntitled = "[untitled post]"

// linkedInUnread is what a page whose JSON-LD could not be read leaves
// unrendered.
const linkedInUnread = "linkedin page: its JSON-LD could not be read, so its entity was not rendered"

// linkedInExcerptRunes caps a recent post's excerpt on a profile.
const linkedInExcerptRunes = 200

// linkedInBlankLines parts a post's plain text into paragraphs.
var linkedInBlankLines = regexp.MustCompile(`\n[ \t]*\n`)

// linkedInPageKind is the entity a LinkedIn address promises.
type linkedInPageKind int

const (
	linkedInNone linkedInPageKind = iota
	linkedInNewsletter
	linkedInTopContent
	linkedInCourse
	linkedInEmbed
	linkedInGuestJob
	linkedInPost
	linkedInJob
	linkedInProfile
	linkedInCompany
	linkedInProduct
	linkedInHub
	linkedInJobList
)

// linkedInKind reads the kind of page an address is: the collection kinds
// (linkedInCollectionKind), /posts/… and /feed/update/… a post, /jobs/view/…
// a job, /in/<vanity>[/<section>] a profile, then the company family, product,
// hub and job-list kinds (linkedInFamilyKind).
func linkedInKind(page *url.URL) linkedInPageKind {
	if kind := linkedInCollectionKind(page); kind != linkedInNone {
		return kind
	}
	for _, prefix := range []string{"/posts/", "/feed/update/"} {
		if rest, ok := strings.CutPrefix(page.Path, prefix); ok && strings.Trim(rest, "/") != "" {
			return linkedInPost
		}
	}
	if rest, ok := strings.CutPrefix(page.Path, "/jobs/view/"); ok && strings.Trim(rest, "/") != "" {
		return linkedInJob
	}
	if rest, ok := strings.CutPrefix(page.Path, "/in/"); ok {
		// A sub-page (/in/<vanity>/recent-activity/…) is served signed-out
		// as the profile itself; its Person decides.
		if vanity, _, _ := strings.Cut(rest, "/"); vanity != "" {
			return linkedInProfile
		}
	}
	if kind := linkedInFamilyKind(page.Path); kind != linkedInNone {
		return kind
	}
	return linkedInNone
}

// isLinkedInHost reports linkedin.com or one of its subdomains.
func isLinkedInHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == linkedInHost || strings.HasSuffix(host, "."+linkedInHost)
}

// isLinkedInPage reports a page address the extractor renders.
func isLinkedInPage(page *url.URL) bool { return linkedInKind(page) != linkedInNone }

// ldText is a JSON-LD value read as text: a string, a number (a year, a
// count), a boolean, an object's name, or null ("").
type ldText string

func (text *ldText) UnmarshalJSON(data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("read a JSON-LD value: %w", err)
	}
	switch typed := value.(type) {
	case nil:
		*text = ""
	case string:
		*text = ldText(strings.TrimSpace(typed))
	case float64:
		*text = ldText(strconv.FormatFloat(typed, 'f', -1, 64))
	case bool:
		*text = ldText(strconv.FormatBool(typed))
	case map[string]any:
		name, ok := typed["name"].(string)
		if !ok {
			return fmt.Errorf("a JSON-LD object holds no name to read as text: %.80s", data)
		}
		*text = ldText(strings.TrimSpace(name))
	default:
		return fmt.Errorf("a JSON-LD value is %T, not text: %.80s", value, data)
	}
	return nil
}

// ldList is a JSON-LD property holding one value or an array of them.
type ldList[T any] []T

func (list *ldList[T]) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	switch {
	case bytes.Equal(trimmed, []byte("null")):
		*list = nil
		return nil
	case bytes.HasPrefix(trimmed, []byte("[")):
		var many []T
		if err := json.Unmarshal(trimmed, &many); err != nil {
			return fmt.Errorf("read a JSON-LD array: %w", err)
		}
		*list = many
		return nil
	}
	var one T
	if err := json.Unmarshal(trimmed, &one); err != nil {
		return fmt.Errorf("read a JSON-LD value: %w", err)
	}
	*list = ldList[T]{one}
	return nil
}

type ldCounter struct {
	InteractionType ldText `json:"interactionType"`
	Count           ldText `json:"userInteractionCount"`
}

type ldRole struct {
	StartDate ldText `json:"startDate"`
	EndDate   ldText `json:"endDate"`
}

// ldThing is a named entity: an author, an organization, a school.
type ldThing struct {
	Name   ldText  `json:"name"`
	URL    ldText  `json:"url"`
	Member *ldRole `json:"member"`
}

// ldPosting is a SocialMediaPosting, one of its comments, a member's Article
// or DiscussionForumPosting.
type ldPosting struct {
	URL           ldText            `json:"url"`
	Headline      ldText            `json:"headline"`
	ArticleBody   ldText            `json:"articleBody"`
	Text          ldText            `json:"text"`
	DatePublished ldText            `json:"datePublished"`
	Author        ldList[ldThing]   `json:"author"`
	Comments      ldList[ldPosting] `json:"comment"`
	CommentCount  ldText            `json:"commentCount"`
	Stats         ldList[ldCounter] `json:"interactionStatistic"`
}

type ldAddress struct {
	Locality ldText `json:"addressLocality"`
	Region   ldText `json:"addressRegion"`
	Country  ldText `json:"addressCountry"`
}

type ldPlace struct {
	Address ldAddress `json:"address"`
}

// ldEntity is one JSON-LD entity of a page, undecoded, with its @type.
type ldEntity struct {
	kind string
	raw  json.RawMessage
}

// ldUnreadError is a JSON-LD block, @graph member or entity that could not
// be read: what names it for a reader, raw is the text it held.
type ldUnreadError struct {
	what string
	raw  []byte
	err  error
}

func (unread *ldUnreadError) Error() string { return "read " + unread.what + ": " + unread.err.Error() }

func (unread *ldUnreadError) Unwrap() error { return unread.err }

// ldUnreadParts is every ldUnreadError err holds, through errors.Join.
func ldUnreadParts(err error) []*ldUnreadError {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var parts []*ldUnreadError
		for _, inner := range joined.Unwrap() {
			parts = append(parts, ldUnreadParts(inner)...)
		}
		return parts
	}
	var unread *ldUnreadError
	if errors.As(err, &unread) {
		return []*ldUnreadError{unread}
	}
	return nil
}

// linkedInEntities reads every JSON-LD block of doc into its entities, a
// @graph's members flattened; the error names each block or member that
// could not be read (an ldUnreadError), the entities of the rest still
// returned.
func linkedInEntities(doc *html.Node) ([]ldEntity, error) {
	var entities []ldEntity
	var failures []error
	for _, script := range elementsByTag(doc, atom.Script) {
		if !strings.EqualFold(strings.TrimSpace(nodeAttr(script, "type")), "application/ld+json") {
			continue
		}
		var err error
		if entities, err = appendLDEntities(entities, []byte(rawText(script)), "a JSON-LD block"); err != nil {
			failures = append(failures, err)
		}
	}
	return entities, errors.Join(failures...)
}

// appendLDEntities appends the entities of data, one block or @graph member
// named what; a member that could not be read is named in the error and
// skipped, its siblings still appended.
func appendLDEntities(into []ldEntity, data []byte, what string) ([]ldEntity, error) {
	trimmed := bytes.TrimSpace(data)
	var members []json.RawMessage
	if bytes.HasPrefix(trimmed, []byte("[")) {
		if err := json.Unmarshal(trimmed, &members); err != nil {
			return into, &ldUnreadError{what: what, raw: trimmed, err: err}
		}
	} else {
		var node struct {
			Type  json.RawMessage   `json:"@type"`
			Graph []json.RawMessage `json:"@graph"`
		}
		if err := json.Unmarshal(trimmed, &node); err != nil {
			return into, &ldUnreadError{what: what, raw: trimmed, err: err}
		}
		if node.Graph == nil {
			return append(into, ldEntity{kind: ldTypeName(node.Type), raw: trimmed}), nil
		}
		members = node.Graph
	}
	var failures []error
	for _, member := range members {
		var err error
		if into, err = appendLDEntities(into, member, "a JSON-LD @graph member"); err != nil {
			failures = append(failures, err)
		}
	}
	return into, errors.Join(failures...)
}

// ldTypeName is an entity's @type: the string, or an array's first; "" when
// it has none.
func ldTypeName(raw json.RawMessage) string {
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		return name
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err == nil && len(names) > 0 {
		return names[0]
	}
	return ""
}

// ldDecode decodes the first entity of kind into into; false when the page
// holds none, or it does not decode (the error).
func ldDecode(entities []ldEntity, kind string, into any) (bool, error) {
	for _, entity := range entities {
		if entity.kind != kind {
			continue
		}
		if err := json.Unmarshal(entity.raw, into); err != nil {
			return false, &ldUnreadError{what: "the page's JSON-LD " + kind, raw: entity.raw, err: err}
		}
		return true, nil
	}
	return false, nil
}

// ldPostings decodes every entity of kind; one that does not decode is
// named in the error and left out.
func ldPostings(entities []ldEntity, kind string) ([]ldPosting, error) {
	var postings []ldPosting
	var failures []error
	for _, entity := range entities {
		if entity.kind != kind {
			continue
		}
		var posting ldPosting
		if err := json.Unmarshal(entity.raw, &posting); err != nil {
			unread := &ldUnreadError{what: "a JSON-LD " + kind + " entity", raw: entity.raw, err: err}
			failures = append(failures, unread)
			continue
		}
		postings = append(postings, posting)
	}
	return postings, errors.Join(failures...)
}

// extractLinkedInPage renders a LinkedIn page from the entity its address
// promises (linkedInKind); false for a page that holds none (a wall), which
// falls through to the generic path. JSON-LD that could not be read is
// logged; a rendered page names it in its partial, and a fall-through names
// it only when it may have held the kind's entity (linkedInUnreadCause).
func extractLinkedInPage(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	entities, readErr := linkedInEntities(doc)
	renderer := markdownRenderer{base: page}
	address := *page
	address.RawQuery, address.Fragment = "", ""
	var (
		markdown, partial string
		found             bool
		err               error
		// entity is the JSON-LD @type the kind is proved by; "" for a
		// markup-only kind.
		entity string
	)
	switch linkedInKind(page) {
	case linkedInNewsletter:
		markdown, found = linkedInNewsletterPage(doc, address.String())
	case linkedInTopContent:
		entity = "CollectionPage"
		markdown, found, err = linkedInTopContentPage(doc, address.String(), entities, renderer)
	case linkedInCourse:
		return extractLinkedInCourse(doc, page, address.String(), entities, readErr, renderer)
	case linkedInEmbed:
		markdown, found = linkedInEmbedPage(doc, address.String())
	case linkedInGuestJob:
		markdown, found = linkedInGuestJobPage(doc, address.String(), renderer)
	case linkedInPost:
		entity = "SocialMediaPosting"
		markdown, partial, found, err = linkedInPostPage(address.String(), entities)
	case linkedInJob:
		entity = "JobPosting"
		markdown, found, err = linkedInJobPage(doc, address.String(), entities, renderer)
	case linkedInProfile:
		entity = "Person"
		markdown, found, err = linkedInProfilePage(doc, address.String(), entities, renderer)
		partial = linkedInSectionReason(page)
	case linkedInCompany:
		entity = "Organization"
		markdown, found, err = linkedInOrgPage(doc, page, address.String(), entities)
	case linkedInProduct:
		markdown, found = linkedInProductPage(doc, address.String())
	case linkedInHub:
		entity = "ItemList"
		markdown, found, err = linkedInHubPage(doc, address.String(), entities)
	case linkedInJobList:
		markdown, found = linkedInJobListPage(doc, page, address.String())
	}
	unread := errors.Join(readErr, err)
	if unread != nil {
		obs.Logger(context.Background()).Warn("harvest: a LinkedIn page's JSON-LD could not be read whole",
			"target", logSource(page.String()), obs.FieldErr, unread.Error())
	}
	if !found {
		return siteExtraction{unrendered: linkedInUnreadCause(err, readErr, entity)}, false
	}
	return siteExtraction{
		markdown: markdown,
		partial:  joinReasons(loginWallReason, partial, linkedInUnreadReason(unread)),
	}, true
}

// linkedInUnreadCause is what a page that fell through leaves unrendered:
// linkedInUnread when its kind's entity (a JSON-LD @type) did not decode
// (kindErr), or a block that could not be read (readErr) names that type;
// "" for a markup-only kind or a page whose unreadable JSON-LD held
// something else — a wall, which the generic path names.
func linkedInUnreadCause(kindErr, readErr error, entity string) string {
	if entity == "" {
		return ""
	}
	if kindErr != nil {
		return linkedInUnread
	}
	for _, part := range ldUnreadParts(readErr) {
		if bytes.Contains(part.raw, []byte(`"`+entity+`"`)) {
			return linkedInUnread
		}
	}
	return ""
}

// linkedInUnreadReason names, for a rendered page, the JSON-LD it could not
// read (or any other part), so the loss never reads as absence; "" when
// everything was read.
func linkedInUnreadReason(err error) string {
	if err == nil {
		return ""
	}
	var names []string
	for _, part := range ldUnreadParts(err) {
		if !slices.Contains(names, part.what) {
			names = append(names, part.what)
		}
	}
	if len(names) == 0 {
		return "linkedin page: part of it could not be read, so it is not rendered whole"
	}
	return "linkedin page: " + strings.Join(names, ", ") + " could not be read, so what it held is not rendered"
}

// linkedInExcerpt is a posting's text on one line, its articleBody preferred,
// cut to linkedInExcerptRunes.
func linkedInExcerpt(post *ldPosting) string {
	return linkedInTrim(strings.Join(strings.Fields(linkedInBody(post)), " "), linkedInExcerptRunes)
}

// linkedInBody is a posting's articleBody, its text when it states none.
func linkedInBody(post *ldPosting) string {
	if post.ArticleBody != "" {
		return string(post.ArticleBody)
	}
	return string(post.Text)
}

// linkedInPostPage renders a SocialMediaPosting: its author (the posting's
// own, never a comment's), date, counts, text and the comments the page
// serves, reconciled against the count it states.
func linkedInPostPage(address string, entities []ldEntity) (markdown, partial string, found bool, err error) {
	var post ldPosting
	if found, err = ldDecode(entities, "SocialMediaPosting", &post); !found {
		return "", "", false, err
	}
	loaded := len(post.Comments)
	stated, statedKnown := ldCount(post.Stats, "CommentAction")
	if count, convErr := strconv.Atoi(string(post.CommentCount)); convErr == nil {
		stated, statedKnown = count, true
	}
	var gaps []string
	statedText := "count not read (the post states no comment count)"
	surplus := ""
	if statedKnown {
		statedText = strconv.Itoa(stated)
		switch rest := stated - loaded; {
		case rest > 0:
			gaps = append(gaps, fmt.Sprintf("%d stated comment(s) not in the page (LinkedIn serves a signed-out "+
				"reader only its first comments)", rest))
		case rest < 0:
			surplus = fmt.Sprintf(" · %d more loaded than stated", -rest)
		}
	} else {
		gaps = append(gaps, "the stated comment count was not read (the post states none)")
	}
	countLine := fmt.Sprintf("**Comments:** %s stated · %d loaded", statedText, loaded) + surplus
	if len(gaps) > 0 {
		countLine += " · gaps: " + strings.Join(gaps, "; ")
		progress := fmt.Sprintf("%d comments loaded, the stated count not read", loaded)
		if statedKnown {
			progress = fmt.Sprintf("%d of %d comments loaded", loaded, stated)
		}
		partial = "linkedin post: " + progress + " — " + strings.Join(gaps, "; ")
	}

	body := linkedInBody(&post)
	var out strings.Builder
	out.WriteString("# " + linkedInHeading(body, string(post.Headline)) + "\n\n")
	out.WriteString("**Author:** " + linkedInAuthor(post.Author) +
		" · **Published:** " + linkedInDate(string(post.DatePublished), unknownPosted) + "  \n")
	out.WriteString("**Post:** " + address + "  \n")
	if reactions, ok := ldCount(post.Stats, "LikeAction"); ok {
		fmt.Fprintf(&out, "**Reactions:** %d · ", reactions)
	}
	out.WriteString(countLine + "\n\n")
	if paragraphs := linkedInParagraphs(body); len(paragraphs) > 0 {
		out.WriteString(strings.Join(paragraphs, "\n\n") + "\n\n")
	}
	out.WriteString("---\n\n## Comments\n\n")
	if loaded == 0 {
		out.WriteString("*No comments are in this page.*\n")
	}
	for index := range post.Comments {
		comment := &post.Comments[index]
		out.WriteString("- **" + linkedInAuthor(comment.Author) + "** · " +
			linkedInDate(string(comment.DatePublished), unknownPosted) + "\n")
		if paragraphs := linkedInParagraphs(string(comment.Text)); len(paragraphs) > 0 {
			out.WriteString(prefixLines(strings.Join(paragraphs, "\n\n"), "  ", "") + "\n")
		}
	}
	return out.String(), partial, true, nil
}

// ldCount reads the count of the first interaction statistic whose type
// names action ("LikeAction", "CommentAction", "Follow").
func ldCount(stats []ldCounter, action string) (int, bool) {
	for _, stat := range stats {
		if !strings.Contains(string(stat.InteractionType), action) {
			continue
		}
		if count, err := strconv.Atoi(string(stat.Count)); err == nil {
			return count, true
		}
	}
	return 0, false
}

// linkedInAuthor is the first author's name, unknownAuthor when none.
func linkedInAuthor(authors []ldThing) string {
	for _, author := range authors {
		if author.Name != "" {
			return string(author.Name)
		}
	}
	return unknownAuthor
}

// linkedInHeading is a post's heading: the first line of its text (its
// headline when the text has none), trimmed to linkedInHeadingRunes.
func linkedInHeading(body, headline string) string {
	for _, line := range strings.Split(body+"\n"+headline, "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			return linkedInTrim(line, linkedInHeadingRunes)
		}
	}
	return linkedInUntitled
}

// linkedInTrim cuts text to limit runes at a word boundary, marked with "…".
func linkedInTrim(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	cut := string(runes[:limit])
	if space := strings.LastIndex(cut, " "); space > len(cut)/2 {
		cut = cut[:space]
	}
	return strings.TrimRight(cut, " ,;:-") + "…"
}

// linkedInParagraphs renders a post's plain text as Markdown paragraphs: a
// blank line parts paragraphs, a single newline stays a hard break.
func linkedInParagraphs(text string) []string {
	var out []string
	for _, paragraph := range linkedInBlankLines.Split(strings.ReplaceAll(text, "\r\n", "\n"), -1) {
		var lines []string
		for _, line := range strings.Split(paragraph, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				lines = append(lines, line)
			}
		}
		if len(lines) > 0 {
			out = append(out, strings.Join(lines, "  \n"))
		}
	}
	return out
}

// linkedInDate renders a JSON-LD instant as "2006-01-02 15:04 UTC"; the value
// as stated when it is no instant, missing when it is empty.
func linkedInDate(value, missing string) string {
	if value == "" {
		return missing
	}
	if instant, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return instant.UTC().Format("2006-01-02 15:04 UTC")
	}
	return value
}

// linkedInDay renders a JSON-LD date or instant as its day, "2006-01-02".
func linkedInDay(value string) string {
	if instant, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return instant.UTC().Format(time.DateOnly)
	}
	return value
}

// linkedInMeta renders label/value pairs as one "**Label:** value · …" line,
// leaving out the empty values.
func linkedInMeta(pairs ...string) string {
	var parts []string
	for index := 0; index+1 < len(pairs); index += 2 {
		if pairs[index+1] != "" {
			parts = append(parts, "**"+pairs[index]+":** "+pairs[index+1])
		}
	}
	return strings.Join(parts, " · ")
}

// linkedInJoin joins the non-empty parts with separator.
func linkedInJoin(separator string, parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, separator)
}

// linkedInTexts is a JSON-LD list's values as strings, a comma-separated
// value split into its items.
func linkedInTexts(values []ldText) []string {
	var out []string
	for _, value := range values {
		for _, item := range strings.Split(string(value), ",") {
			if item = strings.TrimSpace(item); item != "" {
				out = append(out, item)
			}
		}
	}
	return out
}

// linkedInClassText is the text of node's first element of class; "" when none.
func linkedInClassText(node *html.Node, class string) string {
	if found := firstClass(node, class); found != nil {
		return nodeText(found)
	}
	return ""
}

// classElements returns every element under node (node included) whose class
// list names class, in DOM order.
func classElements(node *html.Node, class string) []*html.Node {
	var found []*html.Node
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode && hasClass(current, class) {
			found = append(found, current)
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return found
}

// linkedInWallTrk is a link's trk parameter: LinkedIn tags every link with
// the page template it sits on ("seo-authwall-base_footer-about").
var linkedInWallTrk = regexp.MustCompile(`[?&]trk=([A-Za-z0-9_-]+)`)

// linkedInSignUpWall reports whether markdown, a reader's copy of source, is
// LinkedIn's sign-up wall (the "authwall" template a signed-out visitor is
// bounced to) rather than the page: most of its tagged links name that
// template. The wall's own prose is never read — a page quoting "Join
// LinkedIn" is still the page.
func linkedInSignUpWall(source, markdown string) bool {
	page, err := url.Parse(source)
	if err != nil {
		obs.Logger(context.Background()).
			Warn("harvest: the address could not be parsed for its host", obs.FieldErr, err.Error())
		return false
	}
	if !isLinkedInHost(page.Hostname()) {
		return false
	}
	tagged, walled := 0, 0
	for _, match := range linkedInWallTrk.FindAllStringSubmatch(markdown, -1) {
		tagged++
		if strings.Contains(match[1], "authwall") {
			walled++
		}
	}
	return walled > 0 && walled*2 > tagged
}
