package harvestmcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

// sizeReceipt is the include_content:false text receipt: the read item's own
// field names, without its content.
type sizeReceipt struct {
	Source string   `json:"source"`
	Kind   string   `json:"kind,omitempty"`
	Via    string   `json:"via,omitempty"`
	Cached bool     `json:"cached"`
	Chars  int      `json:"chars"`
	Tokens int      `json:"tokens"`
	Path   string   `json:"path"`
	Gaps   []string `json:"gaps,omitempty"`
}

func (service *Service) describeFetch(source string, result harvest.Result, sizeOnly bool) string {
	source = harvest.PublicSourceLabel(source)
	if result.Error != "" {
		return "# " + source + "\nERROR: " + harvest.PublicFailureMessage(result)
	}
	// Size probes never kill an empty extraction behind a zero-sized success;
	// this wording is part of the Python scheduler contract.
	if sizeOnly {
		if strings.TrimSpace(result.Content) == "" && result.Chars == 0 && result.Bytes == 0 {
			return fmt.Sprintf(
				"# %s\nERROR: Fetched %s but it yielded no readable content (empty after extraction) — nothing to size. %s",
				source,
				source,
				harvest.SearchHint(runtimeSearchEnabled(service.runtime),
					"Use `search_web` to find an alternative copy, or `search_literature` if it is a scholarly title.",
					"Use `search_literature` if it is a scholarly title, or read an alternative copy at another URL.",
				),
			)
		}
		// The receipt names what the typed read item names. A caller
		// budgeting a read learns from gaps that the artifact is incomplete
		// before it reads it.
		receipt := sizeReceipt{
			Source: source,
			Kind:   result.Kind,
			Via:    harvest.PublicMethod(result.Method),
			Cached: result.CacheStatus == cacheStatusHit,
			Chars:  result.Chars,
			Tokens: result.Tokens,
			Path:   result.Path,
		}
		if result.Partial != "" {
			receipt.Gaps = harvest.PublicGaps(result.Partial)
		}
		body, err := json.Marshal(receipt)
		if err != nil {
			return fmt.Sprintf("# %s\nERROR: encode size receipt: %v", source, err)
		}
		return string(body)
	}
	body := result.Content
	stripped := strings.TrimSpace(body)
	meta := frontmatter(result.Path)
	status := result.HTTPStatus
	if stripped == "" {
		message := ""
		if result.ErrorKind != "" || result.Challenge || status >= 400 {
			message = harvest.PublicFailureMessage(result)
		}
		if message == "" {
			message = fmt.Sprintf(
				"Fetched %s but no readable content could be extracted (JS-rendered or bot-blocked — not retrievable from this datacenter IP). %s",
				source,
				harvest.SearchHint(runtimeSearchEnabled(service.runtime),
					"Use `search_web` to find an alternative copy, or `search_literature` if it is a scholarly title.",
					"Use `search_literature` if it is a scholarly title, or read an alternative copy at another URL.",
				),
			)
		}
		return "# " + source + "\nERROR: " + message
	}
	// A thin HTTP error/challenge page is a failure, not a successful body.
	lowerBody := strings.ToLower(stripped)
	challenge := strings.Contains(lowerBody, "cloudflare") || strings.Contains(lowerBody, "captcha") ||
		strings.Contains(lowerBody, "verify you are human")
	if len([]rune(stripped)) < 500 && (status >= 400 || challenge) {
		return "# " + source + "\nERROR: " + harvest.PublicFailureMessage(
			harvest.Result{HTTPStatus: status, ErrorKind: result.ErrorKind, Challenge: challenge},
		)
	}
	fetchedAt := "unknown"
	if value := meta["fetched_at"]; value != "" {
		fetchedAt = value
	}
	tokens := result.Tokens
	if value, err := strconv.Atoi(meta["token_count"]); err == nil {
		tokens = value
	}
	header := fmt.Sprintf(
		"# %s\ncached: %t / bytes: %d / tokens: %d / fetched_at: %s / path: %s",
		source,
		result.CacheStatus == cacheStatusHit,
		result.Bytes,
		tokens,
		fetchedAt,
		result.Path,
	)
	if result.Partial != "" {
		// A known-incomplete artifact says so in the receipt, not only in
		// the marker line the content opens with.
		header += " / gaps: " + strings.Join(harvest.PublicGaps(result.Partial), "; ")
	}

	inlineCap := service.inlineCap()
	if inlineCap > 0 && len([]rune(body)) > inlineCap {
		runes := []rune(body)
		body = string(
			runes[:inlineCap],
		) + fmt.Sprintf(
			"\n\n— [truncated: first %d of %d chars. COMPLETE text is at %s — read that file from char %d for the rest.]",
			inlineCap,
			max(result.Chars, len(runes)),
			result.Path,
			inlineCap,
		)
	}
	return header + "\n\n" + body
}

// inlineCap is output.maxInlineChars from harvester.config.json; an
// unconfigured runtime keeps the default.
func (service *Service) inlineCap() int {
	if service != nil && service.runtime.MaxInlineChars > 0 {
		return service.runtime.MaxInlineChars
	}
	return defaultInlineChars
}

func frontmatter(path string) map[string]string {
	out := map[string]string{}
	if path == "" {
		return out
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	text := string(data)
	if !strings.HasPrefix(text, "---\n") {
		return out
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		return out
	}
	for _, line := range strings.Split(text[4:4+end], "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok {
			out[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return out
}

// renderFind lists the candidates and names every source that failed: with a
// failed source, no candidate is not proof the work is absent.
func renderFind(query string, candidates []harvest.Candidate, failed []harvest.WorkSource) string {
	failures := ""
	if len(failed) > 0 {
		failures = "\n\nThese sources failed, so their records are missing from this answer: " +
			harvest.FailedText(failed) + ". Retry later for their records."
	}
	if len(candidates) == 0 && len(failed) > 0 {
		return fmt.Sprintf("No candidate works found for %q among the sources that answered.", query) + failures
	}
	if len(candidates) == 0 {
		return fmt.Sprintf(
			"No candidate works found for %q. Try search_web (when configured) or a plain web search, or rephrase — a more exact title helps.",
			query,
		)
	}
	lines := []string{
		fmt.Sprintf(
			"%d candidate work(s) for %q — pick one and read it with `read`, passing its `handle:` value in publications:",
			len(candidates),
			query,
		),
		"",
	}
	for index, candidate := range candidates {
		values := []string{candidate.Kind}
		if candidate.Authors != "" {
			values = append(values, candidate.Authors)
		}
		if candidate.Year != 0 {
			values = append(values, strconv.Itoa(candidate.Year))
		}
		if candidate.Free != "" {
			values = append(values, candidate.Free)
		}
		values = append(values, fmt.Sprintf("match %v", candidate.Match))
		meta := strings.Trim(strings.Join(values, " · "), " ·")
		lines = append(
			lines,
			fmt.Sprintf("%d. %s", index+1, valueOr(candidate.Title, "(untitled)")),
			"   handle: "+candidate.URL,
			"   "+meta,
		)
	}
	return strings.Join(lines, "\n") + failures
}

func renderSearch(query string, results []harvest.SearchResult, _ string) string {
	if len(results) == 0 {
		return fmt.Sprintf("No results for %q. Try different terms or a broader query.", query)
	}
	lines := []string{
		fmt.Sprintf("%d result(s) for %q — read the ones you want with `read`, in urls:", len(results), query),
		"",
	}
	for index, result := range results {
		lines = append(lines, fmt.Sprintf("%d. %s", index+1, valueOr(result.Title, "(untitled)")), "   "+result.URL)
		if result.Snippet != "" {
			lines = append(lines, "   "+result.Snippet)
		}
	}
	return strings.Join(lines, "\n")
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

// readFields is the order read keeps its groups in: its input's fields, its
// structured output's arrays and its text Content's headings.
var readFields = []string{fieldURLs, fieldFiles, fieldPublications}

// renderReadGroups groups a read call's items by the field each came in, input
// order kept: the text Content opens each non-empty group with a heading
// ("## urls (4)") before its items, and the structured output carries one
// array per group, an empty one omitted. Every item failed: the call is an
// error, so a failed batch never reads as a result; one item that read keeps
// the batch a result.
func renderReadGroups(jobs []readJob, contents []string, items []ReadItem) (*mcp.CallToolResult, ReadOutput) {
	result := &mcp.CallToolResult{IsError: true}
	var output ReadOutput
	for _, field := range readFields {
		var group []ReadItem
		var texts []mcp.Content
		for index, job := range jobs {
			if job.field != field {
				continue
			}
			group = append(group, items[index])
			texts = append(texts, &mcp.TextContent{Text: contents[index]})
			if items[index].Error == "" {
				result.IsError = false
			}
		}
		if len(group) == 0 {
			continue
		}
		result.Content = append(result.Content, &mcp.TextContent{Text: fmt.Sprintf("## %s (%d)", field, len(group))})
		result.Content = append(result.Content, texts...)
		switch field {
		case fieldURLs:
			output.URLs = group
		case fieldFiles:
			output.Files = group
		case fieldPublications:
			output.Publications = group
		}
	}
	return result, output
}
