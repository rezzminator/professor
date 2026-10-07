package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/store"
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
			t.Setenv("PFM_STATE_DB", sharedPath)
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
	t.Setenv("PFM_STATE_DB", filepath.Join(root, "shared.db"))
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

// TestResumeUnkillHookLiftsAKillWhenTheThreadIsResumed reproduces the host
// report: `pfm chat kill` wrote a permanent kill for a Claude thread, a pane
// later resumed that thread under its own id, and `pfm chat ls` kept hiding the
// live conversation until a hand-run unkill. Claude's SessionStart hook fires
// with source "resume" for --resume, --continue and the in-app /resume alike,
// so that one event lifts the kill; every other SessionStart leaves it standing.
func TestResumeUnkillHookLiftsAKillWhenTheThreadIsResumed(t *testing.T) {
	const id = "57974ea7-0000-4000-8000-000000000001"
	for _, test := range []struct {
		name       string
		payload    string
		wantKilled bool
	}{
		{
			name:    "resumed killed thread leaves the killed set",
			payload: `{"hook_event_name":"SessionStart","source":"resume","session_id":"` + id + `"}`,
		},
		{
			name:       "fresh startup keeps the kill",
			payload:    `{"hook_event_name":"SessionStart","source":"startup","session_id":"` + id + `"}`,
			wantKilled: true,
		},
		{
			name:       "clear SessionStart keeps the kill",
			payload:    `{"hook_event_name":"SessionStart","source":"clear","session_id":"` + id + `"}`,
			wantKilled: true,
		},
		{
			name:       "compact SessionStart keeps the kill",
			payload:    `{"hook_event_name":"SessionStart","source":"compact","session_id":"` + id + `"}`,
			wantKilled: true,
		},
		{
			name:       "SessionEnd keeps the kill",
			payload:    `{"hook_event_name":"SessionEnd","reason":"resume","session_id":"` + id + `"}`,
			wantKilled: true,
		},
		{
			name:       "resume of another thread keeps the kill",
			payload:    `{"hook_event_name":"SessionStart","source":"resume","session_id":"e125ce10-0000-4000-8000-000000000002"}`,
			wantKilled: true,
		},
		{
			name:       "a session id that is not a UUID keeps the kill",
			payload:    `{"hook_event_name":"SessionStart","source":"resume","session_id":"../` + id + `"}`,
			wantKilled: true,
		},
		{name: "malformed input fails open", payload: `{`, wantKilled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := jailTest(t)
			t.Setenv("PFM_STATE_DB", filepath.Join(root, "shared.db"))
			database, err := store.Open()
			if err != nil {
				t.Fatal(err)
			}
			if err := database.UpsertTranscript(context.Background(), store.Transcript{
				UUID: id, Path: filepath.Join(root, "claude", "project", id+".jsonl"),
				CWD: "/work/example", Size: 1, PromptCount: 3,
			}); err != nil {
				t.Fatal(err)
			}
			if err := database.Kill(context.Background(), store.Killed{
				ID: id, Engine: pfmengine.Claude, KilledAt: 1791050854,
			}); err != nil {
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}

			code, stdout, stderr := runInternalHookPayload(t, "resume-unkill", test.payload)

			database, err = store.Open()
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := database.Close(); err != nil {
					t.Errorf("close database: %v", err)
				}
			}()
			_, found, err := database.Killed(context.Background(), id)
			if err != nil {
				t.Fatal(err)
			}
			if found != test.wantKilled {
				t.Fatalf("killed found=%v after %s (rc=%d stderr=%q), want %v",
					found, test.payload, code, stderr, test.wantKilled)
			}
			killedChats, err := database.KilledChats(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if hidden := len(killedChats) == 1; hidden != test.wantKilled {
				t.Fatalf("ls killed set=%#v, want hidden=%v", killedChats, test.wantKilled)
			}
			if code != 0 || stdout != "" {
				t.Fatalf("resume-unkill rc=%d stdout=%q stderr=%q, want a fail-open 0", code, stdout, stderr)
			}
		})
	}
}

func runClearKillPayload(t *testing.T, payload string) (int, string, string) {
	t.Helper()
	return runInternalHookPayload(t, "clear-kill", payload)
}

func runInternalHookPayload(t *testing.T, entry, payload string) (int, string, string) {
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
	code := run([]string{"internal", entry}, &stdout, &stderr)
	return code, stdout.String(), strings.TrimSpace(stderr.String())
}
