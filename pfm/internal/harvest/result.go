package harvest

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

func (h *Harvester) storeResult(
	source, kind, method, content string,
	bytes int64,
	statusCode int,
	rungs []string,
	options FetchOptions,
) Result {
	// The marker, the converter's metadata line and any legacy metadata block
	// leave the body here: they are stored as frontmatter, the body alone as text.
	facts, gaps, body := splitArtifact(content, false)
	facts["gaps"] = gaps
	path, err := h.cache.save(source, kind, method, body, statusCode, rungs, facts)
	if err != nil {
		return Result{Source: source, Kind: kind, Error: err.Error(), Rungs: rungs}
	}
	h.afterCacheWrite()
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
	chars := contentChars(body)
	return Result{
		Source:       source,
		Kind:         kind,
		Content:      truncateInline(body, h.options.MaxInlineChars),
		Path:         path,
		Method:       method,
		CacheStatus:  status,
		Bytes:        bytes,
		Chars:        chars,
		ContentChars: chars,
		Tokens:       EstimateTokens(body),
		HTTPStatus:   statusCode,
		Rungs:        rungs,
		Partial:      gaps,
	}
}

func (h *Harvester) storeResultAlias(
	source, canonicalSource string,
	result Result,
	rungs []string,
	options FetchOptions,
) Result {
	stored := h.storeResult(
		canonicalSource,
		result.Kind,
		result.Method,
		storedContent(result),
		result.Bytes,
		result.HTTPStatus,
		rungs,
		options,
	)
	stored.Source = source
	return stored
}

// storedContent is result's whole content as the pipeline carries it — its
// stored body with its gaps as the partial marker and its converter metadata
// as the metadata line (storedArtifact), so a re-store keeps both — or the
// inline content (which may be truncated, truncateInline) when the artifact
// could not be read.
func storedContent(result Result) string {
	if result.Path == "" {
		return inlineContent(result)
	}
	raw, err := os.ReadFile(result.Path)
	if err != nil {
		obs.Logger(context.Background()).
			Warn("harvest: the stored artifact could not be read; its inline content stands in",
				"path", result.Path, obs.FieldErr, err.Error())
		return inlineContent(result)
	}
	return pipelineContent(storedArtifact(string(raw)))
}

// inlineContent is result's inline content as the ladder carries content: a
// stored result's gaps live in Partial alone, so they go back on as the
// partial marker; content already carrying the marker stands as it is.
func inlineContent(result Result) string {
	if partialReason(result.Content) != "" {
		return result.Content
	}
	return withPartial(result.Content, result.Partial)
}

// pipelineContent is a stored artifact as the ladder carries content: its gaps
// as the partial marker and its converter metadata as the metadata line.
func pipelineContent(meta map[string]string, gaps, body string) string {
	converted := map[string]string{}
	for _, key := range converterMetaKeys {
		if meta[key] != "" {
			converted[key] = meta[key]
		}
	}
	return withPartial(WithConverterMeta(body, converted), gaps)
}

// storedArtifact reads a harvester artifact file: its frontmatter, its gaps and
// its body. A legacy entry's banner and metadata block, still in its body, are
// lifted the same way a fresh conversion's are.
func storedArtifact(raw string) (map[string]string, string, string) {
	meta, content := readFrontmatter(raw)
	lifted, legacyGaps, body := splitArtifact(content, legacyEntry(meta))
	for key, value := range lifted {
		if meta[key] == "" {
			meta[key] = value
		}
	}
	return meta, joinReasons(meta["gaps"], legacyGaps), body
}

func (h *Harvester) resultFromCache(source, kind, content string, meta map[string]string, path string) Result {
	// A legacy entry still carries its banner and metadata block in its body.
	_, legacyGaps, content := splitArtifact(content, legacyEntry(meta))
	gaps := joinReasons(meta["gaps"], legacyGaps)
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
	// The delivering rung's status travels with the entry; an entry stored
	// without one (a local document, an older cache) reports none, never 200.
	status := 0
	if stored, parseErr := strconv.Atoi(meta["http_status"]); parseErr == nil && stored >= 100 && stored < 600 {
		status = stored
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
		HTTPStatus:   status,
		Rungs:        rungs,
		Partial:      gaps,
	}
}

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
	return message + " Rungs tried: " + rungsPhrase(rungs) + "."
}
