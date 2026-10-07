package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/store"
)

func TestChatUnkillOfAChatNotKilledExitsOne(t *testing.T) {
	jailTest(t)
	const id = "88888888-8888-4888-8888-888888888888"
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertTranscript(context.Background(), store.Transcript{
		UUID: id, Path: "/jailed/" + id + ".jsonl",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"chat", "unkill", id}, &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), id+" is not killed; nothing was unkilled") {
		t.Fatalf("unkill code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestChatUnkillByNameClearsTheKilledResumeRow(t *testing.T) {
	root := jailTest(t)
	writeJailedCodexAuth(t, root)
	const (
		id   = "77777777-7777-4777-8777-777777777777"
		name = "NAMED_KILLED_CHAT"
	)
	project := filepath.Join(root, "work", "project")
	sessionDir := filepath.Join(root, "codex", "sessions", "2026", "07", "27")
	for _, dir := range []string{project, sessionDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	indexLine, err := json.Marshal(map[string]string{"id": id, "thread_name": name})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "codex", "session_index.jsonl"),
		append(indexLine, '\n'),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	writeLineageRollout(t, sessionDir, id, id, "", project, []string{"one"}, 100)
	runLineageCLI(t, "index", "--full")
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := database.Kill(ctx, store.Killed{ID: id, Engine: "cx", KilledAt: 100}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "unkill", name}, &stdout, &stderr); code != 0 {
		t.Fatalf("unkill by name code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if stdout.String() != "unkilled "+id+"\n" || stderr.Len() != 0 {
		t.Fatalf("unkill by name stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"ls", "-a", "--tsv"}, &stdout, &stderr); code != 0 {
		t.Fatalf("ls -a code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "\t"+name+"\t") || strings.Contains(stdout.String(), "\ttrue\t") {
		t.Fatalf("unkilled resume row = %q", stdout.String())
	}
}
