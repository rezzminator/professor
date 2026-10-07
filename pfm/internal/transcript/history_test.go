package transcript

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFindHistoryTakesTheNewestMatchOfTheFirstPoolThatHasOne(t *testing.T) {
	const slug, sid = "-fixture-project", "abc"
	empty, first, second := t.TempDir(), t.TempDir(), t.TempDir()
	older := filepath.Join(first, slug, sid+"-older.jsonl")
	newer := filepath.Join(first, slug, sid+"-newer.jsonl")
	shadowed := filepath.Join(second, slug, sid+".jsonl")
	for _, path := range []string{older, newer, shadowed} {
		writeHistory(t, path, "{}")
	}
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(older, base, base); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newer, base.Add(time.Hour), base.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := FindHistory([]string{empty, first, second}, slug, sid)
	if err != nil || got != newer {
		t.Fatalf("FindHistory = %q, %v; want %q", got, err, newer)
	}
	_, err = FindHistory([]string{empty}, slug, sid)
	if err == nil || !strings.Contains(err.Error(), "no transcript matching sid 'abc'") {
		t.Fatalf("FindHistory with no match error = %v", err)
	}
}

func TestReadHistoryKeepsTheLastRealTurns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeHistory(
		t,
		path,
		`{"type":"user","message":{"content":"dropped as the possibly-partial first line"}}`,
		`{"type":"user","timestamp":"t1","message":{"content":"first"}}`,
		`not json`,
		`{"type":"system","timestamp":"t2","message":{"content":"not a turn"}}`,
		`{"type":"user","timestamp":"t3","message":{"content":"<system-reminder>synthetic</system-reminder>"}}`,
		`{"type":"user","timestamp":"t4","message":{"content":"Caveat: The messages below were generated"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"a"},{"type":"tool_use"},{"type":"text","text":"b"}]}}`,
		`{"type":"user","timestamp":"t6","message":{"content":"last"}}`,
	)
	got, err := ReadHistory(path, 2)
	if err != nil {
		t.Fatal(err)
	}
	want := []HistoryMessage{
		{Timestamp: "?", Role: RoleAssistant, Text: "a\nb"},
		{Timestamp: "t6", Role: RoleUser, Text: "last"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadHistory = %#v, want %#v", got, want)
	}
	if _, err := ReadHistory(filepath.Join(t.TempDir(), "missing.jsonl"), 2); err == nil {
		t.Fatal("ReadHistory of a missing file returned no error")
	}
}

func TestTailLinesMatchesTailN(t *testing.T) {
	for _, testCase := range []struct {
		content string
		count   int
		want    []string
	}{
		{"", 3, nil},
		{"a\nb\nc\n", 2, []string{"b", "c"}},
		{"a\nb", 5, []string{"a", "b"}},
	} {
		if got := tailLines(testCase.content, testCase.count); !reflect.DeepEqual(got, testCase.want) {
			t.Fatalf("tailLines(%q, %d) = %q, want %q", testCase.content, testCase.count, got, testCase.want)
		}
	}
}

// writeHistory writes one JSONL record per line, creating the parent dirs.
func writeHistory(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
