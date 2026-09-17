package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestJailedCodexLineageCollapseKillSiblingStaysKilled(t *testing.T) {
	root := jailTest(t)
	writeJailedCodexAuth(t, root)
	const (
		rootID   = "11111111-1111-4111-8111-111111111111"
		childOne = "22222222-2222-4222-8222-222222222222"
		childTwo = "33333333-3333-4333-8333-333333333333"
		childNew = "44444444-4444-4444-8444-444444444444"
	)
	project := filepath.Join(root, "work", "web")
	sessionDir := filepath.Join(
		root,
		"codex",
		"sessions",
		"2026",
		"07",
		"27",
	)
	for _, directory := range []string{project, sessionDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	indexLine, err := json.Marshal(map[string]any{
		"id":          rootID,
		"thread_name": "WEB",
	})
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

	basePrompts := []string{"one", "two", "three"}
	writeLineageRollout(
		t, sessionDir, rootID, rootID, "", project, basePrompts, 100,
	)
	writeLineageRollout(
		t, sessionDir, childOne, rootID, rootID, project, basePrompts, 200,
	)
	writeLineageRollout(
		t, sessionDir, childTwo, rootID, childOne, project, basePrompts, 300,
	)

	runLineageCLI(t, "index", "--full")
	rows := runLineageCLI(t, "ls", "--tsv")
	assertOneWEBLineage(t, rows, rootID, 3)

	killOutput := runLineageCLI(t, "chat", "kill", childTwo)
	if killOutput != "killed "+rootID+"\n" {
		t.Fatalf("kill child output = %q", killOutput)
	}
	if rows := runLineageCLI(t, "ls", "--tsv"); strings.Contains(rows, "\tWEB\t") {
		t.Fatalf("killed lineage returned in default rows:\n%s", rows)
	}

	newestPath := writeLineageRollout(
		t, sessionDir, childNew, rootID, childTwo, project, basePrompts, 400,
	)
	runLineageCLI(t, "index")
	if rows := runLineageCLI(t, "ls", "--tsv"); strings.Contains(rows, "\tWEB\t") {
		t.Fatalf("new sibling unhid lineage:\n%s", rows)
	}
	killed := runLineageCLI(t, "ls", "--killed")
	if !strings.HasPrefix(killed, rootID+"\tcx\t") ||
		strings.Count(killed, "\t") != 2 {
		t.Fatalf("lineage kill = %q, want id/engine/killed_at only", killed)
	}

	// A real new prompt is still no escape: only unkill lifts a kill.
	appendLineagePrompt(t, newestPath, "four")
	runLineageCLI(t, "index")
	if rows := runLineageCLI(t, "ls", "--tsv"); strings.Contains(rows, "\tWEB\t") {
		t.Fatalf("a real new prompt unhid the lineage:\n%s", rows)
	}
	if killed := runLineageCLI(t, "ls", "--killed"); !strings.HasPrefix(killed, rootID+"\tcx\t") {
		t.Fatalf("lineage kill vanished: %q", killed)
	}

	runLineageCLI(t, "chat", "unkill", rootID)
	rows = runLineageCLI(t, "ls", "--tsv")
	assertOneWEBLineage(t, rows, rootID, 4)
	if killed := runLineageCLI(t, "ls", "--killed"); killed != "" {
		t.Fatalf("unkill left a kill behind: %q", killed)
	}
}

func writeLineageRollout(
	t *testing.T,
	directory, id, sessionID, parent, cwd string,
	prompts []string,
	mtime int64,
) string {
	t.Helper()
	path := filepath.Join(
		directory,
		"rollout-2026-07-27T00-00-"+fmt.Sprintf("%02d", mtime/100)+"-"+id+".jsonl",
	)
	records := []any{map[string]any{
		"type":          "session_meta",
		"thread_source": "user",
		"payload": map[string]any{
			"id":               id,
			"cwd":              cwd,
			"session_id":       sessionID,
			"parent_thread_id": parent,
			"thread_source":    "user",
		},
	}}
	for _, prompt := range prompts {
		records = append(records, map[string]any{
			"type": "response_item",
			"payload": map[string]any{
				"type": "message",
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": prompt},
				},
			},
		})
	}
	var content bytes.Buffer
	encoder := json.NewEncoder(&content)
	for _, record := range records {
		if err := encoder.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, content.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(mtime, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	return path
}

func appendLineagePrompt(t *testing.T, path, prompt string) {
	t.Helper()
	record, err := json.Marshal(map[string]any{
		"type": "response_item",
		"payload": map[string]any{
			"type": "message",
			"role": "user",
			"content": []map[string]any{
				{"type": "input_text", "text": prompt},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(append(record, '\n')); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	stamp := time.Unix(500, 0)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

func runLineageCLI(t *testing.T, arguments ...string) string {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run(arguments, &stdout, &stderr); code != 0 {
		t.Fatalf(
			"run(%q) code=%d stdout=%q stderr=%q",
			arguments,
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	if stderr.Len() != 0 {
		t.Fatalf("run(%q) stderr=%q", arguments, stderr.String())
	}
	return stdout.String()
}

func assertOneWEBLineage(
	t *testing.T,
	rows, rootID string,
	prompts int64,
) {
	t.Helper()
	matches := make([]string, 0, 1)
	for _, line := range strings.Split(rows, "\n") {
		if strings.Contains(line, "\tWEB\t") {
			matches = append(matches, line)
		}
	}
	if len(matches) != 1 {
		t.Fatalf("WEB lineage rows = %d:\n%s", len(matches), rows)
	}
	fields := strings.Split(matches[0], "\t")
	if len(fields) < 6 ||
		fields[1] != rootID ||
		fields[4] != "WEB" ||
		fields[5] != strconv.FormatInt(prompts, 10) {
		t.Fatalf("WEB lineage row = %q", matches[0])
	}
}
