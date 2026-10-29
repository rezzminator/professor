package transcript

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// resume runs ResumeLines and returns the start it chose, the lines, next
// and done.
func resume(t *testing.T, path string, from Resume, deadline time.Time) (int64, []readLine, Resume, bool) {
	t.Helper()
	start := int64(-1)
	var got []readLine
	next, done, err := ResumeLines(
		path,
		from,
		deadline,
		func(at int64) { start = at },
		func(line []byte, complete bool) {
			got = append(got, readLine{string(line), complete})
		},
	)
	if err != nil {
		t.Fatalf("ResumeLines(%s, %+v): %v", path, from, err)
	}
	return start, got, next, done
}

// TestResumeLinesResumesOnlyTheSameFileGrownByAppending: an append resumes
// at the last whole line, its torn tail handed again; a shrink, a rewrite of
// the bytes before the offset and a replaced file start at 0; a spent
// deadline stops before the first line and keeps the offset.
func TestResumeLinesResumesOnlyTheSameFileGrownByAppending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.jsonl")
	head := strings.Repeat("{\"a\":1}\n", 100) // longer than one anchor
	if err := os.WriteFile(path, []byte(head+"{\"torn\":"), 0o600); err != nil {
		t.Fatal(err)
	}
	start, got, first, done := resume(t, path, Resume{}, time.Time{})
	if start != 0 || !done || len(got) != 101 || got[100] != (readLine{`{"torn":`, false}) ||
		first.Offset != int64(len(head)) || len(first.Anchor) != anchorBytes {
		t.Fatalf("first read: start %d, done %v, %d lines, next %+v", start, done, len(got), first)
	}
	appendTo := func(text string) {
		file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString(text); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
	}
	appendTo("1}\n")
	start, got, second, done := resume(t, path, first, time.Time{})
	if start != first.Offset || !done || len(got) != 1 || got[0] != (readLine{`{"torn":1}`, true}) ||
		second.Offset != first.Offset+int64(len(`{"torn":1}`)+1) {
		t.Fatalf("append: start %d, done %v, lines %+v, next %+v", start, done, got, second)
	}
	appendTo("{\"b\":2}\n")
	start, got, spent, done := resume(t, path, second, time.Now().Add(-time.Second))
	if start != second.Offset || done || len(got) != 0 || spent.Offset != second.Offset {
		t.Fatalf("spent deadline: start %d, done %v, lines %+v, next %+v", start, done, got, spent)
	}
	if start, _, _, done = resume(t, path, spent, time.Now().Add(-time.Second)); start != second.Offset || done {
		t.Fatalf("spent deadline again: start %d, done %v", start, done)
	}
	rewritten := strings.Replace(head, `{"a":1}`, `{"a":2}`, 1)
	cases := []struct {
		name   string
		change func()
	}{
		{"shrunk", func() {
			if err := os.WriteFile(path, []byte(head[:20]), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"rewritten before the offset", func() {
			if err := os.WriteFile(
				path,
				[]byte(head[:len(head)-8]+`{"c":3}`+"\n{\"torn\":1}\n{\"b\":2}\n"),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
		}},
		{"replaced", func() {
			replacement := path + ".new"
			if err := os.WriteFile(replacement, []byte(rewritten+"{\"torn\":1}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, path); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range cases {
		tc.change()
		if start, _, _, done := resume(t, path, second, time.Time{}); start != 0 || !done {
			t.Fatalf("%s: start %d, done %v; want a read from the top", tc.name, start, done)
		}
	}
}

// TestResumeLinesFinishesTheLineItIsReadingWhenTheBudgetRunsOut: a deadline
// passing inside a line longer than the read buffer stops the read after that
// line, not inside it, so a line no render could read whole within its budget
// is still folded once and the next read starts past it.
func TestResumeLinesFinishesTheLineItIsReadingWhenTheBudgetRunsOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "long.jsonl")
	long := strings.Repeat("x", 300<<10)
	if err := os.WriteFile(path, []byte(long+"\nshort\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Unix(1_800_000_000, 0)
	checks, saved := 0, budgetNow
	budgetNow = func() time.Time {
		checks++
		if checks == 1 {
			return deadline.Add(-time.Nanosecond) // the first look finds budget left
		}
		return deadline.Add(time.Second)
	}
	defer func() { budgetNow = saved }()
	var lines []int
	next, done, err := ResumeLines(path, Resume{}, deadline, func(int64) {}, func(line []byte, complete bool) {
		if complete {
			lines = append(lines, len(line))
		}
	})
	if err != nil || done || next.Offset != int64(len(long)+1) || len(lines) != 1 || lines[0] != len(long) {
		t.Fatalf("next %d, done %v, lines %v, %v; want the long line folded whole, the read stopped at %d",
			next.Offset, done, lines, err, len(long)+1)
	}
}
