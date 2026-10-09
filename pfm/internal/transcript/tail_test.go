package transcript

import (
	"os"
	"path/filepath"
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
	none, isWhole, err := ReadTailLines(path, 4)
	if err != nil || isWhole || len(none) != 0 {
		t.Fatalf("a tail inside one long line has nothing whole to read: %q whole=%v err=%v", none, isWhole, err)
	}
	if _, _, err := ReadTailLines(filepath.Join(t.TempDir(), "gone"), 10); err == nil {
		t.Error("a file that cannot be opened is an error")
	}
}
