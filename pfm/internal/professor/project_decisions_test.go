package professor

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	pfmpaths "github.com/rezzminator/professor/pfm/internal/paths"
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

// TestRetiredAcceptanceScopesStayIndependent covers F7 at the matching and
// report boundary, including both scanner limit diagnostics and earlier hits.
func TestRetiredAcceptanceScopesStayIndependent(t *testing.T) {
	for _, row := range []struct {
		name, rules                          string
		openHits, openCoverage, acceptedHits int
	}{
		{"legacy path hit rule", `[{"path":"docs","reason":"hits"}]`, 0, 2, 1},
		{"named hit rule", `[{"path":"docs","name":"/km","reason":"hit"}]`, 0, 2, 1},
		{"coverage only", `[{"path":"docs","coverage":true,"reason":"coverage"}]`, 1, 0, 0},
		{"both scopes", `[{"path":"docs","reason":"hits"},{"path":"docs","coverage":true,"reason":"coverage"}]`, 0, 0, 1},
		{"coverage path boundary", `[{"path":"doc","coverage":true,"reason":"unrelated"}]`, 1, 2, 0},
		{"invalid named coverage fails closed", `[{"path":"docs","name":"/km","coverage":true,"reason":"invalid"}]`, 1, 2, 0},
	} {
		t.Run(row.name, func(t *testing.T) {
			var rules []RetiredRule
			if err := json.Unmarshal([]byte(row.rules), &rules); err != nil {
				t.Fatal(err)
			}
			failure := RetiredNameFailure{Path: "docs/unreadable", Error: "read failed"}
			scan := RetiredNameScan{
				Hits: []RetiredNameHit{{Path: "docs/archive.txt", Line: 1, Name: "/km"}},
				Incomplete: []RetiredNameFailure{
					{
						Path:  "docs/archive.txt",
						Error: "8388608-byte scan limit reached; line 8193 and the text from there on were not scanned",
					},
					{
						Path:  "docs/long.txt",
						Error: "line 1 is longer than 4194304 bytes; the text from there on was not scanned",
					},
				},
				Failures: []RetiredNameFailure{failure},
			}
			partitionRetired(&scan, rules)
			if len(scan.Hits) != row.openHits || len(scan.Incomplete) != row.openCoverage ||
				scan.AcceptedHits != row.acceptedHits {
				t.Fatalf(
					"scope partition: open hits=%d coverage=%d accepted hits=%d, want %d/%d/%d; %+v",
					len(
						scan.Hits,
					),
					len(scan.Incomplete),
					scan.AcceptedHits,
					row.openHits,
					row.openCoverage,
					row.acceptedHits,
					scan,
				)
			}
			if !reflect.DeepEqual(scan.Failures, []RetiredNameFailure{failure}) {
				t.Fatalf("failures = %+v", scan.Failures)
			}
			for _, accepted := range scan.Accepted {
				var shape struct {
					Coverage bool `json:"coverage"`
				}
				raw, err := json.Marshal(accepted.Rule)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(raw, &shape); err != nil {
					t.Fatal(err)
				}
				if shape.Coverage && accepted.Hits != 0 || !shape.Coverage && len(accepted.Incomplete) != 0 {
					t.Fatalf("rule crosses scope: %+v", accepted)
				}
			}
		})
	}
}

func TestAcceptCoverageIsExplicitAndUndoIsScoped(t *testing.T) {
	for _, row := range []struct {
		name             string
		hits, incomplete bool
		flags            []string
		wantCode         int
	}{
		{"legacy hit accept", true, true, nil, 0},
		{"explicit coverage accept", true, true, []string{"--coverage"}, 0},
		{"coverage cannot accept hits", true, false, []string{"--coverage"}, 1},
		{"hits cannot accept coverage", false, true, nil, 1},
		{"empty scan", false, false, []string{"--coverage"}, 1},
		{"coverage and name conflict", true, true, []string{"--coverage", "--name", "/km"}, 2},
	} {
		t.Run(row.name, func(t *testing.T) {
			project, home := newUpdatedGitStoreProject(t, newGitScaffoldStore(t))
			runtime := config.Runtime{Paths: pfmpaths.Values{Home: home}}
			scan := func(string, map[string]bool) RetiredNameScan {
				var result RetiredNameScan
				if row.hits {
					result.Hits = []RetiredNameHit{{Path: "docs/archive.txt", Line: 1, Name: "/km"}}
				}
				if row.incomplete {
					result.Incomplete = []RetiredNameFailure{{Path: "docs/archive.txt", Error: "scan limit"}}
				}
				return result
			}
			var out, errOut bytes.Buffer
			args := append([]string{"docs", "--reason", "reviewed", "--root", project}, row.flags...)
			if code := RunProjectUpdate("accept", args, &out, &errOut, runtime, scan); code != row.wantCode {
				t.Fatalf(
					"accept code=%d want %d stdout=%q stderr=%q",
					code,
					row.wantCode,
					out.String(),
					errOut.String(),
				)
			}
			baseline, err := Load(project)
			if err != nil {
				t.Fatal(err)
			}
			if row.wantCode != 0 {
				if len(baseline.RetiredAccepted) != 0 {
					t.Fatalf("failed accept persisted %+v", baseline.RetiredAccepted)
				}
				return
			}
			if len(baseline.RetiredAccepted) != 1 {
				t.Fatalf("rules = %+v", baseline.RetiredAccepted)
			}
			partition := scan("", nil)
			partitionRetired(&partition, baseline.RetiredAccepted)
			coverage := len(row.flags) != 0
			if coverage && (len(partition.Hits) != 1 || len(partition.Incomplete) != 0) ||
				!coverage && (len(partition.Hits) != 0 || len(partition.Incomplete) != 1) {
				t.Fatalf("accept crossed scope after reload: %+v", partition)
			}
			// Add the other scope at the same path; both survive persistence.
			other := []string{"docs", "--reason", "other", "--root", project}
			if !coverage {
				other = append(other, "--coverage")
			}
			if code := RunProjectUpdate("accept", other, &out, &errOut, runtime, scan); code != 0 {
				t.Fatalf("other scope code=%d stderr=%q", code, errOut.String())
			}
			baseline, err = Load(project)
			if err != nil || len(baseline.RetiredAccepted) != 2 {
				t.Fatalf("both scopes = %+v err=%v", baseline.RetiredAccepted, err)
			}
			undo := append([]string{"docs", "--undo", "--root", project}, row.flags...)
			if code := RunProjectUpdate("accept", undo, &out, &errOut, runtime, nil); code != 0 {
				t.Fatalf("undo code=%d stderr=%q", code, errOut.String())
			}
			baseline, err = Load(project)
			if err != nil || len(baseline.RetiredAccepted) != 1 {
				t.Fatalf("remaining scope = %+v err=%v", baseline.RetiredAccepted, err)
			}
			remaining := scan("", nil)
			partitionRetired(&remaining, baseline.RetiredAccepted)
			if coverage && (len(remaining.Hits) != 0 || len(remaining.Incomplete) != 1) ||
				!coverage && (len(remaining.Hits) != 1 || len(remaining.Incomplete) != 0) {
				t.Fatalf("undo removed wrong scope: %+v", remaining)
			}
		})
	}
}

func TestCoverageAcceptRenderingNamesItsScope(t *testing.T) {
	var rule RetiredRule
	if err := json.Unmarshal(
		[]byte(`{"path":"docs/old notes.md","coverage":true,"reason":"reviewed"}`),
		&rule,
	); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	writeAcceptedHuman(&out, &RetiredNameScan{Accepted: []RetiredAccepted{{Rule: rule}}})
	for _, want := range []string{"incomplete coverage", "pfm update accept --undo 'docs/old notes.md' --coverage"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("accepted report = %q, want %q", out.String(), want)
		}
	}
	out.Reset()
	writeSessionGuide(&out)
	if !strings.Contains(out.String(), `accept <path> --coverage --reason`) {
		t.Fatalf("coverage guide = %q", out.String())
	}
}
