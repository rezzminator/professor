package transcript

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type readLine struct {
	text     string
	complete bool
}

func readAll(t *testing.T, path string, start int64) ([]readLine, int64, int64) {
	t.Helper()
	var got []readLine
	parsed, read, err := ReadLines(path, start, func(line []byte, complete bool) {
		got = append(got, readLine{string(line), complete})
	})
	if err != nil {
		t.Fatalf("ReadLines(%s, %d): %v", path, start, err)
	}
	return got, parsed, read
}

func TestReadLinesHandsEveryLineAndMarksATornTail(t *testing.T) {
	long := strings.Repeat("x", 200<<10) // longer than the read buffer
	path := filepath.Join(t.TempDir(), "t.jsonl")
	content := "{\"a\":1}\n\n" + long + "\r\n{\"torn\":"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, parsed, read := readAll(t, path, 0)
	want := []readLine{{`{"a":1}`, true}, {"", true}, {long + "\r", true}, {`{"torn":`, false}}
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf(
				"line %d = %.40q complete=%v, want %.40q complete=%v",
				i,
				got[i].text,
				got[i].complete,
				want[i].text,
				want[i].complete,
			)
		}
	}
	if wantParsed := int64(len(content) - len(`{"torn":`)); parsed != wantParsed || read != int64(len(content)) {
		t.Fatalf("parsedOffset %d bytesRead %d, want %d and %d", parsed, read, wantParsed, len(content))
	}
	resumed, parsed2, _ := readAll(t, path, parsed)
	if len(resumed) != 1 || resumed[0] != (readLine{`{"torn":`, false}) || parsed2 != parsed {
		t.Fatalf("resuming at the parsed offset = %+v, %d", resumed, parsed2)
	}
}

func TestReadLinesReportsAMissingFile(t *testing.T) {
	_, _, err := ReadLines(filepath.Join(t.TempDir(), "gone.jsonl"), 0, func([]byte, bool) {})
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("ReadLines of a missing file = %v, want fs.ErrNotExist", err)
	}
}
