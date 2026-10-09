package tokens

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/pricing"
)

func TestGapsLineNamesEveryGapAndBoundsTheLists(t *testing.T) {
	a, _, _ := testAuditor(t)
	pricingLine := "pricing: saved table " + fixture(t, "model-cost.json") + "; Standard service, published " +
		"prompt/context tiers (Codex aggregates use the run peak tier); table fetched 2026-01-01T00:00:00Z from " +
		"anthropic, openai; not historical billing\n"
	if got := a.gapsLine(); got != pricingLine+"data gaps: none" {
		t.Errorf("a clean scan:\n got %q\nwant %q", got, pricingLine+"data gaps: none")
	}
	a.prices.refresh = &pricing.RefreshReport{Status: "failed"}
	a.prices.override = &pricing.Override{Path: "/cfg/prices.json", Rows: 2}
	a.scan.badLines, a.scan.noTimestamp, a.scan.dupFiles = 3, 1, 2
	a.scan.unpricedCalls = 1
	a.scan.unpricedModels.set("gpt-x", 1.0)
	a.scan.copiesOnly = []copyOnly{{"f1", "0123456789abcdef"}, {"f2", "b"}, {"f3", "c"}, {"f4", "d"}}
	a.scan.notes = []string{"n1", "n2", "n3", "n4", "n5", "n6"}
	a.scan.readErrors = []string{"e1", "e2", "e3", "e4"}
	want := pricingLine + "data gaps: price refresh failed (no cause given) — served the table fetched " +
		"2026-01-01T00:00:00Z · price override active: 2 rows from /cfg/prices.json · 3 malformed lines · " +
		"1 calls without a timestamp (dropped) · 1 UNPRICED calls {\"gpt-x\":1} — tokens counted, dollars \"n/a\"; " +
		"check exact IDs and available rates with pfm model-cost --json --all · 4 transcripts hold only copied " +
		"calls: 0123456789ab, b, c · 2 duplicate files skipped · n1 · n2 · n3 · n4 · " +
		"2 further notes (see --out JSON scan.notes) · 4 READ ERRORS: e1 | e2 | e3"
	if got := a.gapsLine(); got != want {
		t.Errorf("every gap:\n got %q\nwant %q", got, want)
	}
	a.prices.refresh.Error = "dial refused"
	if got := a.gapsLine(); !strings.Contains(got, "price refresh failed (dial refused) — served") {
		t.Errorf("the refresh cause is named: %q", got)
	}
}

func TestWalkKeepsWindowTranscriptsAndReportsUnreadableDirs(t *testing.T) {
	a, _, _ := testAuditor(t, "--since", "1d")
	root := t.TempDir()
	fresh := writeTranscript(t, filepath.Join(root, "b", "new.jsonl"), "{}")
	old := writeTranscript(t, filepath.Join(root, "a", "old.jsonl"), "{}")
	writeTranscript(t, filepath.Join(root, "a", "notes.txt"), "x")
	week := goldenNow.Add(-7 * 24 * time.Hour)
	if err := os.Chtimes(old, week, week); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fresh, goldenNow, goldenNow); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "gone")
	var found []string
	a.walk(root, &found)
	a.walk(missing, &found)
	if !reflect.DeepEqual(found, []string{fresh}) || a.scan.skippedOld != 1 {
		t.Errorf("walk = %v, skippedOld %d; want only %s and one old file", found, a.scan.skippedOld, fresh)
	}
	want := []string{missing + ": open " + missing + ": no such file or directory"}
	if !reflect.DeepEqual(a.scan.readErrors, want) {
		t.Errorf("readErrors = %q, want %q: a missing directory is a failure to look", a.scan.readErrors, want)
	}
}

func TestEachLineSkipsEmptyLinesAndKeepsAnUnterminatedTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte("a\n\nb\nc"), 0o644); err != nil {
		t.Fatal(err)
	}
	var lines []string
	if err := eachLine(path, func(line []byte) { lines = append(lines, string(line)) }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(lines, []string{"a", "b", "c"}) {
		t.Errorf("eachLine = %q, want a, b, c", lines)
	}
	if err := eachLine(filepath.Join(t.TempDir(), "none"), func([]byte) {}); err == nil {
		t.Errorf("eachLine of a missing file returned no error")
	}
}

func TestProjectPathsFoldAndHomesStayApart(t *testing.T) {
	folds := map[string]string{
		"/w/repo/.worktrees/flight/sub": "/w/repo",
		"/w/repo///":                    "/w/repo",
		"/w/repo/pkg":                   "/w/repo/pkg",
	}
	for in, want := range folds {
		if got := foldCwd(in); got != want {
			t.Errorf("foldCwd(%q) = %q, want %q", in, got, want)
		}
	}
	for path, want := range map[string]bool{"/home/test": true, "/root": true, "/": true, "/home/test/repo": false, "/w": false} {
		if got := isHome(path); got != want {
			t.Errorf("isHome(%q) = %v, want %v", path, got, want)
		}
	}
	_, err := os.Open(filepath.Join(t.TempDir(), "x"))
	if got := describe(
		err,
	); !strings.HasPrefix(got, "open ") ||
		!strings.HasSuffix(got, ": no such file or directory") {
		t.Errorf("describe(path error) = %q, want the operating system's words", got)
	}
	if got := describe(os.ErrClosed); got != os.ErrClosed.Error() {
		t.Errorf("describe(plain error) = %q", got)
	}
}
