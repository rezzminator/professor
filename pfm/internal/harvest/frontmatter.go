package harvest

// An artifact's frontmatter is everything the harvester knows ABOUT a stored
// document — where it came from, what the converter read about it, what it is
// missing — and its body is the document alone. One writer (renderFrontmatter)
// serves the private cache and the public store; one parser
// (readFrontmatter) serves every reader.

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The frontmatter keys more than one writer or reader names.
const (
	keySource    = "source"
	keyRequest   = "request"
	keyKind      = "kind"
	keyAuthor    = "author"
	keySite      = "site"
	keyLicense   = "license"
	keyPublished = "published"
	keyTitle     = "title"
)

// frontmatterKeys is the order the writer emits keys in; a key outside it
// follows, sorted.
var frontmatterKeys = []string{
	keySource, keyRequest, "field", "url", keyKind, "method", "via", "rungs", "http_status", "fetched_at",
	keyTitle, keyAuthor, keyPublished, keySite, keyLicense, "chars", "token_count", "gaps", "transformed",
}

// converterMetaKeys are the facts a converter reads about a document
// (harvestpy's response "meta"): lifted off the body into frontmatter.
var converterMetaKeys = []string{keyTitle, keyAuthor, keyPublished, keySite, keyLicense, "transformed"}

// frontmatterEscaper keeps frontmatter line-oriented: no value — a URL, a
// page's own title or author — can start a second key.
var frontmatterEscaper = strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A")

var frontmatterUnescaper = strings.NewReplacer("%0D", "\r", "%0A", "\n", "%25", "%")

// renderFrontmatter is fields as an artifact's frontmatter block, the blank
// line after it included; an empty value writes no line.
func renderFrontmatter(fields map[string]string) string {
	var out strings.Builder
	out.WriteString("---\n")
	write := func(key string) {
		if value := fields[key]; value != "" {
			out.WriteString(key + ": " + frontmatterEscaper.Replace(value) + "\n")
		}
	}
	for _, key := range frontmatterKeys {
		write(key)
	}
	extra := make([]string, 0, len(fields))
	for key := range fields {
		if !slices.Contains(frontmatterKeys, key) {
			extra = append(extra, key)
		}
	}
	slices.Sort(extra)
	for _, key := range extra {
		write(key)
	}
	out.WriteString("---\n\n")
	return out.String()
}

// readFrontmatter splits raw into its frontmatter values and its body. Text
// without a closed frontmatter block is all body.
func readFrontmatter(raw string) (map[string]string, string) {
	meta := map[string]string{}
	if !strings.HasPrefix(raw, "---\n") {
		return meta, raw
	}
	end := strings.Index(raw[4:], "\n---\n")
	if end < 0 {
		return meta, raw
	}
	head := raw[4 : 4+end]
	for _, line := range strings.Split(head, "\n") {
		key, value, found := strings.Cut(line, ":")
		if found {
			meta[strings.TrimSpace(key)] = frontmatterUnescaper.Replace(strings.TrimSpace(value))
		}
	}
	body := raw[4+end+len("\n---\n"):]
	return meta, strings.TrimPrefix(body, "\n")
}

// converterMetaPrefix opens the line carrying a converter's metadata ahead of
// its text (WithConverterMeta).
const converterMetaPrefix = "<!-- harvester-meta "

// converterMetaToken is this process's metadata-line token: splitArtifact reads
// back only a line carrying it, so page text cannot author metadata.
var converterMetaToken = rand.Text()

// WithConverterMeta is markdown with the converter's metadata on one line
// ahead of it, for the store to lift into frontmatter (splitArtifact). No
// metadata, or no text, returns markdown as it is. markdown passes pageText:
// under the metadata line no later door sees the page's own partial marker at
// the top, and splitArtifact would read it as the harvester's.
func WithConverterMeta(markdown string, meta map[string]string) string {
	if len(meta) == 0 || strings.TrimSpace(markdown) == "" {
		return markdown
	}
	data, err := json.Marshal(meta)
	if err != nil {
		obs.Logger(context.Background()).Warn("harvest: converter metadata did not encode; the text stands without it",
			obs.FieldErr, err.Error())
		return markdown
	}
	return converterMetaPrefix + converterMetaToken + " " + string(data) + " -->\n\n" + pageText(markdown)
}

// legacyMetaLabels are the converter metadata lines an older conversion wrote
// into the body, ahead of a "---" separator.
var legacyMetaLabels = map[string]string{
	"**Title:**": keyTitle, "**Authors:**": keyAuthor, "**Published:**": keyPublished,
	"**Source:**": keySite, "**License:**": keyLicense,
}

// splitArtifact takes off the top of content what the body must not carry:
// the partial marker (its reason is gaps), the converter's metadata line, and,
// from a legacy entry (legacyEntry), the metadata block an older conversion
// wrote; a moved-page note stays body. In any other content such a block is
// the page's own text.
func splitArtifact(content string, legacy bool) (map[string]string, string, string) {
	meta := map[string]string{}
	var gaps string
	var notes []string
	rest := content
	for {
		trimmed := strings.TrimLeft(rest, "\r\n")
		line, after, _ := strings.Cut(trimmed, "\n")
		switch {
		case strings.HasPrefix(line, partialMarkerPrefix):
			gaps = joinReasons(gaps, strings.TrimPrefix(line, partialMarkerPrefix))
		case strings.HasPrefix(line, converterMetaPrefix+converterMetaToken+" ") && strings.HasSuffix(line, " -->"):
			readConverterMeta(line, meta)
		case strings.HasPrefix(line, movedNotePrefix):
			notes = append(notes, line)
		default:
			if legacy {
				rest = liftLegacyMeta(rest, meta)
			}
			body := strings.TrimLeft(rest, "\r\n")
			if len(notes) > 0 {
				body = strings.Join(append(notes, body), "\n\n")
			}
			return meta, gaps, body
		}
		rest = after
	}
}

func readConverterMeta(line string, meta map[string]string) {
	data := strings.TrimSuffix(strings.TrimPrefix(line, converterMetaPrefix+converterMetaToken+" "), " -->")
	read := map[string]string{}
	if err := json.Unmarshal([]byte(data), &read); err != nil {
		obs.Logger(context.Background()).Warn("harvest: a converter metadata line did not decode; it is dropped",
			obs.FieldErr, err.Error())
		return
	}
	for key, value := range read {
		if slices.Contains(converterMetaKeys, key) && strings.TrimSpace(value) != "" {
			meta[key] = value
		}
	}
}

// legacyEntry reports whether an artifact's frontmatter is an older
// harvester's: every current write records chars, no older one did.
func legacyEntry(meta map[string]string) bool {
	return meta["chars"] == ""
}

// liftLegacyMeta moves a legacy metadata block (only labelled lines and blank
// lines, then a "---" line) from the top of content into meta.
func liftLegacyMeta(content string, meta map[string]string) string {
	lines := strings.Split(content, "\n")
	found := map[string]string{}
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if trimmed == "---" {
			if len(found) == 0 {
				return content
			}
			for key, value := range found {
				meta[key] = value
			}
			return strings.Join(lines[index+1:], "\n")
		}
		label, value, ok := strings.Cut(trimmed, " ")
		key, known := legacyMetaLabels[label]
		if !ok || !known {
			return content
		}
		found[key] = strings.TrimSpace(value)
	}
	return content
}
