package harvest

import (
	"testing"
)

func TestLegacyRungReceiptsGroupOnlyConsecutiveOACandidates(t *testing.T) {
	rungs := []string{"direct", "oa:unpaywall", "oa:openalex", "oa:core", "wayback"}
	if got := rungsPhrase(rungs); got != "direct, oa-mirror(3 sources), wayback" {
		t.Fatalf("rungsPhrase()=%q", got)
	}
	if got := rungsSummary([]string{"direct", "oa:unpaywall"}); got != "direct, oa:unpaywall" {
		t.Fatalf("rungsSummary()=%q", got)
	}
	if got := withRungs("boom", []string{"direct"}); got != "boom" {
		t.Fatalf("single-rung receipt=%q", got)
	}
	want := "boom Rungs tried: direct, wayback."
	if got := withRungs("boom", []string{"direct", "wayback"}); got != want {
		t.Fatalf("multi-rung receipt=%q, want %q", got, want)
	}
}
