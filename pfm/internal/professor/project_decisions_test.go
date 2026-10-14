package professor

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// TestRetiredRuleCoversByPathAndName: a rule covers a file, or everything
// under a directory, never a sibling sharing a name prefix; a named rule
// covers only that retired name.
func TestRetiredRuleCoversByPathAndName(t *testing.T) {
	for _, row := range []struct {
		rule       RetiredRule
		path, name string
		want       bool
	}{
		{RetiredRule{Path: "docs/dev/backlog"}, "docs/dev/backlog/a.md", "/km", true},
		{RetiredRule{Path: "docs/dev/backlog"}, "docs/dev/backlog", "/km", true},
		{RetiredRule{Path: "docs/dev/backlog"}, "docs/dev/backlog-old/a.md", "/km", false},
		{RetiredRule{Path: "docs/a.md", Name: "/km"}, "docs/a.md", "/km", true},
		{RetiredRule{Path: "docs/a.md", Name: "/km"}, "docs/a.md", "/jc", false},
		{RetiredRule{Path: "docs"}, "src/docs/a.md", "/km", false},
	} {
		if got := row.rule.Covers(row.path, row.name); got != row.want {
			t.Errorf("%+v.Covers(%q, %q) = %v, want %v", row.rule, row.path, row.name, got, row.want)
		}
	}
}

// TestKeepPinRecordsTheReasonAndKeepsTheEarliestUntakenCommit: a keep
// acknowledges the current template and remembers where the untaken change
// starts; a second keep before any port leaves that start alone.
func TestKeepPinRecordsTheReasonAndKeepsTheEarliestUntakenCommit(t *testing.T) {
	first := keepPin(FilePin{Template: "project/a.md", TemplateHash: "h0", PinnedSHA: "aaa1111", PinnedAt: "d0"},
		"h1", "bbb2222", "own tool", "2026-10-12")
	want := FilePin{
		Template: "project/a.md", TemplateHash: "h1", PinnedSHA: "bbb2222", PinnedAt: "2026-10-12",
		Kept: &KeptNote{Reason: "own tool", FromSHA: "aaa1111", At: "2026-10-12"},
	}
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("first keep = %+v, want %+v", first, want)
	}
	second := keepPin(first, "h2", "ccc3333", "still own tool", "2026-10-13")
	if second.Kept.FromSHA != "aaa1111" || second.Kept.Reason != "still own tool" || second.PinnedSHA != "ccc3333" {
		t.Fatalf("second keep = %+v, want the earliest FromSHA aaa1111 and the new reason and pin", second)
	}
}

// TestPartitionRetiredMovesCoveredHitsOutOfReview: hits a recorded rule covers
// leave the review list and are counted per rule — a narrower rule inside a
// wider one counts the hits it covers too, never zero — each accepted hit
// once in the total; a rule that covers nothing is listed with zero.
func TestPartitionRetiredMovesCoveredHitsOutOfReview(t *testing.T) {
	scan := RetiredNameScan{Hits: []RetiredNameHit{
		{Path: "docs/dev/backlog/a.md", Line: 1, Name: "/wave"},
		{Path: "docs/dev/backlog/b.md", Line: 2, Name: "/km"},
		{Path: "src/x.go", Line: 3, Name: "/wave"},
	}}
	rules := []RetiredRule{
		{Path: "docs/dev/backlog", Reason: "records"},
		{Path: "gone", Reason: "stale"},
		{Path: "docs/dev/backlog/b.md", Name: "/km", Reason: "this one by name"},
	}
	partitionRetired(&scan, rules)
	if len(scan.Hits) != 1 || scan.Hits[0].Path != "src/x.go" {
		t.Fatalf("hits left in review = %+v, want only src/x.go", scan.Hits)
	}
	want := []RetiredAccepted{{Rule: rules[0], Hits: 2}, {Rule: rules[1], Hits: 0}, {Rule: rules[2], Hits: 1}}
	if !reflect.DeepEqual(scan.Accepted, want) || scan.AcceptedHits != 2 {
		t.Fatalf("accepted = %+v (total %d), want %+v (total 2)", scan.Accepted, scan.AcceptedHits, want)
	}
}

func TestKeptReportsGitHistoryOrExplicitManualReview(t *testing.T) {
	for _, row := range []struct{ name, from, pinned, kept, earlier string }{
		{"git history", "aaa1111", "bbb2222", "untaken upstream change: git -C /blueprint diff aaa1111 bbb2222 -- templates/project/a.md", "untaken since aaa1111"},
		{"self hosted", UnknownSelfHostedSHA, UnknownSelfHostedSHA, "untaken upstream change unavailable: no recorded git history; compare the current template and local file by hand\n      review: diff -u /blueprint/templates/project/a.md /project/.claude/a.md", "untaken change has no recorded git history"},
		{"missing earlier history", UnknownSelfHostedSHA, "bbb2222", "untaken upstream change unavailable: no recorded git history; compare the current template and local file by hand\n      review: diff -u /blueprint/templates/project/a.md /project/.claude/a.md", "untaken change has no recorded git history"},
	} {
		t.Run(row.name, func(t *testing.T) {
			item := projectReportItem{
				Status:   projectKept,
				Local:    ".claude/a.md",
				Template: "project/a.md",
				Pin: FilePin{
					PinnedSHA: row.pinned, Kept: &KeptNote{FromSHA: row.from, At: "2026-10-12", Reason: "local tool"},
				},
			}
			report := projectReport{
				Root:   "/project",
				Store:  Store{Root: "/blueprint"},
				Counts: map[projectStatus]int{projectKept: 1},
				Items:  []projectReportItem{item},
			}
			var kept, earlier bytes.Buffer
			writeKeptHuman(&kept, report)
			writeKeptEarlier(&earlier, item)
			if !strings.Contains(kept.String(), row.kept) {
				t.Errorf("kept output = %q, want %q", kept.String(), row.kept)
			}
			if !strings.Contains(earlier.String(), row.earlier) {
				t.Errorf("earlier output = %q, want %q", earlier.String(), row.earlier)
			}
		})
	}
}
