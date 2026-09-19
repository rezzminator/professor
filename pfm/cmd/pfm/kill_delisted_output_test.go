package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"hostops/pfm/internal/store"
)

// A kill that only wrote a tombstone must SAY it only wrote a tombstone.
//
// `pfm chat kill <id>` for a target the fleet holds no live row for ends in
// runKill, which answered a flat "killed <id>" — the same words a kill that
// closed a running pane answers. An MCP caller reading that line over the
// dispatcher (mcpserv.cliAction) had nothing else to read: it reported "ok"
// for a chat whose TUI was still running, because the row it resolved was the
// resumable twin of a live seat the scan had not recognised. The pane-closing
// form already names its mechanism (runResolvedChatKill); this is its twin.
func TestChatKillOfAResumableRowSaysItOnlyDeListed(t *testing.T) {
	root := jailTest(t)
	const id = "44444444-4444-4444-8444-444444444444"
	transcriptPath := filepath.Join(root, "claude", "project", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		transcriptPath,
		[]byte(`{"type":"user","cwd":"/work/example","message":{"content":"de-list fixture"}}`+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertTranscript(context.Background(), store.Transcript{
		UUID: id, Path: transcriptPath, PromptCount: 3,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "kill", id}, &stdout, &stderr); code != 0 {
		t.Fatalf("kill code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	want := "killed " + id + "\tde-listed only, no live pane closed\n"
	if stdout.String() != want {
		t.Fatalf("kill stdout=%q, want %q", stdout.String(), want)
	}
}
