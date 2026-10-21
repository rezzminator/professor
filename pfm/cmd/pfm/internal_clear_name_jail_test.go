package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestClearSuccessorGetsOwnNameBeforeFirstPrompt(t *testing.T) {
	for _, original := range []string{"Research Desk", ""} {
		t.Run("name="+original, func(t *testing.T) {
			root := testjail.Fleet(t)
			t.Setenv("TMUX", "")
			t.Setenv("HERDR_ENV", "")
			const id = "22222222-2222-4222-8222-222222222222"
			path := filepath.Join(root, "claude", id+".jsonl")
			body := `{"type":"custom-title","customTitle":"` + original + `"}` + "\n"
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			payload, err := json.Marshal(map[string]string{
				"hook_event_name": "SessionStart", "source": "clear", "session_id": id,
				"session_title": original, "transcript_path": path, "cwd": "/work/example",
			})
			if err != nil {
				t.Fatal(err)
			}
			code, out, stderr := runInternalHookPayload(t, "clear-name", string(payload))
			if code != 0 || out != "" || stderr != "" {
				t.Fatalf("clear start = %d stdout=%q stderr=%q", code, out, stderr)
			}
			db, err := store.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			got, found, err := db.Transcript(context.Background(), id)
			want := ""
			if original != "" {
				want = original + " #2"
			}
			if err != nil || (original != "" && (!found || got.CustomTitle != want)) ||
				(original == "" && got.CustomTitle != "") {
				t.Fatalf("successor name=%q found=%v err=%v, want %q", got.CustomTitle, found, err, want)
			}
			before, err := os.ReadFile(path)
			if err != nil || string(before) != body {
				t.Fatalf("start rewrote transcript: %q err=%v", before, err)
			}
			prompt := `{"hook_event_name":"UserPromptSubmit","session_id":"` + id + `","transcript_path":"` + path + `"}`
			code, out, stderr = runInternalHookPayload(t, "clear-name", prompt)
			if code != 0 || stderr != "" {
				t.Fatalf("first prompt = %d stdout=%q stderr=%q", code, out, stderr)
			}
			if original == "" {
				if out != "" {
					t.Fatalf("unnamed clear invented title: %s", out)
				}
				return
			}
			var output struct {
				Hook struct {
					Event string `json:"hookEventName"`
					Title string `json:"sessionTitle"`
				} `json:"hookSpecificOutput"`
			}
			if err := json.Unmarshal(
				[]byte(out),
				&output,
			); err != nil || output.Hook.Title != want ||
				output.Hook.Event != "UserPromptSubmit" {
				t.Fatalf("first prompt title=%s err=%v, want %q", out, err, want)
			}
			_, again, stderr := runInternalHookPayload(t, "clear-name", prompt)
			if again != "" || stderr != "" {
				t.Fatalf("second prompt retitled: %s stderr=%q", again, stderr)
			}
			// A delta index may still see Claude's inherited title before the hook output lands.
			if err := db.UpsertTranscript(
				context.Background(),
				store.Transcript{UUID: id, Path: path, CustomTitle: original},
			); err != nil {
				t.Fatal(err)
			}
			got, _, err = db.Transcript(context.Background(), id)
			if err != nil || got.CustomTitle != want || strings.EqualFold(got.CustomTitle, original) {
				t.Fatalf("double-name state returned after index: %+v err=%v", got, err)
			}
		})
	}
}
