package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/store"
)

func TestClaudeClearKillHookOwnsOnlySessionEndClear(t *testing.T) {
	for _, test := range []struct {
		name     string
		payload  string
		fleet    bool
		wantKill bool
	}{
		{
			name:     "clear fleet chat",
			payload:  `{"hook_event_name":"SessionEnd","reason":"clear","session_id":"11111111-1111-4111-8111-111111111111"}`,
			fleet:    true,
			wantKill: true,
		},
		{
			name:    "exit stays untouched",
			payload: `{"hook_event_name":"SessionEnd","reason":"prompt_input_exit","session_id":"11111111-1111-4111-8111-111111111111"}`,
			fleet:   true,
		},
		{
			name:    "other reason stays untouched",
			payload: `{"hook_event_name":"SessionEnd","reason":"logout","session_id":"11111111-1111-4111-8111-111111111111"}`,
			fleet:   true,
		},
		{
			name:    "clear SessionStart stays untouched",
			payload: `{"hook_event_name":"SessionStart","source":"clear","session_id":"11111111-1111-4111-8111-111111111111"}`,
			fleet:   true,
		},
		{
			name:    "bare non-fleet clear stays untouched",
			payload: `{"hook_event_name":"SessionEnd","reason":"clear","session_id":"11111111-1111-4111-8111-111111111111"}`,
		},
		{name: "malformed input fails open", payload: `{`},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := jailTest(t)
			sharedPath := filepath.Join(root, "shared.db")
			t.Setenv("PFM_SHARED_DB", sharedPath)
			t.Setenv("TMUX", "")
			t.Setenv("TMUX_PANE", "")
			t.Setenv("CODEX_THREAD_ID", "")
			id := "11111111-1111-4111-8111-111111111111"
			transcriptPath := filepath.Join(root, "claude", "project", id+".jsonl")
			if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o700); err != nil {
				t.Fatal(err)
			}
			body := `{"type":"user","cwd":"/work/example","message":{"content":"first"}}` + "\n" +
				`{"type":"user","cwd":"/work/example","message":{"content":"second"}}` + "\n"
			if err := os.WriteFile(transcriptPath, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if test.fleet {
				database, err := store.Open()
				if err != nil {
					t.Fatal(err)
				}
				if err := database.UpsertTranscript(context.Background(), store.Transcript{
					UUID: id, Path: transcriptPath, CWD: "/work/example", Size: 1,
					PromptCount: 1,
				}); err != nil {
					t.Fatal(err)
				}
				if err := database.Close(); err != nil {
					t.Fatal(err)
				}
			}

			code, stdout, stderr := runClearKillPayload(t, test.payload)
			if code != 0 || stdout != "" {
				t.Fatalf("clear-kill rc=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			database, err := store.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := database.Close(); err != nil {
					t.Errorf("close database: %v", err)
				}
			}()
			killed, found, err := database.Killed(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if found != test.wantKill {
				t.Fatalf("killed found=%v row=%#v stderr=%q, want %v", found, killed, stderr, test.wantKill)
			}
			if test.wantKill && (killed.BaselinePrompts == nil || *killed.BaselinePrompts != 2) {
				t.Fatalf("clear baseline = %#v, want refreshed prompt count 2", killed.BaselinePrompts)
			}
		})
	}
}

func TestClearKillHookDoubleFireIsIdempotent(t *testing.T) {
	root := jailTest(t)
	t.Setenv("PFM_SHARED_DB", filepath.Join(root, "shared.db"))
	id := "22222222-2222-4222-8222-222222222222"
	transcriptPath := filepath.Join(root, "claude", "project", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcriptPath, []byte(
		`{"type":"user","cwd":"/work/example","message":{"content":"one"}}`+"\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertTranscript(context.Background(), store.Transcript{
		UUID: id, Path: transcriptPath, CWD: "/work/example", Size: 1, PromptCount: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	payload := `{"hook_event_name":"SessionEnd","reason":"clear","session_id":"` + id + `"}`
	for fire := 0; fire < 2; fire++ {
		code, stdout, stderr := runClearKillPayload(t, payload)
		if code != 0 || stdout != "" || stderr != "" {
			t.Fatalf("fire %d rc=%d stdout=%q stderr=%q", fire+1, code, stdout, stderr)
		}
	}
	database, err = store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	killed, err := database.KilledChats(context.Background())
	if err != nil || len(killed) != 1 || killed[0].ID != id ||
		killed[0].BaselinePrompts == nil || *killed[0].BaselinePrompts != 1 {
		t.Fatalf("double-fire killed=%#v error=%v", killed, err)
	}
}

func runClearKillPayload(t *testing.T, payload string) (int, string, string) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	previous := os.Stdin
	os.Stdin = reader
	defer func() {
		os.Stdin = previous
		_ = reader.Close()
	}()
	var stdout, stderr bytes.Buffer
	code := run([]string{"internal", "clear-kill"}, &stdout, &stderr)
	return code, stdout.String(), strings.TrimSpace(stderr.String())
}
