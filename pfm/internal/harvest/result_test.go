package harvest

import (
	"path/filepath"
	"testing"
)

func TestLegacyRungReceiptsGroupOnlyConsecutiveOACandidates(t *testing.T) {
	t.Parallel()
	rungs := []string{"direct", "oa:unpaywall", "oa:openalex", "oa:core", "wayback"}
	if got := rungsPhrase(rungs); got != "direct, oa-mirror(3 sources), wayback" {
		t.Fatalf("rungsPhrase()=%q", got)
	}
	if got := withRungs("boom", []string{"direct"}); got != "boom" {
		t.Fatalf("single-rung receipt=%q", got)
	}
	want := "boom Rungs tried: direct, wayback."
	if got := withRungs("boom", []string{"direct", "wayback"}); got != want {
		t.Fatalf("multi-rung receipt=%q, want %q", got, want)
	}
}

// TestStoredArtifactLiftsALegacyBlockOnlyFromALegacyEntry: a metadata block
// opening the body is lifted only from an entry an older harvester wrote (its
// frontmatter has no chars, which every current write carries); in a current
// entry it is the page's own text and the converter's title stands.
func TestStoredArtifactLiftsALegacyBlockOnlyFromALegacyEntry(t *testing.T) {
	t.Parallel()
	block := "**Title:** Draft\n**Source:** internal\n\n---\n\nThe body.\n"
	for _, tc := range []struct {
		name, raw, title, gaps, body string
	}{
		{
			name: "current entry",
			raw: renderFrontmatter(map[string]string{
				keySource: frontmatterSourceHarvester, "chars": "60", keyTitle: "Converter title",
			}) + block,
			title: "Converter title",
			body:  block,
		},
		{
			name: "legacy entry",
			raw: "---\nfetched_at: 2024-01-02T03:04:05Z\ntoken_count: 9\nsource: harvester\n---\n\n" +
				partialMarkerPrefix + "login wall\n\n" + block,
			title: "Draft",
			gaps:  "login wall",
			body:  "The body.\n",
		},
	} {
		meta, gaps, body := storedArtifact(tc.raw)
		if meta[keyTitle] != tc.title || gaps != tc.gaps || body != tc.body {
			t.Errorf("%s: title %q gaps %q body %q, want title %q gaps %q body %q",
				tc.name, meta[keyTitle], gaps, body, tc.title, tc.gaps, tc.body)
		}
	}
}

// TestStoredContentKeepsGapsWhenTheArtifactDoesNotRead: a re-store whose
// artifact cannot be read falls back to the inline content, which carries its
// gaps only in Partial; the content the ladder re-stores keeps them.
func TestStoredContentKeepsGapsWhenTheArtifactDoesNotRead(t *testing.T) {
	t.Parallel()
	result := Result{
		Path:    filepath.Join(t.TempDir(), "absent.md"),
		Content: "The body.\n",
		Partial: "3 of 9 comments loaded",
	}
	content := storedContent(result)
	if got := partialReason(content); got != result.Partial {
		t.Fatalf("re-stored content %q carries gaps %q, want %q", content, got, result.Partial)
	}
	if got := partialBody(content); got != "\nThe body.\n" {
		t.Fatalf("re-stored body %q, want the inline content", got)
	}
}
