package harvestmcp

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

// ReadView is how a read answer renders its items: the MCP read tool's and
// the pfm harvest command's one rendering (RenderReadItem).
type ReadView struct {
	SizeOnly      bool // include_content:false: a size line stands in for the content
	Remote        bool // the remote server: no item names a server path
	InlineCap     int  // the most content characters one item shows; <= 0 is the default
	SearchEnabled bool // a web search backend is configured, so a failure may name harvester_search_web
}

// RenderReadItem is item's text block, n of total in its call:
//
//	=== [n/N] {item as passed}
//	{absolute artifact path}
//	{status lines: partial, truncated — only when the artifact is not clean; or error}
//	{content, or its size}
//
// It reports whether the item failed. An item that failed with no artifact
// is its header and its error line alone.
func RenderReadItem(n, total int, item string, result harvest.Result, view ReadView) (string, bool) {
	label := harvest.PublicSourceLabel(item)
	lines := []string{itemHeader(n, total, item)}
	path := result.Path
	if view.Remote {
		path = ""
	}
	if message := itemFailure(label, result, view); message != "" {
		if result.Error == "" && path != "" {
			lines = append(lines, path)
		}
		return strings.Join(append(lines, "error: "+message), "\n"), true
	}
	if path != "" {
		lines = append(lines, path)
	}
	if result.Partial != "" {
		lines = append(lines, "partial: "+strings.Join(harvest.PublicGaps(result.Partial), "; "))
	}
	if view.SizeOnly {
		lines = append(lines, fmt.Sprintf("size: %d chars, ~%d tokens", result.Chars, result.Tokens))
		return strings.Join(lines, "\n"), false
	}
	content, cut := strings.CutSuffix(result.Content, harvest.InlineTruncationNote)
	limit := view.InlineCap
	if limit <= 0 {
		limit = defaultInlineChars
	}
	if runes := []rune(content); len(runes) > limit {
		content, cut = string(runes[:limit]), true
	}
	if shown := len([]rune(strings.TrimSpace(content))); cut && shown < result.Chars {
		truncated := fmt.Sprintf("truncated: %d of %d chars", shown, result.Chars)
		if path != "" {
			truncated += ", full text at the path"
		}
		lines = append(lines, truncated)
	}
	return strings.Join(append(lines, strings.TrimRight(content, " \t\r\n")), "\n"), false
}

// itemHeader opens item's block, n of total in its call.
func itemHeader(n, total int, item string) string {
	return fmt.Sprintf("=== [%d/%d] %s", n, total, harvest.PublicSourceLabel(item))
}

// failedItem is the block of an item that failed with no artifact.
func failedItem(n, total int, item, message string) string {
	return itemHeader(n, total, item) + "\nerror: " + message
}

// itemFailure is why a read item is a failure, or "": the harvester's own
// error, a size probe of an empty extraction, an empty body, or a thin HTTP
// error or challenge page standing in for one.
func itemFailure(label string, result harvest.Result, view ReadView) string {
	if result.Error != "" {
		return harvest.PublicFailureMessage(result)
	}
	hint := harvest.SearchHint(
		view.SearchEnabled,
		"Use `harvester_search_web` to find an alternative copy, or `harvester_search_literature` if it is a scholarly title.",
		"Use `harvester_search_literature` if it is a scholarly title, or read an alternative copy at another URL.",
	)
	// Size probes never kill an empty extraction behind a zero-sized success;
	// this wording is part of the Python scheduler contract.
	if view.SizeOnly {
		if strings.TrimSpace(result.Content) == "" && result.Chars == 0 && result.Bytes == 0 {
			return fmt.Sprintf(
				"Fetched %s but it yielded no readable content (empty after extraction) — nothing to size. %s",
				label, hint)
		}
		return ""
	}
	stripped := strings.TrimSpace(result.Content)
	status := result.HTTPStatus
	if stripped == "" {
		if result.ErrorKind != "" || result.Challenge || status >= 400 {
			if message := harvest.PublicFailureMessage(result); message != "" {
				return message
			}
		}
		return fmt.Sprintf(
			"Fetched %s but no readable content could be extracted (JS-rendered or bot-blocked — not retrievable from this datacenter IP). %s",
			label,
			hint,
		)
	}
	// A thin HTTP error/challenge page is a failure, not a successful body.
	lowerBody := strings.ToLower(stripped)
	challenge := strings.Contains(lowerBody, "cloudflare") || strings.Contains(lowerBody, "captcha") ||
		strings.Contains(lowerBody, "verify you are human")
	if len([]rune(stripped)) < 500 && (status >= 400 || challenge) {
		return harvest.PublicFailureMessage(
			harvest.Result{HTTPStatus: status, ErrorKind: result.ErrorKind, Challenge: challenge},
		)
	}
	return ""
}

// readView is this service's rendering of read items.
func (service *Service) readView(sizeOnly bool) ReadView {
	return ReadView{
		SizeOnly:      sizeOnly,
		Remote:        service.runtime.Remote,
		InlineCap:     service.inlineCap(),
		SearchEnabled: runtimeSearchEnabled(service.runtime),
	}
}

// inlineCap is output.maxInlineChars from harvester.config.json; an
// unconfigured runtime keeps the default.
func (service *Service) inlineCap() int {
	if service != nil && service.runtime.MaxInlineChars > 0 {
		return service.runtime.MaxInlineChars
	}
	return defaultInlineChars
}

// textResult is a tool answer of one text: an error result when isError.
func textResult(text string, isError bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}, IsError: isError}
}

// readError is a read call refused as a whole: one error line.
func readError(err error) *mcp.CallToolResult {
	return textResult("error: "+err.Error(), true)
}

// RenderFind is found as text — the candidates, one block each, and every
// source's status, since with a failed source no candidate is not proof the
// work is absent: the harvester_search_literature answer and pfm harvest
// search's output.
func RenderFind(query string, found FindOutput) string {
	candidates, sources := found.Candidates, found.Sources
	statuses := make([]string, 0, len(sources))
	var failed bool
	for _, source := range sources {
		status := fmt.Sprintf("%s %s (%d)", source.Source, source.Status, source.Results)
		if source.Error != "" {
			status += ": " + source.Error
		}
		statuses = append(statuses, status)
		failed = failed || source.Status != harvest.SourceAnswered
	}
	footer := ""
	switch {
	case failed:
		footer = "\n\nsources (the records of a source that did not answer are missing from this answer; retry later for them): " +
			strings.Join(
				statuses,
				"; ",
			)
	case len(statuses) > 0:
		footer = "\n\nsources: " + strings.Join(statuses, "; ")
	}
	if len(candidates) == 0 && failed {
		return fmt.Sprintf("No candidate works found for %q among the sources that answered.", query) + footer
	}
	if len(candidates) == 0 {
		return fmt.Sprintf(
			"No candidate works found for %q. Try harvester_search_web (when configured) or a plain web search, or rephrase — a more exact title helps.",
			query,
		) + footer
	}
	blocks := []string{fmt.Sprintf(
		"%d candidate work(s) for %q — pick one and read it with `harvester_read`, passing its handle in publications:",
		len(candidates), query,
	)}
	for index, candidate := range candidates {
		values := []string{}
		for _, value := range []string{candidate.Type, candidate.Authors} {
			if value != "" {
				values = append(values, value)
			}
		}
		if candidate.Year != 0 {
			values = append(values, strconv.Itoa(candidate.Year))
		}
		openAccess := "open access: no"
		if candidate.OpenAccess {
			openAccess = "open access: yes"
		}
		lines := []string{
			fmt.Sprintf("=== [%d/%d] %s", index+1, len(candidates), valueOr(candidate.Title, "(untitled)")),
			"handle: " + candidate.Handle,
		}
		if len(values) > 0 {
			lines = append(lines, strings.Join(values, " · "))
		}
		if ids := idsText(candidate.IDs); ids != "" {
			lines = append(lines, "ids: "+ids)
		}
		blocks = append(blocks, strings.Join(append(lines, openAccess), "\n"))
	}
	return strings.Join(blocks, "\n\n") + footer
}

// idsText is ids as "doi 10.…, arxiv 1706.03762", sorted by name.
func idsText(ids map[string]string) string {
	names := make([]string, 0, len(ids))
	for name := range ids {
		names = append(names, name)
	}
	slices.Sort(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+" "+ids[name])
	}
	return strings.Join(parts, ", ")
}

// renderSearch lists the web results, one block each: title, URL, snippet
// and the engine that found it.
func renderSearch(query string, results []WebResult) string {
	if len(results) == 0 {
		return fmt.Sprintf("No results found for %q. Try different terms or a broader query.", query)
	}
	blocks := []string{fmt.Sprintf(
		"%d result(s) for %q — read the ones you want with `harvester_read`, in urls:", len(results), query,
	)}
	for index, result := range results {
		lines := []string{
			fmt.Sprintf("=== [%d/%d] %s", index+1, len(results), valueOr(result.Title, "(untitled)")),
			result.URL,
		}
		if result.Snippet != "" {
			lines = append(lines, result.Snippet)
		}
		if result.Engine != "" {
			lines = append(lines, "engine: "+result.Engine)
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	return strings.Join(blocks, "\n\n")
}

func valueOr(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// assertPublicURL delegates to harvest's own SSRF/scheme chokepoint
// (AssertFetchable) instead of keeping a second, drifting copy of the
// private/internal check here — harvest.assertFetchable is the one
// authority (its suffix list, e.g. .ts.net, is not duplicated in this
// package).
func assertPublicURL(parsed *url.URL) error {
	return harvest.AssertFetchable(parsed.String())
}
