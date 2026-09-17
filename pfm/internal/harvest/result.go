package harvest

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func (h *Harvester) storeResult(
	source, kind, method, content string,
	bytes int64,
	statusCode int,
	rungs []string,
	options FetchOptions,
) Result {
	path, err := h.cache.save(source, kind, method, content, rungs)
	if err != nil {
		return Result{Source: source, Kind: kind, Error: err.Error(), Rungs: rungs}
	}
	status := cacheStatusMiss
	if options.Refresh {
		status = cacheStatusRefresh
	}
	// The public receipt reports the on-disk artifact size, including its
	// provenance frontmatter, just like the Python cache result. Fall back to
	// source bytes only if a filesystem stat races with cleanup.
	if info, statErr := os.Stat(path); statErr == nil {
		bytes = info.Size()
	}
	chars := contentChars(content)
	return Result{
		Source:       source,
		Kind:         kind,
		Content:      truncateInline(content, h.options.MaxInlineChars),
		Path:         path,
		Method:       method,
		CacheStatus:  status,
		Bytes:        bytes,
		Chars:        chars,
		ContentChars: chars,
		Tokens:       EstimateTokens(content),
		HTTPStatus:   statusCode,
		Rungs:        rungs,
	}
}

func (h *Harvester) storeResultAlias(
	source, canonicalSource string,
	result Result,
	rungs []string,
	options FetchOptions,
) Result {
	content := result.Content
	if result.Path != "" {
		if raw, err := os.ReadFile(result.Path); err == nil {
			_, content = parseCacheFrontmatter(string(raw))
		}
	}
	stored := h.storeResult(
		canonicalSource,
		result.Kind,
		result.Method,
		content,
		result.Bytes,
		result.HTTPStatus,
		rungs,
		options,
	)
	stored.Source = source
	return stored
}

func (h *Harvester) resultFromCache(source, kind, content string, meta map[string]string, path string) Result {
	rungs := []string{}
	if raw := meta["rungs"]; raw != "" {
		for _, rung := range strings.Split(raw, ",") {
			if s := strings.TrimSpace(rung); s != "" {
				rungs = append(rungs, s)
			}
		}
	}
	bytes := int64(0)
	if info, statErr := os.Stat(path); statErr == nil {
		bytes = info.Size()
	} else {
		bytes = int64(len(content))
	}
	tokens := EstimateTokens(content)
	if stored, parseErr := strconv.Atoi(meta["token_count"]); parseErr == nil && stored >= 0 {
		tokens = stored
	}
	chars := contentChars(content)
	return Result{
		Source:       source,
		Kind:         kind,
		Content:      truncateInline(content, h.options.MaxInlineChars),
		Path:         path,
		Method:       meta["method"],
		CacheStatus:  cacheStatusHit,
		Bytes:        bytes,
		Chars:        chars,
		ContentChars: chars,
		Tokens:       tokens,
		Rungs:        rungs,
	}
}

func rungsSummary(rungs []string) string { return strings.Join(rungs, ", ") }

func rungsPhrase(rungs []string) string {
	parts := make([]string, 0, len(rungs))
	for index := 0; index < len(rungs); {
		if !strings.HasPrefix(rungs[index], "oa:") {
			parts = append(parts, rungs[index])
			index++
			continue
		}
		end := index
		for end < len(rungs) && strings.HasPrefix(rungs[end], "oa:") {
			end++
		}
		count := end - index
		label := "source"
		if count != 1 {
			label = "sources"
		}
		parts = append(parts, fmt.Sprintf("oa-mirror(%d %s)", count, label))
		index = end
	}
	return strings.Join(parts, ", ")
}

func withRungs(message string, rungs []string) string {
	if len(rungs) <= 1 {
		return message
	}
	return message + " Rungs tried: " + rungsPhrase(rungs) + " — re-fetching will not help."
}
