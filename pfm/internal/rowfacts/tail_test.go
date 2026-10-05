package rowfacts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestReadTailCutsToWholeLinesAndSaysWhenItReachedTheStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	writeFile(t, path, "first line\nsecond line\nthird line\n")
	whole, isWhole, err := readTranscriptTail(path, 1<<20)
	if err != nil || !isWhole || string(whole) != "first line\nsecond line\nthird line\n" {
		t.Fatalf("a generous tail is the whole file: %q whole=%v err=%v", whole, isWhole, err)
	}
	part, isWhole, err := readTranscriptTail(path, 20)
	if err != nil || isWhole || string(part) != "third line\n" {
		t.Fatalf("a short tail drops its cut first line: %q whole=%v err=%v", part, isWhole, err)
	}
	none, isWhole, err := readTranscriptTail(path, 4)
	if err != nil || isWhole || len(none) != 0 {
		t.Fatalf("a tail inside one long line has nothing whole to read: %q whole=%v err=%v", none, isWhole, err)
	}
	if _, _, err := readTranscriptTail(filepath.Join(t.TempDir(), "gone"), 10); err == nil {
		t.Error("a file that cannot be opened is an error")
	}
}

func TestLinesBackwardVisitsNewestFirstAndStopsWhenAsked(t *testing.T) {
	var seen []string
	linesBackward([]byte("one\n\ntwo\nthree\n"), func(line []byte) bool {
		seen = append(seen, string(line))
		return string(line) == "two"
	})
	if strings.Join(seen, ",") != "three,two" {
		t.Fatalf("visited %v, want newest first, blank lines skipped, stopping at two", seen)
	}
}

func TestScanTailWidensUntilTheTailAnswersOrTheFileEnds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	writeFile(t, path, "needle\n"+strings.Repeat("filler line\n", 40))
	reads := 0
	err := scanTail(path, []int64{64, 128, 1 << 20}, func(data []byte) bool {
		reads++
		return strings.Contains(string(data), "needle")
	})
	if err != nil || reads != 3 {
		t.Fatalf("the needle sits beyond the first two widths: reads=%d err=%v", reads, err)
	}
	reads = 0
	_ = scanTail(path, []int64{1 << 20, 2 << 20}, func([]byte) bool { reads++; return false })
	if reads != 1 {
		t.Errorf("a file read whole is not read again at a wider width: %d reads", reads)
	}
}
