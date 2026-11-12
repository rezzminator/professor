package transcript

import (
	"context"
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

func TestFindCodexHistoryTakesTheNewestRolloutOfTheThread(t *testing.T) {
	const thread = "71717171-7171-4171-8171-717171717171"
	first, second := t.TempDir(), t.TempDir()
	older := filepath.Join(first, "sessions", "2026", "01", "02", "rollout-2026-01-02T03-04-05-"+thread+".jsonl")
	newer := filepath.Join(second, "archived_sessions", "rollout-2026-01-03T03-04-05-"+thread+".jsonl")
	other := filepath.Join(first, "sessions", "2026", "01", "02",
		"rollout-2026-01-04T03-04-05-72727272-7272-4272-8272-727272727272.jsonl")
	for _, path := range []string{older, newer, other} {
		writeHistory(t, path, "{}")
	}
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for path, at := range map[string]time.Time{older: base, newer: base.Add(time.Hour), other: base.Add(2 * time.Hour)} {
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		name, sid string
		homes     []string
		want      string
	}{
		{"the newest rollout across homes", thread, []string{first, second}, newer},
		{"a thread id prefix", "7171", []string{first, second}, newer},
		{"only the home that holds it", thread, []string{first}, older},
		{"no rollout of the thread", "73737373", []string{first, second}, ""},
		{"no Codex home", thread, nil, ""},
		{"an empty sid matches nothing", "", []string{first}, ""},
	} {
		got, err := FindCodexHistory(context.Background(), row.homes, row.sid)
		if err != nil || got != row.want {
			t.Errorf("%s: FindCodexHistory(%v, %q) = %q, %v; want %q", row.name, row.homes, row.sid, got, err, row.want)
		}
	}
}

func TestReadHistoryKeepsTheLastRealTurns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeHistory(
		t,
		path,
		`{"type":"user","timestamp":"t0","message":{"content":"the first line of a short file"}}`,
		`{"type":"user","timestamp":"t1","message":{"content":"first"}}`,
		`not json`,
		`{"type":"system","timestamp":"t2","message":{"content":"not a turn"}}`,
		`{"type":"user","timestamp":"t3","message":{"content":"<system-reminder>synthetic</system-reminder>"}}`,
		`{"type":"user","timestamp":"t3b","message":{"content":[{"type":"text","text":"<system-reminder>block</system-reminder>"},{"type":"text","text":"typed after the reminder block"}]}}`,
		`{"type":"user","timestamp":"t3c","message":{"content":"<system-reminder>lead</system-reminder>\ntyped after the reminder string"}}`,
		`{"type":"user","timestamp":"t4","message":{"content":"Caveat: The messages below were generated"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"a"},{"type":"tool_use"},{"type":"text","text":"b"}]}}`,
		`{"type":"user","timestamp":1730000000,"message":{"content":"numeric stamp"}}`,
		`{"type":"user","timestamp":"t6","message":{"content":"last"}}`,
	)
	got, err := ReadHistory(path, 20)
	if err != nil {
		t.Fatal(err)
	}
	want := []HistoryMessage{
		{Timestamp: "t0", Role: RoleUser, Text: "the first line of a short file"},
		{Timestamp: "t1", Role: RoleUser, Text: "first"},
		{Timestamp: "?", Role: historyUnparsedRole, Text: "row skipped: not JSON (8 bytes)"},
		{Timestamp: "t3b", Role: RoleUser, Text: "typed after the reminder block"},
		{Timestamp: "t3c", Role: RoleUser, Text: "typed after the reminder string"},
		{Timestamp: "?", Role: RoleAssistant, Text: "a\nb"},
		{Timestamp: "?", Role: RoleUser, Text: "numeric stamp"},
		{Timestamp: "t6", Role: RoleUser, Text: "last"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ReadHistory(%s) = %#v, want %#v", path, got, want)
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
