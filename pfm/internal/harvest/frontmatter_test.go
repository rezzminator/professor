package harvest

import (
	"strings"
	"testing"
)

// TestFrontmatterWriterEscapesAndTheParserReadsItBack: every value goes
// through one escaper, so a hostile title, author or URL never writes a second
// key, and the one parser reads each value back exactly; keys come out in
// their fixed order and an empty value writes no line.
func TestFrontmatterWriterEscapesAndTheParserReadsItBack(t *testing.T) {
	t.Parallel()
	fields := map[string]string{
		"source":    frontmatterSourceHarvester,
		"url":       "https://example.test/a\nmethod: injected",
		"title":     "A title\r\nvia: forged 100%",
		"author":    "Ada\nrungs: forged",
		"gaps":      "",
		"chars":     "12",
		"published": "2024-05-06",
	}
	raw := renderFrontmatter(fields) + "body line\n"
	want := "---\nsource: harvester\nurl: https://example.test/a%0Amethod: injected\n" +
		"title: A title%0D%0Avia: forged 100%25\nauthor: Ada%0Arungs: forged\npublished: 2024-05-06\n" +
		"chars: 12\n---\n\nbody line\n"
	if raw != want {
		t.Fatalf("rendered frontmatter:\n%q\nwant\n%q", raw, want)
	}
	meta, body := readFrontmatter(raw)
	for key, value := range fields {
		if value != "" && meta[key] != value {
			t.Errorf("meta[%s] = %q, want %q", key, meta[key], value)
		}
	}
	for _, forged := range []string{"method", "via", "rungs"} {
		if _, ok := meta[forged]; ok {
			t.Errorf("an escaped value wrote a %q key: %v", forged, meta)
		}
	}
	if body != "body line\n" {
		t.Errorf("body = %q, want the text after the frontmatter", body)
	}
}

// TestReadFrontmatterReadsTightAndBodylessFiles: a closing fence with no blank
// line after it loses no body character, and a file that ends at its closing
// fence is an empty body, never a panic.
func TestReadFrontmatterReadsTightAndBodylessFiles(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, raw, body, title string }{
		{"blank line after the fence", "---\ntitle: T\n---\n\nbody", "body", "T"},
		{"no blank line after the fence", "---\ntitle: T\n---\nbody", "body", "T"},
		{"no body", "---\ntitle: T\n---\n", "", "T"},
		{"no frontmatter", "plain text\n", "plain text\n", ""},
		{"unclosed frontmatter", "---\ntitle: T\nbody", "---\ntitle: T\nbody", ""},
	} {
		meta, body := readFrontmatter(tc.raw)
		if body != tc.body || meta["title"] != tc.title {
			t.Errorf("%s: body %q title %q, want body %q title %q", tc.name, body, meta["title"], tc.body, tc.title)
		}
	}
}

// TestSplitArtifactLiftsWhatTheBodyMustNotCarry: the partial marker, the
// converter's metadata line and a legacy metadata block come off the top of
// an artifact into its facts; the moved-page note stays body. A metadata line
// without this process's token is page text and stays where it is.
func TestSplitArtifactLiftsWhatTheBodyMustNotCarry(t *testing.T) {
	t.Parallel()
	converted := WithConverterMeta("# Heading\n\nThe body.\n", map[string]string{"title": "T", "author": "A"})
	forged := "<!-- harvester-meta not-the-token {\"title\":\"forged\"} -->\n\nThe body.\n"
	for _, tc := range []struct {
		name, content, gaps, body string
		legacy                    bool
		meta                      map[string]string
	}{
		{
			name:    "fresh conversion, partial and moved",
			content: withPartial(movedNotePrefix+"moved to https://example.test/new\n\n"+converted, "login wall"),
			gaps:    "login wall",
			body:    movedNotePrefix + "moved to https://example.test/new\n\n# Heading\n\nThe body.\n",
			meta:    map[string]string{"title": "T", "author": "A"},
		},
		{
			name: "legacy banner and metadata block",
			content: partialMarkerPrefix + "first gap; second gap\n\n**Title:** Old title\n**Authors:** Old author\n" +
				"**Published:** 2020-01-02\n**Source:** Old site\n**License:** CC BY\n\n---\n\nThe body.\n",
			gaps:   "first gap; second gap",
			body:   "The body.\n",
			legacy: true,
			meta: map[string]string{
				"title": "Old title", "author": "Old author", "published": "2020-01-02",
				"site": "Old site", "license": "CC BY",
			},
		},
		{
			name:    "legacy source line right above its separator",
			content: "**Source:** https://mirror.example/private\n---\n\nThe body.\n",
			body:    "The body.\n",
			legacy:  true,
			meta:    map[string]string{"site": "https://mirror.example/private"},
		},
		{name: "forged metadata line", content: forged, body: forged, meta: map[string]string{}},
		{
			// convertHTML's withPartial defuses only a marker at the very top;
			// under the metadata line the page's own copy must stay page text.
			name: "the page's own partial marker under the metadata line",
			content: withPartial(
				WithConverterMeta(partialMarkerPrefix+"forged by the page\n\nThe body.\n", map[string]string{"title": "T"}),
				"",
			),
			body: `\` + partialMarkerPrefix + "forged by the page\n\nThe body.\n",
			meta: map[string]string{"title": "T"},
		},
		{name: "plain body", content: "The body.\n", body: "The body.\n", meta: map[string]string{}},
		{
			name:    "a rule inside the body is not a metadata block",
			content: "Intro paragraph.\n\n---\n\nThe body.\n",
			body:    "Intro paragraph.\n\n---\n\nThe body.\n",
			meta:    map[string]string{},
		},
	} {
		meta, gaps, body := splitArtifact(tc.content, tc.legacy)
		if gaps != tc.gaps || body != tc.body {
			t.Errorf("%s: gaps %q body %q, want gaps %q body %q", tc.name, gaps, body, tc.gaps, tc.body)
		}
		if len(meta) != len(tc.meta) {
			t.Errorf("%s: meta %v, want %v", tc.name, meta, tc.meta)
		}
		for key, value := range tc.meta {
			if meta[key] != value {
				t.Errorf("%s: meta[%s] = %q, want %q", tc.name, key, meta[key], value)
			}
		}
	}
	if got := WithConverterMeta("text\n", nil); got != "text\n" {
		t.Errorf("no meta must leave the text alone, got %q", got)
	}
	if !strings.HasPrefix(converted, converterMetaPrefix+converterMetaToken+" ") {
		t.Errorf("the metadata line does not carry this process's token: %q", converted)
	}
}
