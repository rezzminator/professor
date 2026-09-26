package chat

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
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
	root := testjail.Fleet(t)
	ctx := context.Background()
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
	matches, err := Find(ctx, nil, FindRequest{Excerpt: first + "\n" + second})
	if err != nil || len(matches) != 2 {
		t.Fatalf("Find = %+v, %v; want the two transcripts that hold a needle", matches, err)
	}
	if best := matches[0]; best.ID != "both" || best.Hits != 2 || best.Needles != 2 ||
		best.First != "2026-01-01T00:00:00Z" || best.Last != "2026-01-02T00:00:00Z" {
		t.Fatalf("best match = %+v, want both needles and its timestamp range", best)
	}
	if matches, err := Find(ctx, nil, FindRequest{Excerpt: first + "\n" + second, Self: "both"}); err != nil ||
		len(matches) != 1 || matches[0].ID != "one" {
		t.Fatalf("Find(Self: both) = %+v, %v; want the asking session's transcript left out", matches, err)
	}
	if _, err := Find(
		ctx,
		nil,
		FindRequest{Excerpt: "a sentence no transcript here holds"},
	); !errors.Is(
		err,
		ErrNoExcerptMatch,
	) {
		t.Fatalf("Find(absent) = %v, want ErrNoExcerptMatch", err)
	}
	if _, err := Find(ctx, nil, FindRequest{Excerpt: "  "}); !errors.Is(err, ErrNoExcerpt) {
		t.Fatalf("Find(blank) = %v, want ErrNoExcerpt", err)
	}
}

// TestClaudeTranscriptsListsASymlinkedRootOnce is a REGRESSION test for the
// seat roots that share ONE projects directory by symlink: the seen map was
// keyed by the literal path, so ~/.cc/1/projects/p/x.jsonl and
// ~/.cc/2/projects/p/x.jsonl — the same file — were both listed, and chat_find
// reported one session three times. A file entry whose own link is dangling
// still has to be listed (EvalSymlinks fails there, and a file that cannot be
// resolved is not a reason to drop it or to fail the whole search).
func TestClaudeTranscriptsListsASymlinkedRootOnce(t *testing.T) {
	root := testjail.Fleet(t)
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

	files, err := ClaudeTranscripts(nil)
	if err != nil {
		t.Fatalf("ClaudeTranscripts() error = %v, want the roots listed", err)
	}
	want := []string{filepath.Join(mirror, "p", "live.jsonl"), filepath.Join(third, "q", "dangle.jsonl")}
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
