package codexappendix

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHookLifecycleAndVisibleUnknownHistory(t *testing.T) {
	home := t.TempDir()
	prompt := PromptPath(home)
	if err := os.MkdirAll(filepath.Dir(prompt), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(prompt, []byte("Current appendix"), 0o600); err != nil {
		t.Fatal(err)
	}
	body := marker + "\n\nCurrent appendix"
	message := func(role, text string) string {
		raw, _ := json.Marshal(
			map[string]any{
				"type":    "message",
				"role":    role,
				"content": []any{map[string]any{"type": "input_text", "text": text}},
			},
		)
		return string(raw)
	}
	item := func(m string) string { return `{"type":"response_item","payload":` + m + "}\n" }
	meta := "{\"type\":\"session_meta\",\"payload\":{}}\n"
	checkpoint := func(items string) string {
		return `{"type":"compacted","payload":{"replacement_history":[` + items + "]}}\n"
	}
	tests := []struct {
		name, source, history string
		inject, warn          bool
	}{
		{"fresh", "startup", meta, true, false},
		{"same version resume", "resume", meta + item(message("developer", body)), false, false},
		{"copied root fork", "startup", meta + item(message("developer", body)), false, false},
		{"compact retains", "compact", meta + checkpoint(message("developer", body)), false, false},
		{"compact discards", "compact", meta + item(message("developer", body)) + checkpoint(""), true, false},
		{"user quotation", "resume", meta + item(message("user", body)), true, false},
		{"changed version", "resume", meta + item(message("developer", marker+"\n\nOld appendix")), true, true},
		{
			"rollback unknown",
			"resume",
			meta + item(
				message("developer", body),
			) + "{\"type\":\"event_msg\",\"payload\":{\"type\":\"thread_rolled_back\",\"num_turns\":1}}\n",
			true,
			true,
		},
		{
			"referenced fork unknown",
			"startup",
			"{\"type\":\"session_meta\",\"payload\":{\"history_base\":{\"thread_id\":\"ancestor\"}}}\n",
			true,
			true,
		},
		{"legacy compact", "compact", meta + "{\"type\":\"compacted\",\"payload\":{}}\n", true, true},
		{"malformed", "resume", meta + "garbage\n", true, true},
		{"null transcript", "startup", "", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var transcript any
			if tt.history != "" {
				path := filepath.Join(home, "rollout.jsonl")
				if err := os.WriteFile(path, []byte(tt.history), 0o600); err != nil {
					t.Fatal(err)
				}
				transcript = path
			}
			request, _ := json.Marshal(
				map[string]any{"hook_event_name": "SessionStart", "source": tt.source, "transcript_path": transcript},
			)
			var output bytes.Buffer
			if err := Run(bytes.NewReader(request), &output, home); err != nil {
				t.Fatal(err)
			}
			var response struct {
				Specific *struct {
					Context string `json:"additionalContext"`
				} `json:"hookSpecificOutput"`
				Warning string `json:"systemMessage"`
			}
			if err := json.Unmarshal(output.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if (response.Specific != nil) != tt.inject || (response.Warning != "") != tt.warn {
				t.Fatalf("response=%s", output.String())
			}
			if tt.inject && response.Specific.Context != body {
				t.Fatalf("incorrect developer context: %s", output.String())
			}
		})
	}
}

func TestHistoryBudgetAndCheckpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	padding := strings.Repeat("x", historyLimit+1)
	if err := os.WriteFile(path, []byte(padding), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := presentInHistory(&path, "appendix"); err == nil {
		t.Fatal("unbounded history reported absent")
	}
	if err := os.WriteFile(
		path,
		[]byte(padding+"\n{\"type\":\"compacted\",\"payload\":{\"replacement_history\":[]}}\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if present, _, err := presentInHistory(&path, "appendix"); err != nil || present {
		t.Fatalf("checkpoint present=%v err=%v", present, err)
	}
}

func TestRegistrationCancellationKillsNonExecLauncherChildren(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "native")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n/bin/sleep 10\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := rpc(ctx, binary, dir, "hooks/list", nil)
	if err == nil || time.Since(start) > time.Second {
		t.Fatalf("unbounded helper: elapsed=%s err=%v", time.Since(start), err)
	}
}
