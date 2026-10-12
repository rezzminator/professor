package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTailLinesCutsToWholeLinesAndSaysWhenItReachedTheStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(path, []byte("first line\nsecond line\nthird line\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	whole, isWhole, err := ReadTailLines(path, 1<<20)
	if err != nil || !isWhole || string(whole) != "first line\nsecond line\nthird line\n" {
		t.Fatalf("a generous tail is the whole file: %q whole=%v err=%v", whole, isWhole, err)
	}
	part, isWhole, err := ReadTailLines(path, 20)
	if err != nil || isWhole || string(part) != "third line\n" {
		t.Fatalf("a short tail drops its cut first line: %q whole=%v err=%v", part, isWhole, err)
	}
	last, isWhole, err := ReadTailLines(path, 4)
	if err != nil || isWhole || string(last) != "third line\n" {
		t.Fatalf("a tail inside the last line reads further to that whole line: %q whole=%v err=%v", last, isWhole, err)
	}
	if _, _, err := ReadTailLines(filepath.Join(t.TempDir(), "gone"), 10); err == nil {
		t.Error("a file that cannot be opened is an error")
	}
}

// A last line longer than the window is read whole, with the window's worth of
// whole lines before it: the lines the title lookup and the row scans came for
// sit before a transcript's one very large entry.
func TestReadTailLinesReadsFurtherWhenTheLastLineIsLongerThanTheWindow(t *testing.T) {
	big := strings.Repeat("x", 100)
	for name, ending := range map[string]string{"newline-ended": "\n", "unterminated": ""} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "t.jsonl")
			content := "first line\nsecond line\nthird line\n" + big + ending
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			got, isWhole, err := ReadTailLines(path, 30)
			if want := "second line\nthird line\n" + big + ending; err != nil || isWhole || string(got) != want {
				t.Fatalf("tail = %q whole=%v err=%v, want %q", got, isWhole, err, want)
			}
			got, isWhole, err = ReadTailLines(path, int64(len(content)))
			if err != nil || !isWhole || string(got) != content {
				t.Fatalf("a window over the whole file = %q whole=%v err=%v", got, isWhole, err)
			}
		})
	}
}

// A last line past the reader's bound is an error that says so, never an empty tail.
func TestReadTailLinesSaysWhenTheLastLineIsBeyondTheBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("first line\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxTailLine + 4096); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	got, _, err := ReadTailLines(path, 1024)
	if err == nil || !strings.Contains(err.Error(), "last line of "+path+" is longer than") || len(got) != 0 {
		t.Fatalf("tail = %d bytes, err = %v; want an error naming the bound", len(got), err)
	}
}
