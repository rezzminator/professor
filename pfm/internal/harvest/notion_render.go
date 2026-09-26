package harvest

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The Notion block renderer (notion.go reads the tree): each block type to
// its Markdown form, in tree order — headings, text, lists, to-dos, toggles
// (a bold summary followed by their children), quotes, callouts, code,
// equations, dividers, tables, media and bookmarks as links, child pages as
// links. rendered records every block it rendered, so the extractor names the
// rest.

// notionRenderer renders one page's tree.
type notionRenderer struct {
	state    *notionPage
	base     *url.URL
	seen     map[string]bool
	rendered map[string]bool
}

// notionHeadings maps a heading block to its Markdown level (the page title
// is the level-1 heading).
var notionHeadings = map[string]int{"header": 2, "sub_header": 3, "sub_sub_header": 4}

// notionMedia names the blocks that render as a link to what they embed.
var notionMedia = func() map[string]bool {
	kinds := map[string]bool{}
	for _, kind := range strings.Fields("video audio file pdf embed figma tweet gist codepen maps drive typeform " +
		"excalidraw") {
		kinds[kind] = true
	}
	return kinds
}()

// notionJoin joins the non-empty parts as Markdown blocks.
func notionJoin(parts ...string) string {
	kept := parts[:0:0]
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, "\n\n")
}

// children renders ids in order; a run of numbered items counts from 1.
func (renderer *notionRenderer) children(ids []string) string {
	var parts []string
	number := 0
	for _, id := range ids {
		if block := renderer.state.block(id); block != nil && block.Type == "numbered_list" {
			number++
		} else {
			number = 0
		}
		parts = append(parts, renderer.block(id, number))
	}
	return notionJoin(parts...)
}

// block renders one block and its children; "" for a block the page does
// not hold, one already rendered, or one of a kind it does not render (left
// out of rendered, so the extractor names it).
func (renderer *notionRenderer) block(id string, number int) string {
	block := renderer.state.block(id)
	if block == nil || renderer.seen[id] || (block.Alive != nil && !*block.Alive) {
		return ""
	}
	renderer.seen[id] = true
	text, ok := renderer.render(block, number)
	renderer.rendered[id] = ok
	return text
}

func (renderer *notionRenderer) render(block *notionBlock, number int) (string, bool) {
	title := renderer.rich(block.Properties["title"], false)
	children := func() string { return renderer.children(block.Content) }
	if level, heading := notionHeadings[block.Type]; heading {
		return notionJoin(strings.Repeat("#", level)+" "+strings.Join(strings.Fields(title), " "), children()), true
	}
	if notionMedia[block.Type] {
		source := renderer.source(block)
		if source == "" {
			return "", false
		}
		label := notionJoin(renderer.rich(block.Properties["caption"], false))
		if label == "" {
			label = block.Type
		}
		return notionJoin("["+label+"]("+source+")", children()), true
	}
	switch block.Type {
	case notionTypeText:
		return notionJoin(title, children()), true
	case "toggle":
		summary := renderer.rich(block.Properties["title"], true)
		return notionJoin(wrapInline(summary, "**"), children()), true
	case "bulleted_list":
		return notionItem("- ", title, children()), true
	case "numbered_list":
		return notionItem(strconv.Itoa(max(number, 1))+". ", title, children()), true
	case "to_do":
		box := "- [ ] "
		if renderer.plain(block.Properties["checked"]) == "Yes" {
			box = "- [x] "
		}
		return notionItem(box, title, children()), true
	case "quote":
		return prefixLines(notionJoin(title, children()), "> ", ">"), true
	case "callout":
		if icon := block.Format.PageIcon; icon != "" && !strings.Contains(icon, "/") {
			title = icon + " " + title
		}
		return prefixLines(notionJoin(title, children()), "> ", ">"), true
	case "code":
		return "```" + renderer.plain(block.Properties["language"]) + "\n" +
			strings.Trim(renderer.plain(block.Properties["title"]), "\n") + "\n```", true
	case "equation":
		return "$$\n" + renderer.plain(block.Properties["title"]) + "\n$$", true
	case "divider":
		return "---", true
	case "bookmark":
		link := renderer.source(block)
		if link == "" {
			return "", false
		}
		label := title
		if label == "" {
			label = link
		}
		return notionJoin("["+label+"]("+link+")", renderer.rich(block.Properties["description"], false)), true
	case "image":
		source := renderer.source(block)
		if source == "" {
			return "", false
		}
		return "![" + renderer.plain(block.Properties["caption"]) + "](" + source + ")", true
	case notionTypePage, "collection_view_page":
		return renderer.pageLink(title, block.ID), true
	case "alias":
		target := block.Format.AliasPointer.ID
		if target == "" {
			return "", false
		}
		label := ""
		if linked := renderer.state.block(target); linked != nil {
			label = renderer.plain(linked.Properties["title"])
		}
		return renderer.pageLink(label, target), true
	case "table":
		return renderer.table(block), true
	case "column_list", "column", "transclusion_container":
		return children(), true
	case "table_of_contents", "breadcrumb":
		// Generated from the page itself; nothing of their own to render.
		return "", true
	case "collection_view", "transclusion_reference":
		// A database view's rows and a synced block's source live in records
		// the page's tree does not name; named by the extractor.
		return "", false
	}
	if title == "" && len(block.Content) == 0 {
		return "", false
	}
	return notionJoin(title, children()), true
}

// notionItem renders a list item: its marker, its text, its children indented
// under it.
func notionItem(marker, title, children string) string {
	return marker + indentAfterFirst(notionJoin(title, children), strings.Repeat(" ", len(marker)))
}

// table renders a table block's rows, the first as the header, cells in the
// block's column order.
func (renderer *notionRenderer) table(block *notionBlock) string {
	var rows []string
	for _, id := range block.Content {
		row := renderer.state.block(id)
		if row == nil || renderer.seen[id] {
			continue
		}
		renderer.seen[id] = true
		renderer.rendered[id] = true
		cells := make([]string, 0, len(block.Format.ColumnOrder))
		for _, column := range block.Format.ColumnOrder {
			cells = append(cells, strings.ReplaceAll(renderer.rich(row.Properties[column], false), "|", `\|`))
		}
		rows = append(rows, "| "+strings.Join(cells, " | ")+" |")
		if len(rows) == 1 {
			rows = append(rows, "|"+strings.Repeat(" --- |", len(cells)))
		}
	}
	return strings.Join(rows, "\n")
}

// source is the address a media or bookmark block names; a file the site
// hosts is reached through the page host's image proxy.
func (renderer *notionRenderer) source(block *notionBlock) string {
	source := renderer.plain(block.Properties["source"])
	if source == "" {
		source = renderer.plain(block.Properties["link"])
	}
	if source == "" {
		source = block.Format.DisplaySource
	}
	switch {
	case source == "":
		return ""
	case strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://"):
		return source
	case strings.HasPrefix(source, "attachment:"):
		return "https://" + renderer.base.Host + "/image/" + url.QueryEscape(source) +
			"?table=block&id=" + block.ID
	}
	return renderer.resolve(source)
}

// pageLink links a page by its id on the page's host.
func (renderer *notionRenderer) pageLink(label, id string) string {
	link := "https://" + renderer.base.Host + "/" + strings.ReplaceAll(id, "-", "")
	if label == "" {
		label = link
	}
	return "[" + label + "](" + link + ")"
}

// resolve makes a link the page names absolute against the page's address.
func (renderer *notionRenderer) resolve(link string) string {
	parsed, err := url.Parse(link)
	if err != nil {
		obs.Logger(context.Background()).Warn("harvest: a Notion link could not be parsed; left as it is",
			"href", logSource(link), obs.FieldErr, err.Error())
		return link
	}
	return renderer.base.ResolveReference(parsed).String()
}

// notionSegments decodes a rich-text property: segments of a text and its
// marks; nil, logged, when it is not one.
func notionSegments(raw json.RawMessage) [][]json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var segments [][]json.RawMessage
	if err := json.Unmarshal(raw, &segments); err != nil {
		obs.Logger(context.Background()).Warn("harvest: a Notion rich-text property does not decode; left out",
			obs.FieldErr, err.Error())
		return nil
	}
	return segments
}

// plain is a rich-text property's text without its marks.
func (renderer *notionRenderer) plain(raw json.RawMessage) string {
	var text strings.Builder
	for _, segment := range notionSegments(raw) {
		var part string
		if len(segment) > 0 && json.Unmarshal(segment[0], &part) == nil {
			text.WriteString(part)
		}
	}
	return strings.TrimSpace(text.String())
}

// rich renders a rich-text property as inline Markdown: bold, italics,
// strike-through, code, links, and the mentions it can name (a page, a
// date, an equation); noBold leaves bold out (a toggle's summary, bold
// already).
func (renderer *notionRenderer) rich(raw json.RawMessage, noBold bool) string {
	var text strings.Builder
	for _, segment := range notionSegments(raw) {
		var part string
		if len(segment) == 0 {
			continue
		}
		if err := json.Unmarshal(segment[0], &part); err != nil {
			obs.Logger(context.Background()).Warn("harvest: a Notion rich-text segment has no text; left out",
				obs.FieldErr, err.Error())
			continue
		}
		var marks [][]json.RawMessage
		if len(segment) > 1 {
			if err := json.Unmarshal(segment[1], &marks); err != nil {
				obs.Logger(context.Background()).Warn("harvest: a Notion rich-text segment's marks do not decode; "+
					"its text kept plain", obs.FieldErr, err.Error())
			}
		}
		text.WriteString(renderer.segment(part, marks, noBold))
	}
	return strings.TrimSpace(text.String())
}

// segment renders one run of text under its marks, its surrounding spaces
// kept outside them.
func (renderer *notionRenderer) segment(text string, marks [][]json.RawMessage, noBold bool) string {
	var bold, italic, strike, code bool
	link := ""
	for _, mark := range marks {
		var name, argument string
		if len(mark) == 0 || json.Unmarshal(mark[0], &name) != nil {
			continue
		}
		if len(mark) > 1 && json.Unmarshal(mark[1], &argument) != nil {
			argument = ""
		}
		switch name {
		case "b":
			bold = true
		case "i":
			italic = true
		case "s":
			strike = true
		case "c":
			code = true
		case "a":
			link = renderer.resolve(argument)
		case "p":
			text = renderer.pageLink(renderer.plain(renderer.titleOf(argument)), argument)
		case "e":
			text = "$" + argument + "$"
		case "d":
			var date struct {
				Start string `json:"start_date"`
				End   string `json:"end_date"`
			}
			if len(mark) > 1 && json.Unmarshal(mark[1], &date) == nil && date.Start != "" {
				text = strings.TrimSuffix(date.Start+" → "+date.End, " → ")
			}
		}
	}
	core := strings.TrimSpace(text)
	if core == "" {
		return text
	}
	lead := text[:len(text)-len(strings.TrimLeftFunc(text, unicode.IsSpace))]
	trail := text[len(lead)+len(core):]
	if code {
		core = "`" + core + "`"
	}
	if strike {
		core = "~~" + core + "~~"
	}
	if italic {
		core = "*" + core + "*"
	}
	if bold && !noBold {
		core = "**" + core + "**"
	}
	if link != "" {
		core = "[" + core + "](" + link + ")"
	}
	return lead + core + trail
}

// titleOf is a held block's title property; nil when the page lacks it.
func (renderer *notionRenderer) titleOf(id string) json.RawMessage {
	if block := renderer.state.block(id); block != nil {
		return block.Properties["title"]
	}
	return nil
}
