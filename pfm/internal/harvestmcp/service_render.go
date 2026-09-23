package harvestmcp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

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
					"Use `webSearch` to find an alternative copy, or `findWorks` if it is a scholarly title.",
					"Use `findWorks` if it is a scholarly title, or fetch an alternative copy at another URL.",
				),
			)
		}
		receipt := map[string]any{
			jsonPropertySource: source,
			"tokens":           result.Tokens,
			"token_count":      result.Tokens,
			"chars":            result.Chars,
			"path":             result.Path,
			"cache_status":     result.CacheStatus,
		}
		if result.Partial != "" {
			// A caller budgeting a read learns the artifact is incomplete
			// before it reads it.
			receipt["partial"] = result.Partial
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
					"Use `webSearch` to find an alternative copy, or `findWorks` if it is a scholarly title.",
					"Use `findWorks` if it is a scholarly title, or fetch an alternative copy at another URL.",
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
		"# %s\ncache_status: %s / bytes: %d / tokens: %d / fetched_at: %s / path: %s",
		source,
		result.CacheStatus,
		result.Bytes,
		tokens,
		fetchedAt,
		result.Path,
	)
	if result.Partial != "" {
		// A known-incomplete artifact says so in the receipt, not only in
		// the marker line the content opens with.
		header += " / PARTIAL: " + result.Partial
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
			"No candidate works found for %q. Try a plain WebSearch, or rephrase — a more exact title helps.",
			query,
		)
	}
	lines := []string{
		fmt.Sprintf(
			"%d candidate work(s) for %q — pick one and read it with `readWork`, passing its `handle:` value:",
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
		fmt.Sprintf("%d result(s) for %q — read the ones you want with `readPage`:", len(results), query),
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
