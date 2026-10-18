package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTranscriptCLIEntry(t *testing.T) {
	jailTest(t)
	path := filepath.Join(t.TempDir(), "session.jsonl")
	record := `{"type":"user","sessionId":"cli-entry","timestamp":"2026-09-29T20:00:00Z",` +
		`"message":{"content":"cli entry prompt"}}` + "\n"
	if err := os.WriteFile(path, []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errs bytes.Buffer
	if code := run([]string{"transcript", "--help"}, &out, &errs); code != 0 ||
		!strings.Contains(out.String(), "{show,locate,agents}") {
		t.Fatalf("transcript --help code=%d stdout=%q stderr=%q", code, out.String(), errs.String())
	}
	out.Reset()
	if code := run([]string{"transcript", "show", path}, &out, &errs); code != 0 ||
		!strings.HasPrefix(out.String(), "TRANSCRIPT claude · cli-entry") ||
		!strings.Contains(out.String(), "PROMPT cli entry prompt") {
		t.Fatalf("transcript show code=%d stdout=%q stderr=%q", code, out.String(), errs.String())
	}
	out.Reset()
	if code := run([]string{"transcript", "locate", "no-such-session-anywhere"}, &out, &errs); code != 1 ||
		!strings.Contains(errs.String(), "TRANSCRIPT FAILED — NOT FOUND no-such-session-anywhere") {
		t.Fatalf("transcript locate of nothing code=%d stderr=%q", code, errs.String())
	}
	errs.Reset()
	if code := run([]string{"transcript", "list", "x"}, &out, &errs); code != 2 ||
		!strings.Contains(errs.String(), "argument verb: invalid choice: 'list'") {
		t.Fatalf("transcript list code=%d stderr=%q", code, errs.String())
	}
	out.Reset()
	if code := run([]string{"help"}, &out, &errs); code != 0 || !strings.Contains(out.String(), "  transcript ") {
		t.Fatalf("help code=%d lacks transcript: %s", code, out.String())
	}
}
