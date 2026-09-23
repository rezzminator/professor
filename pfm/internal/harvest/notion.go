package harvest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The Notion page extractor. A public Notion page (on {name}.notion.site or
// www.notion.so) serves an empty application shell: its blocks load after the
// page from the site's own API on the page's host, unauthenticated. The
// extractor reads them the way the site's client does, through loaders.go's
// budget: the page chunk (POST /api/v3/loadPageChunk, its cursor followed while
// it names more), then the records of every block the tree names that the
// chunk does not hold — a toggle's answer, a toggleable heading's children,
// their children in turn — from /api/v3/syncRecordValuesSpace, up to
// notionRecordBatch blocks a request, round after round until the tree is
// closed. Each answer is checked to be the page's (the first chunk holds the
// page's own block; a records answer holds only blocks it was asked for, each
// under its own id) before it is kept in the page as a harvester-notion-answer
// element, so a later conversion replays it like any followed loader. The tree
// renders in order; every block the tree names is counted, stated · loaded,
// and a block the site serves no anonymous reader, one never read, or one of a
// kind the harvester does not render is named, never dropped silently.

const (
	notionAnswerTag = "harvester-notion-answer"
	// notionChunkPages bounds the page chunks read for one page.
	notionChunkPages = 20
	// notionRecordBatch is the most block records one request asks for.
	notionRecordBatch = 100
	notionChunkLimit  = 100
	notionKindChunk   = "chunk"
	notionKindRecords = "records"
	notionTypePage    = "page"
	notionTypeText    = "text"
)

var (
	notionHosts = []string{"notion.site", "notion.so"}
	notionID    = regexp.MustCompile(`([0-9a-f]{32})$`)
)

// notionPageID reads the page id a Notion address ends in (a slug's last 32
// hex digits, dashed or not), as the API spells it; "" when it names none.
func notionPageID(page *url.URL) string {
	segment := strings.ToLower(strings.ReplaceAll(path.Base(page.Path), "-", ""))
	match := notionID.FindStringSubmatch(segment)
	if match == nil {
		return ""
	}
	id := match[1]
	return id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
}

// isNotionPage accepts a Notion address naming a page.
func isNotionPage(page *url.URL) bool {
	return notionPageID(page) != ""
}

type notionFormat struct {
	PageIcon      string   `json:"page_icon"`
	DisplaySource string   `json:"display_source"`
	ColumnOrder   []string `json:"table_block_column_order"`
	AliasPointer  struct {
		ID string `json:"id"`
	} `json:"alias_pointer"`
}

type notionBlock struct {
	ID         string                     `json:"id"`
	Type       string                     `json:"type"`
	Properties map[string]json.RawMessage `json:"properties"`
	Content    []string                   `json:"content"`
	Format     notionFormat               `json:"format"`
	Alive      *bool                      `json:"alive"`
	SpaceID    string                     `json:"space_id"`
}

// notionRecord is one block's record: its value, or — for a block the site
// serves no anonymous reader — a role and no value.
type notionRecord struct {
	SpaceID string `json:"spaceId"`
	Value   struct {
		Role  string       `json:"role"`
		Value *notionBlock `json:"value"`
	} `json:"value"`
}

type notionRecordMap struct {
	Block map[string]notionRecord `json:"block"`
}

type notionAnswer struct {
	Cursor    json.RawMessage  `json:"cursor"`
	RecordMap *notionRecordMap `json:"recordMap"`
}

// notionKept is a records answer as the page keeps it: the ids it was asked
// for beside the answer, so an id the site answered nothing for is not asked
// again.
type notionKept struct {
	Asked  []string        `json:"asked"`
	Answer json.RawMessage `json:"answer"`
}

// notionPage is what a page holds: its id and the answers kept for it.
type notionPage struct {
	id      string
	answers int
	chunks  int
	cursor  json.RawMessage
	blocks  map[string]notionRecord
	asked   map[string]bool
	records int
	dropped bool
}

// notionPageOf reads the page a Notion address names and the answers kept for
// it; false for an address naming no page.
func notionPageOf(doc *html.Node, page *url.URL) (notionPage, bool) {
	state := notionPage{id: notionPageID(page), blocks: map[string]notionRecord{}, asked: map[string]bool{}}
	if state.id == "" {
		return notionPage{}, false
	}
	for _, node := range keptAnswers(doc, notionAnswerTag) {
		if nodeAttr(node, "dropped") != "" {
			state.dropped = true
			continue
		}
		if number, err := strconv.Atoi(nodeAttr(node, "page")); err != nil || number != state.answers {
			obs.Logger(context.Background()).Warn("harvest: a kept Notion answer is out of order; left out",
				"page", nodeAttr(node, "page"))
			break
		}
		if err := state.keep(nodeAttr(node, "kind"), []byte(rawText(node))); err != nil {
			obs.Logger(context.Background()).Warn("harvest: a kept Notion answer no longer decodes; left out",
				"page", nodeAttr(node, "page"), obs.FieldErr, err.Error())
			break
		}
		state.answers++
	}
	return state, true
}

// keep merges one kept answer into the state.
func (state *notionPage) keep(kind string, body []byte) error {
	var answer notionAnswer
	switch kind {
	case notionKindChunk:
		if err := json.Unmarshal(body, &answer); err != nil {
			return err
		}
		state.chunks++
		state.cursor = answer.Cursor
	case notionKindRecords:
		var kept notionKept
		if err := json.Unmarshal(body, &kept); err != nil {
			return err
		}
		if err := json.Unmarshal(kept.Answer, &answer); err != nil {
			return err
		}
		for _, id := range kept.Asked {
			state.asked[id] = true
		}
		state.records++
	default:
		return fmt.Errorf("unknown answer kind %q", kind)
	}
	if answer.RecordMap == nil {
		return fmt.Errorf("a %s answer holds no record map", kind)
	}
	for id, record := range answer.RecordMap.Block {
		if _, seen := state.blocks[id]; !seen {
			state.blocks[id] = record
		}
	}
	return nil
}

// block is the served value of id's record; nil when the page holds none.
func (state *notionPage) block(id string) *notionBlock {
	return state.blocks[id].Value.Value
}

// chunkMore reports whether the last chunk's cursor names more of the page.
func (state *notionPage) chunkMore() bool {
	var cursor struct {
		Stack []json.RawMessage `json:"stack"`
	}
	if err := json.Unmarshal(state.cursor, &cursor); err != nil {
		obs.Logger(context.Background()).Warn("harvest: a Notion chunk's cursor does not decode; no further chunk",
			obs.FieldErr, err.Error())
		return false
	}
	return len(cursor.Stack) > 0
}

// reach lists every block id the page's tree names, in tree order, the page
// first: a served, living block's children follow it; a child page is named
// but not entered (it is a page of its own).
func (state *notionPage) reach() []string {
	var order []string
	seen := map[string]bool{}
	var walk func(id string)
	walk = func(id string) {
		if seen[id] {
			return
		}
		seen[id] = true
		order = append(order, id)
		block := state.block(id)
		if block == nil || (block.Alive != nil && !*block.Alive) || (id != state.id && notionLinksOut(block.Type)) {
			return
		}
		for _, child := range block.Content {
			walk(child)
		}
	}
	walk(state.id)
	return order
}

// notionLinksOut reports a block that stands for a page of its own.
func notionLinksOut(kind string) bool {
	return kind == notionTypePage || kind == "collection_view_page" || kind == "alias"
}

// notionLoaders names the next page chunk while the chunks are not done, then
// the next batch of block records the tree names and the page lacks.
func notionLoaders(doc *html.Node, page *url.URL) []pageLoader {
	state, ok := notionPageOf(doc, page)
	if !ok || state.dropped {
		return nil
	}
	number := state.answers
	if state.chunks == 0 || (state.records == 0 && state.chunks < notionChunkPages && state.chunkMore()) {
		return []pageLoader{notionChunkLoader(doc, page, &state, number)}
	}
	root := state.block(state.id)
	if root == nil {
		return nil
	}
	space := root.SpaceID
	if space == "" {
		space = state.blocks[state.id].SpaceID
	}
	var want []string
	for _, id := range state.reach() {
		if _, held := state.blocks[id]; !held && !state.asked[id] {
			want = append(want, id)
		}
	}
	if len(want) == 0 || space == "" {
		return nil
	}
	want = want[:min(len(want), notionRecordBatch)]
	requests := make([]map[string]any, 0, len(want))
	asked := map[string]bool{}
	for _, id := range want {
		asked[id] = true
		requests = append(requests, map[string]any{
			"pointer": map[string]string{"table": "block", "id": id, "spaceId": space}, "version": -1,
		})
	}
	key := fmt.Sprintf("notion-records %s %d", state.id, number)
	return []pageLoader{notionLoader(page, key, fmt.Sprintf("the page's block records (request %d)", number+1),
		"/api/v3/syncRecordValuesSpace", map[string]any{"requests": requests},
		func(body []byte, contentType string) error {
			if err := checkNotionRecords(body, contentType, asked); err != nil {
				return err
			}
			kept, err := json.Marshal(notionKept{Asked: want, Answer: body})
			if err != nil {
				return fmt.Errorf("the records answer could not be kept: %w", err)
			}
			keepAnswer(doc, notionAnswerTag, key, notionKindRecords, number, kept)
			return nil
		},
		func() { keepAnswer(doc, notionAnswerTag, key, notionKindRecords, number, nil) })}
}

// notionChunkLoader names the page's next chunk.
func notionChunkLoader(doc *html.Node, page *url.URL, state *notionPage, number int) pageLoader {
	cursor := state.cursor
	if state.chunks == 0 {
		cursor = json.RawMessage(`{"stack":[]}`)
	}
	chunk := state.chunks
	key := fmt.Sprintf("notion-chunk %s %d", state.id, chunk)
	request := map[string]any{
		"pageId": state.id, "limit": notionChunkLimit, "cursor": cursor, "chunkNumber": chunk,
		"verticalColumns": false,
	}
	id := state.id
	return notionLoader(page, key, fmt.Sprintf("the page's block chunk %d", chunk+1), "/api/v3/loadPageChunk", request,
		func(body []byte, contentType string) error {
			if err := checkNotionChunk(body, contentType, id, chunk == 0); err != nil {
				return err
			}
			keepAnswer(doc, notionAnswerTag, key, notionKindChunk, number, body)
			return nil
		},
		func() { keepAnswer(doc, notionAnswerTag, key, notionKindChunk, number, nil) })
}

// notionLoader is one JSON POST to the site's API on the page's own host.
func notionLoader(
	page *url.URL,
	key, label, apiPath string,
	request map[string]any,
	graft func(body []byte, contentType string) error,
	drop func(),
) pageLoader {
	body, err := json.Marshal(request)
	if err != nil {
		obs.Logger(context.Background()).Warn("harvest: a Notion API request could not be encoded",
			"kind", label, obs.FieldErr, err.Error())
	}
	return pageLoader{
		key:     key,
		label:   label,
		method:  http.MethodPost,
		target:  (&url.URL{Scheme: page.Scheme, Host: page.Host, Path: apiPath}).String(),
		body:    body,
		headers: map[string]string{headerContentType: mediaTypeJSON, headerAccept: mediaTypeJSON},
		graft:   graft,
		drop:    drop,
	}
}

// checkNotionChunk proves an answer is a chunk of the page's block tree: a
// record map and a cursor, and — the first chunk — the page's own block.
func checkNotionChunk(body []byte, contentType, pageID string, first bool) error {
	var answer notionAnswer
	if err := json.Unmarshal(body, &answer); err != nil || answer.RecordMap == nil || answer.Cursor == nil {
		return fmt.Errorf("answered by a %s body that is not a chunk of the page's block tree",
			discourseContentType(contentType))
	}
	if root := answer.RecordMap.Block[pageID].Value.Value; first && (root == nil || root.ID != pageID) {
		return fmt.Errorf("answered by a chunk that does not hold the page's own block")
	}
	return nil
}

// checkNotionRecords proves an answer is the records asked for: a record map
// holding only asked ids, each served block under its own id.
func checkNotionRecords(body []byte, contentType string, asked map[string]bool) error {
	var answer notionAnswer
	if err := json.Unmarshal(body, &answer); err != nil || answer.RecordMap == nil {
		return fmt.Errorf("answered by a %s body that is not a set of the page's block records",
			discourseContentType(contentType))
	}
	for id, record := range answer.RecordMap.Block {
		if !asked[id] {
			return fmt.Errorf("answered with a block record it was not asked for")
		}
		if block := record.Value.Value; block != nil && block.ID != id {
			return fmt.Errorf("answered with a block record under another block's id")
		}
	}
	return nil
}

// extractNotionPage renders a Notion page's block tree from the answers kept
// in its page; false, naming why, when the tree did not load.
func extractNotionPage(doc *html.Node, page *url.URL) (siteExtraction, bool) {
	state, ok := notionPageOf(doc, page)
	if !ok {
		return siteExtraction{}, false
	}
	root := state.block(state.id)
	if root == nil {
		return siteExtraction{
			unrendered: "the page's block tree did not load from the site's page-chunk API",
		}, false
	}
	renderer := notionRenderer{state: &state, base: page, seen: map[string]bool{}, rendered: map[string]bool{}}
	body := renderer.children(root.Content)
	renderer.rendered[state.id] = true
	reached := state.reach()
	types := map[string]int{}
	unrendered := map[string]int{}
	loaded, unserved, unread, dead := 0, 0, 0, 0
	for _, id := range reached {
		record, held := state.blocks[id]
		block := record.Value.Value
		switch {
		case block != nil:
			loaded++
			types[block.Type]++
			if block.Alive != nil && !*block.Alive {
				dead++
			} else if !renderer.rendered[id] {
				unrendered[block.Type]++
			}
		case held || state.asked[id]:
			unserved++
		default:
			unread++
		}
	}
	title := renderer.plain(root.Properties["title"])
	if title == "" {
		title = "Untitled"
	}
	counts := fmt.Sprintf("**Blocks:** %d stated · %d loaded", len(reached), loaded)
	if dead > 0 {
		counts += fmt.Sprintf(" (%s deleted, which the site does not show)", notionBlocks(dead))
	}
	lines := []string{
		"# " + title, "",
		"**Page:** https://" + page.Host + page.Path,
		counts,
		"**Block types:** " + notionTally(types),
		"", body,
	}
	var partial string
	if loaded < len(reached) {
		partial = fmt.Sprintf("notion page: %d of %d blocks loaded", loaded, len(reached))
	}
	if unserved > 0 {
		partial = joinReasons(partial, notionBlocks(unserved)+" the site serves to no anonymous reader")
	}
	if unread > 0 {
		partial = joinReasons(partial, notionBlocks(unread)+" not read: the block records request did not complete")
	}
	if state.chunks >= notionChunkPages && state.chunkMore() {
		partial = joinReasons(partial, fmt.Sprintf("the page's chunks continue past the %d the harvester reads",
			notionChunkPages))
	}
	if len(unrendered) > 0 {
		missing := 0
		for _, count := range unrendered {
			missing += count
		}
		partial = joinReasons(partial, fmt.Sprintf("%s of a kind the harvester does not render (%s)",
			notionBlocks(missing), notionTally(unrendered)))
	}
	return siteExtraction{markdown: strings.Join(lines, "\n"), partial: partial, apiRecord: true}, true
}

// notionBlocks counts blocks in words: "1 block", "3 blocks".
func notionBlocks(count int) string {
	if count == 1 {
		return "1 block"
	}
	return strconv.Itoa(count) + " blocks"
}

// notionTally lists counts by kind, the kinds sorted: "text 3, toggle 2".
func notionTally(counts map[string]int) string {
	parts := make([]string, 0, len(counts))
	for kind, count := range counts {
		parts = append(parts, fmt.Sprintf("%s %d", kind, count))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}
