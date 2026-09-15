package artifact

import (
	"strings"
	"testing"
)

func TestParseAnchorRowCanonicalGrammar(t *testing.T) {
	row, err := ParseAnchorRow("- `internal/dream/artifact/map.go:10-20` — blob `0123456789ab`")
	if err != nil {
		t.Fatalf("ParseAnchorRow() error = %v", err)
	}
	if row.DisplayPath != "internal/dream/artifact/map.go:10-20" ||
		row.LookupPath != "internal/dream/artifact/map.go" ||
		row.ObjectType != GitBlob ||
		row.Hash != "0123456789ab" {
		t.Fatalf("ParseAnchorRow() = %#v", row)
	}
	if got := RenderAnchorRow(row); got != "- `internal/dream/artifact/map.go:10-20` — blob `0123456789ab`" {
		t.Fatalf("RenderAnchorRow() = %q", got)
	}
}

func TestParseAnchorRowRejectsRetiredAndUnsafeForms(t *testing.T) {
	tests := []struct {
		name string
		row  string
		want string
	}{
		{
			"40 character hash",
			"- `map.go` — blob `0123456789abcdef0123456789abcdef01234567`",
			"anchor row grammar mismatch",
		},
		{
			"commit row",
			"- `map.go` — `git log -1`: `0123456789ab` (2026-08-13); blob `0123456789ab`",
			"anchor row grammar mismatch",
		},
		{"uppercase hash", "- `map.go` — blob `0123456789AB`", "anchor row grammar mismatch"},
		{"absolute path", "- `/etc/passwd` — blob `0123456789ab`", "unsafe anchor path: /etc/passwd"},
		{"parent traversal", "- `a/../secret` — blob `0123456789ab`", "unsafe anchor path: a/../secret"},
		{"git internals", "- `.git/config` — blob `0123456789ab`", "unsafe anchor path: .git/config"},
		{"stacked range", "- `map.go:10:12` — blob `0123456789ab`", "anchor row grammar mismatch"},
		{"multiple comma ranges", "- `map.go:1-2,4-5` — blob `0123456789ab`", "anchor row grammar mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseAnchorRow(test.row)
			assertErrorContains(t, err, test.want)
		})
	}
}

func TestParseMapReturnsCanonicalStructure(t *testing.T) {
	parsed, err := ParseMap(canonicalMap)
	if err != nil {
		t.Fatalf("ParseMap() error = %v", err)
	}
	if parsed.Title != "Artifact boundary" || parsed.Question != "What does the parser guarantee?" ||
		parsed.Answer != "It validates syntax once." || parsed.DerivationTrail != "The battery exposed parser drift." {
		t.Fatalf("ParseMap() sections = %#v", parsed)
	}
	if parsed.Provenance != (Provenance{Date: "2026-08-13", SessionID: "0123abcd"}) {
		t.Fatalf("ParseMap() provenance = %#v", parsed.Provenance)
	}
	if len(parsed.Anchors) != 2 {
		t.Fatalf("ParseMap() anchors = %d, want 2", len(parsed.Anchors))
	}
}

func TestParseMapEnforcesCanonicalShape(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{
			"legacy title",
			strings.Replace(canonicalMap, "# Artifact boundary", "# MAP: Artifact boundary", 1),
			"legacy title prefix",
		},
		{
			"unexpected heading",
			strings.Replace(canonicalMap, "## Answer", "## Notes\n\nnotes\n\n## Answer", 1),
			"unexpected section heading",
		},
		{
			"empty question",
			strings.Replace(canonicalMap, "What does the parser guarantee?", "", 1),
			"Question, Answer, or Derivation trail is empty",
		},
		{"bad provenance", strings.Replace(canonicalMap, "sid 0123abcd", "sid ABCD", 1), "Provenance grammar mismatch"},
		{
			"one anchor",
			strings.Replace(canonicalMap, "\n- `internal/dream/artifact/types.go` — blob `fedcba987654`", "", 1),
			"anchor count outside 2-8: 1",
		},
		{
			"nine anchors",
			canonicalMap + strings.Repeat("- `extra.go` — blob `0123456789ab`\n", 7),
			"anchor count outside 2-8: 9",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseMap(test.text)
			assertErrorContains(t, err, test.want)
		})
	}
}

const canonicalMap = "# Artifact boundary\n" +
	"\n" +
	"## Question\n" +
	"\n" +
	"What does the parser guarantee?\n" +
	"\n" +
	"## Answer\n" +
	"\n" +
	"It validates syntax once.\n" +
	"\n" +
	"## Derivation trail\n" +
	"\n" +
	"The battery exposed parser drift.\n" +
	"\n" +
	"Provenance: 2026-08-13 · sid 0123abcd\n" +
	"\n" +
	"## Anchors\n" +
	"\n" +
	"- `internal/dream/artifact/map.go:10-20` — blob `0123456789ab`\n" +
	"- `internal/dream/artifact/types.go` — blob `fedcba987654`\n"
