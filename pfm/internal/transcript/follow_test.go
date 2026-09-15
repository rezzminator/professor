package transcript

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFromReadsOnlyWhatWasAppended(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	write(t, path, claudeUser("first"), claudeAssistant("one"))

	entries, offset, err := From(context.Background(), path, "cc", 0)
	if err != nil {
		t.Fatalf("From() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2: %+v", len(entries), entries)
	}
	if offset == 0 {
		t.Fatal("offset did not advance")
	}

	appendLines(t, path, claudeUser("second"), claudeAssistant("two"))
	entries, next, err := From(context.Background(), path, "cc", offset)
	if err != nil {
		t.Fatalf("From() error = %v", err)
	}
	if len(entries) != 2 || entries[0].Text != "second" || entries[1].Text != "two" {
		t.Fatalf("second read = %+v, want only the appended pair", entries)
	}
	if next <= offset {
		t.Fatalf("offset %d did not advance past %d", next, offset)
	}

	entries, last, err := From(context.Background(), path, "cc", next)
	if err != nil || len(entries) != 0 {
		t.Fatalf("quiet read = %+v (err=%v), want nothing", entries, err)
	}
	if last != next {
		t.Fatalf("quiet read moved the offset %d -> %d", next, last)
	}
}

// TestFromHoldsAPartialLine is the poll-lands-mid-write case: half a record is
// not a record, and consuming it would drop the whole turn — the caller would
// then wait forever for an answer that was already on disk.
func TestFromHoldsAPartialLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	write(t, path, claudeUser("whole"))
	partial := claudeAssistant("half")
	appendRaw(t, path, partial[:len(partial)/2])

	entries, offset, err := From(context.Background(), path, "cc", 0)
	if err != nil {
		t.Fatalf("From() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Text != "whole" {
		t.Fatalf("entries = %+v, want only the complete record", entries)
	}

	appendRaw(t, path, partial[len(partial)/2:])
	entries, _, err = From(context.Background(), path, "cc", offset)
	if err != nil {
		t.Fatalf("From() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Text != "half" {
		t.Fatalf("completed read = %+v, want the record that finished", entries)
	}
}

func TestFromRestartsWhenTheFileShrinks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	write(t, path, claudeUser("old one"), claudeUser("old two"))
	_, offset, err := From(context.Background(), path, "cc", 0)
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, claudeUser("rewritten"))

	entries, _, err := From(context.Background(), path, "cc", offset)
	if err != nil {
		t.Fatalf("From() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Text != "rewritten" {
		t.Fatalf("entries = %+v, want the rewritten file read whole", entries)
	}
}

func TestSizeOfAChatThatHasNotSpoken(t *testing.T) {
	size, err := Size(filepath.Join(t.TempDir(), "absent.jsonl"))
	if err != nil || size != 0 {
		t.Fatalf("Size(absent) = %d, %v, want 0, nil", size, err)
	}
	if size, err := Size(""); err != nil || size != 0 {
		t.Fatalf("Size(\"\") = %d, %v, want 0, nil", size, err)
	}
}

func claudeUser(text string) string {
	return `{"type":"user","message":{"content":"` + text + `"}}` + "\n"
}

func claudeAssistant(text string) string {
	return `{"type":"assistant","message":{"content":[{"type":"text","text":"` +
		text + `"}]}}` + "\n"
}

func write(t *testing.T, path string, lines ...string) {
	t.Helper()
	body := ""
	for _, line := range lines {
		body += line
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	for _, line := range lines {
		appendRaw(t, path, line)
	}
}

func appendRaw(t *testing.T, path, chunk string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("close file: %v", err)
		}
	}()
	if _, err := file.WriteString(chunk); err != nil {
		t.Fatal(err)
	}
}
