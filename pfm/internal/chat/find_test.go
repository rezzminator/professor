package chat

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestExcerptNeedlesStripsDecorationAndKeepsTheFiveLongest pins the needle
// rules: quote/list/heading decoration stripped, 20+ characters only, the five
// longest, longest first — and a short literal (a chat name) searched whole.
func TestExcerptNeedlesStripsDecorationAndKeepsTheFiveLongest(t *testing.T) {
	excerpt := strings.Join([]string{
		"# short",
		"> aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\r",
		"- bbbbbbbbbbbbbbbbbbbbbbbbb",
		"* cccccccccccccccccccccc   ",
		"ddddddddddddddddddddd",
		"eeeeeeeeeeeeeeeeeeeee",
		"fffffffffffffffffffff",
		"nineteen characters",
	}, "\n")
	want := []string{
		strings.Repeat("a", 30), strings.Repeat("b", 25), strings.Repeat("c", 22),
		strings.Repeat("d", 21), strings.Repeat("e", 21),
	}
	if needles := ExcerptNeedles(excerpt); !reflect.DeepEqual(needles, want) {
		t.Fatalf("ExcerptNeedles() = %q, want %q", needles, want)
	}
	if got := ExcerptNeedles("  LUNA:ORCHESTRATOR \n"); !reflect.DeepEqual(got, []string{"LUNA:ORCHESTRATOR"}) {
		t.Fatalf("short literal needles = %q, want the literal itself", got)
	}
	if got := ExcerptNeedles(" \n\t"); len(got) != 0 {
		t.Fatalf("blank excerpt needles = %q, want none", got)
	}
}

// TestFindRanksByHitsAndNamesEachEmptyAnswer pins the shared search: every
// match, most needles first — and each way of finding nothing is its own
// error, so no surface reads "could not search" as "not there".
func TestFindRanksByHitsAndNamesEachEmptyAnswer(t *testing.T) {
	root := memoryFleet(t)
	ctx, recorder := obs.Test(t)
	if _, err := Find(
		ctx,
		nil,
		FindRequest{Excerpt: "the verb layer answers every surface"},
	); !errors.Is(
		err,
		ErrNoTranscriptRegistry,
	) {
		t.Fatalf("Find over an empty registry = %v, want ErrNoTranscriptRegistry", err)
	}
	const first, second = "the verb layer answers every surface", "one implementation behind each tool"
	directory := filepath.Join(root, "claude", "find")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"both.jsonl": `{"type":"user","timestamp":"2026-01-01T00:00:00Z","message":{"content":"` + first + `"}}` + "\n" +
			`{"type":"user","timestamp":"2026-01-02T00:00:00Z","message":{"content":"` + second + `"}}` + "\n",
		"one.jsonl":     `{"type":"user","message":{"content":"` + second + `"}}` + "\n",
		"neither.jsonl": `{"type":"user","message":{"content":"unrelated"}}` + "\n",
	} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A transcript removed since the listing is no longer a candidate, never a failed search.
	if err := os.Symlink(filepath.Join(directory, "removed"), filepath.Join(directory, "gone.jsonl")); err != nil {
		t.Fatal(err)
	}
	// One transcript and one project directory that cannot be read cost only themselves.
	loop := filepath.Join(directory, "loop.jsonl")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}
	badProject := filepath.Join(root, "claude", "bad")
	if err := os.MkdirAll(badProject, 0o700); err != nil {
		t.Fatal(err)
	}
	listProject := readProjectDirectory
	readProjectDirectory = func(path string) ([]os.DirEntry, error) {
		if path == badProject {
			return nil, errors.New("project listing denied")
		}
		return listProject(path)
	}
	t.Cleanup(func() { readProjectDirectory = listProject })
	matches, err := Find(ctx, nil, FindRequest{Excerpt: first + "\n" + second})
	if err != nil || len(matches) != 2 {
		t.Fatalf("Find = %+v, %v; want the two transcripts that hold a needle", matches, err)
	}
	var warned []string
	for _, record := range recorder.Records() {
		path, _ := record.Field("path")
		cause, _ := record.Field("err")
		warned = append(warned, fmt.Sprintf("%s %s: %v", record.Message, path, cause))
	}
	wantWarned := []string{
		"transcript project directory skipped " + badProject + ": project listing denied",
		"find skipped an unreadable transcript " + loop + ": open " + loop + ": too many levels of symbolic links",
	}
	if !reflect.DeepEqual(warned, wantWarned) {
		t.Fatalf("find warnings = %q, want %q", warned, wantWarned)
	}
	// A surface resolving one chat never picks from a partial list.
	wantPartial := "2 transcript(s) or project director(ies) could not be read: the best match among the rest may not be the chat meant"
	if partial, err := Find(ctx, nil, FindRequest{Excerpt: first + "\n" + second, Complete: true}); partial != nil ||
		err == nil || err.Error() != wantPartial {
		t.Fatalf("Find(Complete) = %+v, %v; want %q", partial, err, wantPartial)
	}
	if files, skipped, err := ClaudeTranscripts(nil); err != nil || skipped != 1 || len(files) != 5 {
		t.Fatalf("ClaudeTranscripts() = %d files, %d skipped, %v; want the 5 listed transcripts and 1 skipped",
			len(files), skipped, err)
	}
	if best := matches[0]; best.ID != "both" || best.Hits != 2 || best.Needles != 2 ||
		best.First != "2026-01-01T00:00:00Z" || best.Last != "2026-01-02T00:00:00Z" {
		t.Fatalf("best match = %+v, want both needles and its timestamp range", best)
	}
	if matches, err := Find(ctx, nil, FindRequest{Excerpt: first + "\n" + second, Self: "both"}); err != nil ||
		len(matches) != 1 || matches[0].ID != "one" {
		t.Fatalf("Find(Self: both) = %+v, %v; want the asking session's transcript left out", matches, err)
	}
	_, err = Find(ctx, nil, FindRequest{Excerpt: "a sentence no transcript here holds"})
	if want := ErrNoExcerptMatch.Error() +
		"; 2 transcript(s) or project director(ies) could not be read and were skipped"; !errors.Is(err, ErrNoExcerptMatch) ||
		err.Error() != want {
		t.Fatalf("Find(absent) = %v, want ErrNoExcerptMatch naming what was skipped: %q", err, want)
	}
	if _, err := Find(ctx, nil, FindRequest{Excerpt: "  "}); !errors.Is(err, ErrNoExcerpt) {
		t.Fatalf("Find(blank) = %v, want ErrNoExcerpt", err)
	}
}

// The caller's jail roots retain their given path spellings, including
// symlinked roots and dangling transcript entries.
func TestClaudeTranscriptsUsesGivenPaths(t *testing.T) {
	root := memoryFleet(t)
	shared := filepath.Join(root, "seat-a", "projects")
	third := filepath.Join(root, "seat-c", "projects")
	for _, directory := range []string{filepath.Join(shared, "p"), filepath.Join(third, "q")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(shared, "p", "live.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "gone", "dangle.jsonl"),
		filepath.Join(third, "q", "dangle.jsonl")); err != nil {
		t.Fatal(err)
	}
	mirror := filepath.Join(root, "seat-b", "projects")
	if err := os.MkdirAll(filepath.Dir(mirror), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(shared, mirror); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_CLAUDE_ROOTS", strings.Join([]string{mirror, shared, third}, string(os.PathListSeparator)))

	files, skipped, err := ClaudeTranscripts(nil)
	if err != nil || skipped != 0 {
		t.Fatalf("ClaudeTranscripts() skipped %d, error = %v; want the roots listed whole", skipped, err)
	}
	want := []string{
		filepath.Join(mirror, "p", "live.jsonl"),
		filepath.Join(shared, "p", "live.jsonl"),
		filepath.Join(third, "q", "dangle.jsonl"),
	}
	sort.Strings(want)
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("ClaudeTranscripts() = %q, want one entry per distinct file %q", files, want)
	}
}

// TestTranscriptRangeSpansALineOverEightMegabytes is a REGRESSION test for
// transcriptRange's former bufio.Scanner, whose 8 MB max token buffer made it
// return bufio.ErrTooLong (silently ignored, matching ScanLines's old code)
// and stop scanning right there — a transcript with a giant single line (a
// huge tool result) between its first and last timestamped records lost its
// real First/Last for whatever the scanner had already seen. bytes.Split has
// no such limit.
func TestTranscriptRangeSpansALineOverEightMegabytes(t *testing.T) {
	huge := `{"type":"user","message":{"content":"` + strings.Repeat("x", 9*1024*1024) + `"}}`
	raw := []byte(
		`{"type":"user","timestamp":"2026-01-01T00:00:00Z","message":{"content":"first"}}` + "\n" +
			huge + "\n" +
			`{"type":"user","timestamp":"2026-01-03T00:00:00Z","message":{"content":"last"}}` + "\n",
	)
	first, last := transcriptRange(raw)
	if first != "2026-01-01T00:00:00Z" || last != "2026-01-03T00:00:00Z" {
		t.Fatalf("transcriptRange() = (%q, %q), want the outer two timestamps around the oversized line", first, last)
	}
}
