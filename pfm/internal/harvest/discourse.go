package harvest

import (
	"bytes"
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
)

// The Discourse topic extractor. Every Discourse forum, on any domain, names
// itself in <meta name="generator" content="Discourse …">, and serves a client
// without JavaScript its crawler view: the topic's posts as plain HTML, about
// 20 to a page (div.crawler-post id="post_N" inside the topic's
// schema.org DiscussionForumPosting — a QAPage on a forum running the solved
// plugin), the pages chained by <link rel="next"> /
// <link rel="prev"> (?page=N). A main-content extractor keeps the first post
// of that and drops the rest; this one renders every post in the page.
// discourseLoaders names, for Go to follow (loaders.go), the previous and next
// pages still linked, the topic's first page when the page is a single post's
// (/t/slug/id/N carries post N alone, unlinked), and the topic's JSON
// (/t/slug/id.json), whose posts_count is the count the topic states — the
// crawler view states none. A page's answer is merged into the topic by post
// number — only once both it and the page name the same topic, so a page of
// unknown identity merges nothing — and its own links replace the ones it was
// reached by, so following ends when no page is left. The extractor then
// reconciles the posts it rendered against the stated count: a page link left
// unfollowed, a stated post no page served, and a count not read each flag the
// artifact partial and are named.

// discourseCountMeta carries the topic's stated post count into the page once
// its JSON is read; discourseFollowedMeta marks a synthetic loader (the count,
// the first page) this page already holds the answer of.
const (
	discourseCountMeta    = "harvester-discourse-posts-count"
	discourseFollowedMeta = "harvester-discourse-followed"
)

// The directions a topic page is reached in: by a rel="prev" or rel="next"
// link, or as the topic's first page.
const (
	relPrev        = "prev"
	relNext        = "next"
	discourseFirst = "first"
)

var (
	// discourseTopicIDRe reads the topic id off a topic URL's path:
	// /t/{slug}/{id} or /t/{id}, under any subfolder the forum is served at.
	discourseTopicIDRe = regexp.MustCompile(`/t/(?:[^/]+/)?(\d+)/?$`)
	discoursePostIDRe  = regexp.MustCompile(`^post_(\d+)$`)
)

// isDiscourse reports a page Discourse generated, by its generator meta.
func isDiscourse(doc *html.Node) bool {
	found := false
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if found {
			return
		}
		if node.Type == html.ElementNode && node.DataAtom == atom.Meta &&
			strings.EqualFold(nodeAttr(node, "name"), "generator") {
			content := strings.ToLower(strings.TrimSpace(nodeAttr(node, "content")))
			found = content == "discourse" || strings.HasPrefix(content, "discourse ")
			if found {
				return
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return found
}

// discourseTopic is a Discourse topic page's crawler view.
type discourseTopic struct {
	// root is the topic's schema.org element, the posts' container.
	root *html.Node
	// url is the topic's own address (no page, no post number); nil when the
	// page names none. id is its topic id, "" when url is nil.
	url *url.URL
	id  string
	// posts are the crawler posts in the page, in DOM order.
	posts []*html.Node
}

// discourseTopicOf reads doc as a Discourse topic page; false for any other
// page (a category listing, a user page, a wall, a non-Discourse page).
func discourseTopicOf(doc *html.Node, page *url.URL) (discourseTopic, bool) {
	if !isDiscourse(doc) {
		return discourseTopic{}, false
	}
	root := discourseTopicRoot(doc)
	if root == nil {
		return discourseTopic{}, false
	}
	topic := discourseTopic{root: root, posts: discoursePosts(root)}
	if len(topic.posts) == 0 {
		return discourseTopic{}, false
	}
	var candidates []string
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if isElement(child, "link") && nodeAttr(child, "itemprop") == "url" {
			candidates = append(candidates, nodeAttr(child, "href"))
		}
	}
	candidates = append(candidates, relTargets(doc, "canonical")...)
	for _, candidate := range candidates {
		parsed, err := url.Parse(strings.TrimSpace(candidate))
		if err != nil || candidate == "" {
			continue
		}
		resolved := page.ResolveReference(parsed)
		resolved.RawQuery, resolved.Fragment = "", ""
		resolved.Path = strings.TrimSuffix(resolved.Path, "/")
		if match := discourseTopicIDRe.FindStringSubmatch(resolved.Path); match != nil {
			topic.url, topic.id = resolved, match[1]
			break
		}
	}
	return topic, true
}

// discourseTopicRoot is the first element whose itemtype is the topic's:
// schema.org/DiscussionForumPosting, or schema.org/QAPage where the solved
// plugin marks the topic up as a question and its answers.
func discourseTopicRoot(node *html.Node) *html.Node {
	if node.Type == html.ElementNode {
		itemType := nodeAttr(node, "itemtype")
		if strings.HasSuffix(itemType, "schema.org/DiscussionForumPosting") ||
			strings.HasSuffix(itemType, "schema.org/QAPage") {
			return node
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := discourseTopicRoot(child); found != nil {
			return found
		}
	}
	return nil
}

// discoursePosts returns the crawler posts under root in DOM order: the
// div.crawler-post elements whose id is post_N (the page's navigation block is
// classed crawler-post too, without an id).
func discoursePosts(root *html.Node) []*html.Node {
	var posts []*html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type != html.ElementNode {
				continue
			}
			if hasClass(child, "crawler-post") && discoursePostNumber(child) > 0 {
				posts = append(posts, child)
				continue
			}
			walk(child)
		}
	}
	walk(root)
	return posts
}

// discoursePostNumber is a crawler post's number, from its id post_N; 0 when
// the element is not one.
func discoursePostNumber(post *html.Node) int {
	match := discoursePostIDRe.FindStringSubmatch(nodeAttr(post, "id"))
	if match == nil {
		return 0
	}
	number, err := strconv.Atoi(match[1])
	if err != nil {
		return 0
	}
	return number
}

// hasPost reports post number in topic.
func (topic discourseTopic) hasPost(number int) bool {
	for _, post := range topic.posts {
		if discoursePostNumber(post) == number {
			return true
		}
	}
	return false
}

// discourseStated is the stated post count the topic's JSON put in doc.
func discourseStated(doc *html.Node) (int, bool) {
	if meta := firstWithAttr(doc, "name", discourseCountMeta, nil); meta != nil {
		return redditCount(nodeAttr(meta, "content"))
	}
	return 0, false
}

// discourseMarked reports a synthetic loader doc already holds the answer of.
func discourseMarked(doc *html.Node, key string) bool {
	var found bool
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if found {
			return
		}
		if node.Type == html.ElementNode && node.DataAtom == atom.Meta &&
			nodeAttr(node, "name") == discourseFollowedMeta && nodeAttr(node, "content") == key {
			found = true
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return found
}

// discourseMark adds a <meta name=… content=…> to doc's head.
func discourseMark(doc *html.Node, name, content string) {
	head := firstElement(doc, "head")
	if head == nil {
		head = doc
	}
	head.AppendChild(&html.Node{
		Type:     html.ElementNode,
		Data:     "meta",
		DataAtom: atom.Meta,
		Attr:     []html.Attribute{{Key: "name", Val: name}, {Key: "content", Val: content}},
	})
}

// relElements returns the <link> and <a> elements whose rel names rel.
func relElements(doc *html.Node, rel string) []*html.Node {
	var found []*html.Node
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && (node.DataAtom == atom.Link || node.DataAtom == atom.A) &&
			nodeAttr(node, "href") != "" {
			for _, token := range strings.Fields(strings.ToLower(nodeAttr(node, "rel"))) {
				if token == rel {
					found = append(found, node)
					break
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	return found
}

// relTargets returns the distinct hrefs of doc's rel elements, in DOM order.
func relTargets(doc *html.Node, rel string) []string {
	var targets []string
	seen := map[string]bool{}
	for _, node := range relElements(doc, rel) {
		href := strings.TrimSpace(nodeAttr(node, "href"))
		if !seen[href] {
			seen[href] = true
			targets = append(targets, href)
		}
	}
	return targets
}

// discoursePageLinks returns doc's rel links resolved against page, without
// fragments, distinct, in DOM order.
func discoursePageLinks(doc *html.Node, page *url.URL, rel string) []*url.URL {
	var links []*url.URL
	seen := map[string]bool{}
	for _, href := range relTargets(doc, rel) {
		parsed, err := url.Parse(href)
		if err != nil {
			continue
		}
		resolved := page.ResolveReference(parsed)
		resolved.Fragment = ""
		if !seen[resolved.String()] {
			seen[resolved.String()] = true
			links = append(links, resolved)
		}
	}
	return links
}

// discoursePageLabel names a topic page to a reader: "page 3", "page 1".
func discoursePageLabel(target *url.URL) string {
	if number := target.Query().Get("page"); number != "" {
		return "page " + number
	}
	return "page 1"
}

// discourseLoaders names the loaders still in a Discourse topic page, for
// loaders.go: the topic's JSON while its stated count is not in the page, the
// topic's first page while the page holds neither post #1 nor a link back,
// then every previous and next page still linked.
func discourseLoaders(doc *html.Node, page *url.URL) []pageLoader {
	topic, ok := discourseTopicOf(doc, page)
	if !ok {
		return nil
	}
	referer := page.String()
	var loaders []pageLoader
	if topic.url != nil {
		countTarget := topic.url.String() + ".json"
		countKey := "discourse-count " + countTarget
		if _, stated := discourseStated(doc); !stated && !discourseMarked(doc, countKey) {
			loaders = append(loaders, discourseCountLoader(doc, topic, countKey, countTarget, referer))
		}
		first := topic.url.String()
		firstKey := "discourse-page " + first
		if len(relElements(doc, relPrev)) == 0 && !topic.hasPost(1) && !discourseMarked(doc, firstKey) {
			loaders = append(loaders, discoursePageLoader(doc, page, topic, discourseFirst, topic.url, referer))
		}
	}
	for _, rel := range []string{relPrev, relNext} {
		for _, target := range discoursePageLinks(doc, page, rel) {
			loaders = append(loaders, discoursePageLoader(doc, page, topic, rel, target, referer))
		}
	}
	return loaders
}

// discourseCountLoader reads the topic's stated post count off its JSON.
func discourseCountLoader(doc *html.Node, topic discourseTopic, key, target, referer string) pageLoader {
	return pageLoader{
		key:     key,
		label:   "the topic's post count",
		method:  http.MethodGet,
		target:  target,
		headers: map[string]string{headerReferer: referer, headerAccept: "application/json"},
		graft: func(body []byte, contentType string) error {
			var answer struct {
				ID         json.Number `json:"id"`
				PostsCount *int        `json:"posts_count"`
			}
			if err := json.Unmarshal(body, &answer); err != nil || answer.PostsCount == nil {
				return fmt.Errorf(
					"answered by a %s body that is not the topic's JSON",
					discourseContentType(contentType),
				)
			}
			switch {
			case answer.ID == "":
				return fmt.Errorf("answered by JSON naming no topic id, not verifiable as topic %s", topic.id)
			case answer.ID.String() != topic.id:
				return fmt.Errorf("answered by the JSON of topic %s, not topic %s", answer.ID, topic.id)
			}
			discourseMark(doc, discourseCountMeta, strconv.Itoa(*answer.PostsCount))
			discourseMark(doc, discourseFollowedMeta, key)
			return nil
		},
		drop: func() { discourseMark(doc, discourseFollowedMeta, key) },
	}
}

// discoursePageLoader fetches one page of the topic (rel "prev", "next", or
// "first" for the topic's first page) and merges its posts in by number; the
// page's own links in that direction replace the one it was reached by.
func discoursePageLoader(
	doc *html.Node,
	page *url.URL,
	topic discourseTopic,
	rel string,
	target *url.URL,
	referer string,
) pageLoader {
	key := "discourse-page " + target.String()
	label := map[string]string{
		relPrev:        "previous page",
		relNext:        "next page",
		discourseFirst: "the topic's first page",
	}[rel]
	if rel != discourseFirst {
		label += " (" + discoursePageLabel(target) + ")"
	}
	unlink := func() {
		for _, direction := range []string{relPrev, relNext} {
			for _, node := range relElements(doc, direction) {
				if parsed, err := url.Parse(nodeAttr(node, "href")); err == nil {
					resolved := page.ResolveReference(parsed)
					resolved.Fragment = ""
					if resolved.String() == target.String() {
						detach(node)
					}
				}
			}
		}
	}
	return pageLoader{
		key:     key,
		label:   label,
		method:  http.MethodGet,
		target:  target.String(),
		headers: map[string]string{headerReferer: referer},
		graft: func(body []byte, contentType string) error {
			answer, err := html.Parse(bytes.NewReader(body))
			if err != nil {
				return fmt.Errorf(
					"answered by a %s body that does not parse as HTML",
					discourseContentType(contentType),
				)
			}
			answered, ok := discourseTopicOf(answer, target)
			if !ok {
				return fmt.Errorf("answered by a page (%s, titled %q) that is not a Discourse topic page",
					discourseContentType(contentType), pageTitle(answer))
			}
			switch {
			case topic.id == "" || answered.id == "":
				// Unknown identity fails closed: posts merged in are
				// presented as this topic's.
				return fmt.Errorf("answered by a page (titled %q) not verifiable as this topic: "+
					"a page names no topic address", pageTitle(answer))
			case answered.id != topic.id:
				return fmt.Errorf("answered by a page of another topic (titled %q)", pageTitle(answer))
			}
			if discourseMerge(topic.root, answered.posts) == 0 {
				return fmt.Errorf(
					"answered by a page (titled %q) holding no post not already loaded",
					pageTitle(answer),
				)
			}
			directions := []string{rel}
			if rel == discourseFirst {
				directions = []string{relPrev, relNext}
			}
			for _, direction := range directions {
				for _, node := range relElements(doc, direction) {
					detach(node)
				}
				for _, link := range discoursePageLinks(answer, target, direction) {
					discourseLink(doc, direction, link.String())
				}
			}
			unlink()
			discourseMark(doc, discourseFollowedMeta, key)
			return nil
		},
		drop: func() {
			unlink()
			discourseMark(doc, discourseFollowedMeta, key)
		},
	}
}

// discourseLink adds a <link rel=rel href=href> to doc's head.
func discourseLink(doc *html.Node, rel, href string) {
	head := firstElement(doc, "head")
	if head == nil {
		head = doc
	}
	head.AppendChild(&html.Node{
		Type:     html.ElementNode,
		Data:     "link",
		DataAtom: atom.Link,
		Attr:     []html.Attribute{{Key: "rel", Val: rel}, {Key: "href", Val: href}},
	})
}

// discourseMerge inserts every post not already under root in its place by
// post number, and returns how many it inserted.
func discourseMerge(root *html.Node, incoming []*html.Node) int {
	existing := discoursePosts(root)
	have := map[int]bool{}
	for _, post := range existing {
		have[discoursePostNumber(post)] = true
	}
	added := 0
	for _, post := range incoming {
		number := discoursePostNumber(post)
		if have[number] {
			continue
		}
		have[number] = true
		detach(post)
		var before *html.Node
		for _, current := range existing {
			if discoursePostNumber(current) > number {
				before = current
				break
			}
		}
		switch {
		case before != nil:
			before.Parent.InsertBefore(post, before)
		case len(existing) > 0:
			last := existing[len(existing)-1]
			last.Parent.InsertBefore(post, last.NextSibling)
		default:
			root.AppendChild(post)
		}
		existing = discoursePosts(root)
		added++
	}
	return added
}

// discourseContentType is an answer's media type without its parameters.
func discourseContentType(contentType string) string {
	media, _, _ := strings.Cut(contentType, ";")
	if media = strings.TrimSpace(media); media != "" {
		return media
	}
	return "untyped"
}

// extractDiscourseTopic renders a Discourse topic page: its header, the count
// line reconciling the posts loaded against the count the topic states, and
// every post in number order with its author, date and likes.
func extractDiscourseTopic(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	topic, ok := discourseTopicOf(doc, page)
	if !ok {
		return siteExtraction{}, false
	}
	renderer := markdownRenderer{base: page}
	loaded := len(topic.posts)
	first, last := discoursePostNumber(topic.posts[0]), discoursePostNumber(topic.posts[loaded-1])

	var gaps []string
	for _, target := range discoursePageLinks(doc, page, relPrev) {
		gaps = append(gaps, fmt.Sprintf("the posts before #%d are not loaded (previous page, %s)",
			first, discoursePageLabel(target)))
	}
	if topic.url != nil && len(relElements(doc, relPrev)) == 0 && !topic.hasPost(1) &&
		!discourseMarked(doc, "discourse-page "+topic.url.String()) {
		gaps = append(gaps, fmt.Sprintf("the posts before #%d are not loaded (the topic's first page)", first))
	}
	for _, target := range discoursePageLinks(doc, page, relNext) {
		gaps = append(gaps, fmt.Sprintf("the posts after #%d are not loaded (next page, %s)",
			last, discoursePageLabel(target)))
	}
	// Without the stated count, posts no page links cannot be ruled out: an
	// unread count is a gap, and an unrequestable one (no topic address to
	// read it from) is named apart from one whose read failed.
	stated, statedKnown := discourseStated(doc)
	statedText := "count not read (the topic's JSON was not fetched or not readable)"
	countLine := ""
	switch {
	case statedKnown:
		statedText = strconv.Itoa(stated)
		rest := stated - loaded
		switch {
		case rest > 0 && len(gaps) == 0:
			gaps = append(gaps, fmt.Sprintf(
				"%d stated post(s) in no page served (hidden, deleted or not shown to a signed-out reader)", rest))
		case rest > 0:
			gaps = append(gaps, fmt.Sprintf("%d stated post(s) not loaded", rest))
		case rest < 0:
			countLine = fmt.Sprintf(" · %d more loaded than stated (posted while the pages were read)", -rest)
		}
	case topic.url == nil:
		statedText = "count not read (the page names no topic address to read it from)"
		gaps = append(gaps, "the stated post count could not be requested (the page names no topic address)")
	default:
		gaps = append(gaps, "the stated post count was not read")
	}
	countLine = fmt.Sprintf("**Posts:** %s stated · %d loaded", statedText, loaded) + countLine
	if len(gaps) > 0 {
		countLine += " · gaps: " + strings.Join(gaps, "; ")
	}

	var out strings.Builder
	title := ""
	var meta []string
	for child := topic.root.FirstChild; child != nil; child = child.NextSibling {
		if !isElement(child, "meta") {
			continue
		}
		switch nodeAttr(child, "itemprop") {
		case "headline", "name":
			title = strings.TrimSpace(nodeAttr(child, "content"))
		case "articleSection":
			if section := strings.TrimSpace(nodeAttr(child, "content")); section != "" {
				meta = append(meta, "**Category:** "+section)
			}
		}
	}
	if title == "" {
		title = pageTitle(doc)
	}
	out.WriteString("# " + title + "\n\n")
	if len(meta) > 0 {
		out.WriteString(strings.Join(meta, " · ") + "  \n")
	}
	if topic.url != nil {
		out.WriteString("**Topic:** " + topic.url.String() + "  \n")
	}
	out.WriteString(countLine + "\n\n---\n\n")
	for _, post := range topic.posts {
		out.WriteString(discourseRenderPost(post, renderer))
	}

	partial := ""
	if len(gaps) > 0 {
		progress := fmt.Sprintf("%d posts loaded, the stated count not read", loaded)
		if statedKnown {
			progress = fmt.Sprintf("%d of %d posts loaded", loaded, stated)
		}
		partial = "discourse topic: " + progress + " — " + strings.Join(gaps, "; ")
	}
	// A browser render of a Discourse topic is its JavaScript app, which
	// shows no more of the topic than the crawler pages Go follows.
	return siteExtraction{markdown: out.String(), partial: partial}, true
}

// discourseRenderPost renders one crawler post: a heading with its number,
// author, date and likes, then its body.
func discourseRenderPost(post *html.Node, renderer markdownRenderer) string {
	header := "## #" + strconv.Itoa(discoursePostNumber(post))
	var body *html.Node
	var author, date string
	// likes is -1 until a like count is read; likesUnread marks one stated
	// but not a number, rendered as such rather than as no likes.
	likes, likesUnread := -1, false
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type != html.ElementNode {
				continue
			}
			switch {
			case body == nil && hasClass(child, "post") && nodeAttr(child, "itemprop") == "text":
				body = child
				continue
			case author == "" && nodeAttr(child, "itemprop") == "author":
				if name := firstWithAttr(child, "itemprop", "name", nil); name != nil {
					author = nodeText(name)
				}
			case date == "" && child.DataAtom == atom.Time && hasClass(child, "post-time"):
				date = discourseDate(nodeAttr(child, "datetime"))
			case likes < 0 && !likesUnread && nodeAttr(child, "itemprop") == "interactionStatistic":
				if kind := firstWithAttr(child, "itemprop", "interactionType", nil); kind != nil &&
					strings.HasSuffix(nodeAttr(kind, "content"), "LikeAction") {
					if count := firstWithAttr(child, "itemprop", "userInteractionCount", nil); count != nil {
						if read, ok := redditCount(nodeAttr(count, "content")); ok {
							likes = read
						} else {
							likesUnread = true
						}
					}
				}
				continue
			}
			walk(child)
		}
	}
	walk(post)
	if author == "" {
		author = "[unknown]"
	}
	header += " · " + author
	if date != "" {
		header += " · " + date
	}
	switch {
	case likesUnread:
		header += " · likes unread"
	case likes == 1:
		header += " · 1 like"
	case likes > 1:
		header += " · " + strconv.Itoa(likes) + " likes"
	}
	var out strings.Builder
	out.WriteString(header + "\n\n")
	if body != nil {
		discourseTidyBody(body)
		if blocks := renderer.blocks(body); len(blocks) > 0 {
			out.WriteString(strings.Join(blocks, "\n\n") + "\n\n")
		}
	}
	return out.String()
}

// discourseTidyBody rewrites a post body's cooked-markup furniture for the
// renderer: an emoji image becomes its :name:, an avatar and an image's size
// caption are dropped, and a quote or link preview (an <aside>) becomes a
// block, so its blockquote keeps its shape.
func discourseTidyBody(body *html.Node) {
	var drop []*html.Node
	var walk func(node *html.Node, lightbox bool)
	walk = func(node *html.Node, lightbox bool) {
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			if child.Type != html.ElementNode {
				continue
			}
			switch {
			case child.DataAtom == atom.Img && hasClass(child, "emoji"):
				name := nodeAttr(child, "alt")
				if name == "" {
					name = nodeAttr(child, "title")
				}
				child.Type, child.Data, child.DataAtom, child.Attr = html.TextNode, name, 0, nil
				continue
			case child.DataAtom == atom.Img && hasClass(child, "avatar"), lightbox && hasClass(child, "meta"):
				drop = append(drop, child)
				continue
			case child.DataAtom == atom.Aside:
				child.Data, child.DataAtom = "div", atom.Div
			}
			walk(child, lightbox || hasClass(child, "lightbox") || hasClass(child, "lightbox-wrapper"))
		}
	}
	walk(body, false)
	for _, node := range drop {
		detach(node)
	}
}

// discourseDate renders a post's ISO timestamp as "2006-01-02 15:04 UTC".
func discourseDate(raw string) string {
	raw = strings.TrimSpace(raw)
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return raw
	}
	return parsed.UTC().Format("2006-01-02 15:04 UTC")
}
