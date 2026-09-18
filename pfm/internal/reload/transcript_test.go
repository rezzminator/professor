package reload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTranscriptAppendBacksUpThenAppendsOneSeparatedLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	// No trailing newline on purpose: the append must separate itself.
	if err := os.WriteFile(path, []byte(`{"type":"user","sessionId":"s"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	transcript, err := OpenTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(transcript.Raw()) != `{"type":"user","sessionId":"s"}` {
		t.Fatalf("Raw() = %q, want the whole file as read under the lock", transcript.Raw())
	}
	backup := filepath.Join(dir, "backups", "s-1.jsonl")
	if err := transcript.Append(
		[]byte(`{"type":"custom-title","customTitle":"x","sessionId":"s"}`),
		backup,
	); err != nil {
		t.Fatal(err)
	}
	if err := transcript.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"user","sessionId":"s"}` + "\n" + `{"type":"custom-title","customTitle":"x","sessionId":"s"}` + "\n"
	if string(got) != want {
		t.Fatalf("transcript after append = %q, want %q", got, want)
	}
	copied, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("backup was not written: %v", err)
	}
	if string(copied) != `{"type":"user","sessionId":"s"}` {
		t.Fatalf("backup = %q, want the pre-append content byte for byte", copied)
	}
}

func TestTranscriptAppendNeverOverwritesAnExistingBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "taken.jsonl")
	if err := os.WriteFile(backup, []byte("somebody else's copy\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	transcript, err := OpenTranscript(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := transcript.Close(); err != nil {
			t.Error(err)
		}
	}()
	err = transcript.Append([]byte(`{"type":"x"}`), backup)
	if err == nil || !strings.Contains(err.Error(), "create transcript backup") {
		t.Fatalf("Append over an existing backup = %v, want a refusal naming the backup", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != "{}\n" {
		t.Fatalf("transcript changed although the backup was refused: %q (%v)", got, readErr)
	}
}

func TestOpenTranscriptNamesAMissingFile(t *testing.T) {
	_, err := OpenTranscript(filepath.Join(t.TempDir(), "missing.jsonl"))
	if err == nil || !strings.Contains(err.Error(), "missing.jsonl") {
		t.Fatalf("OpenTranscript(missing) = %v, want an error naming the path", err)
	}
}
