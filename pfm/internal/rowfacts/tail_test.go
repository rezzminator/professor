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

func TestScanTailHandsOverALastLineLongerThanEveryWidth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	big := strings.Repeat("x", 4096)
	writeFile(t, path, "needle\n"+strings.Repeat("filler line\n", 40)+big+"\n")
	seen := false
	err := scanTail(path, []int64{64, 128}, func(data []byte) bool {
		if strings.HasSuffix(string(data), big+"\n") && strings.Contains(string(data), "filler line\n") {
			seen = true
		}
		return seen
	})
	if err != nil || !seen {
		t.Fatalf("no scan saw the whole last line with the lines before it: seen=%v err=%v", seen, err)
	}
}
