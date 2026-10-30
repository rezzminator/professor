package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// The CLAUDE_RULES report: a named chat cleared twice, then exited, stayed in
// the picker, because clear-kill skipped every named session (#37's first
// fix). The /clear successor now carries its own `{base} #N` name, so the
// named conversation is hidden like any other: its kill keeps a prompt
// baseline, and the hook never rewrites an engine-owned transcript.
func TestClearKillHidesANamedConversation(t *testing.T) {
	for _, title := range []struct {
		name   string
		record string
		cwd    string
	}{
		{"custom title", `{"type":"custom-title","customTitle":"Research Desk"}`, "/work/example"},
		{"agent name", `{"type":"agent-name","agentName":"Research Desk"}`, "/work/example"},
		{"missing cached cwd", `{"type":"custom-title","customTitle":"Research Desk"}`, ""},
		{"dot cached cwd", `{"type":"custom-title","customTitle":"Research Desk"}`, "."},
	} {
		t.Run(title.name, func(t *testing.T) {
			root := testjail.Fleet(t)
			t.Setenv("PFM_STATE_DB", filepath.Join(root, "shared.db"))
			ctx := context.Background()
			database, err := store.Open()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := database.Close(); err != nil {
					t.Errorf("close database: %v", err)
				}
			})
			const original = "11111111-1111-4111-8111-111111111111"
			const successor = "22222222-2222-4222-8222-222222222222"
			contents := make(map[string]string)
			for _, id := range []string{original, successor} {
				body := title.record + "\n"
				if id == original {
					body += `{"type":"user","cwd":"/work/example","message":{"content":"Research this topic"}}` + "\n"
				} else {
					body += `{"type":"user","cwd":"/work/example","message":{"content":"<command-name>/clear</command-name>"}}` + "\n"
				}
				path := filepath.Join(root, "claude", id+".jsonl")
				contents[path] = body
				if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
				// Seed a stale, unnamed index entry: /rename may have just landed.
				if err := database.UpsertTranscript(ctx, store.Transcript{
					UUID: id, Path: path, CWD: title.cwd, Size: 1,
				}); err != nil {
					t.Fatal(err)
				}
			}
			payload := `{"hook_event_name":"SessionEnd","reason":"clear","session_id":"` + original + `"}`
			if code, stdout, stderr := runClearKillPayload(t, payload); code != 0 || stdout != "" || stderr != "" {
				t.Fatalf("ClearKill() = %d, stdout=%q stderr=%q", code, stdout, stderr)
			}
			for path, want := range contents {
				got, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if string(got) != want {
					t.Fatalf("/clear hook rewrote engine-owned transcript %s: got %q, want %q", path, got, want)
				}
			}
			transcripts, err := database.Transcripts(ctx)
			if err != nil {
				t.Fatal(err)
			}
			killed, err := database.KilledChats(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(killed) != 1 || killed[0].ID != original ||
				killed[0].BaselinePrompts == nil || *killed[0].BaselinePrompts != 1 {
				t.Fatalf("named conversation after /clear: killed=%#v, want %s hidden at baseline 1", killed, original)
			}
			for _, row := range compose.Compose(compose.Input{Transcripts: transcripts, Killed: killed}).Rows {
				if row.ID == original && !row.Killed {
					t.Fatalf("named conversation still listed after /clear: row=%#v", row)
				}
			}
		})
	}
}

func TestClaudeClearKillHookOwnsOnlySessionEndClear(t *testing.T) {
	for _, test := range []struct {
		name     string
		payload  string
		fleet    bool
		records  string
		wantKill bool
	}{
		{
			name:     "clear fleet chat",
			payload:  `{"hook_event_name":"SessionEnd","reason":"clear","session_id":"11111111-1111-4111-8111-111111111111"}`,
			fleet:    true,
			wantKill: true,
		},
		{
			name:     "an AI title is not a chosen name",
			payload:  `{"hook_event_name":"SessionEnd","reason":"clear","session_id":"11111111-1111-4111-8111-111111111111"}`,
			fleet:    true,
			records:  `{"type":"ai-title","aiTitle":"Automatic topic"}` + "\n",
			wantKill: true,
		},
		{
			name:     "empty title records are not a chosen name",
			payload:  `{"hook_event_name":"SessionEnd","reason":"clear","session_id":"11111111-1111-4111-8111-111111111111"}`,
			fleet:    true,
			records:  `{"type":"custom-title","customTitle":""}` + "\n" + `{"type":"agent-name","agentName":""}` + "\n",
			wantKill: true,
		},
		{
			name:     "a named chat's clear hides it",
			payload:  `{"hook_event_name":"SessionEnd","reason":"clear","session_id":"11111111-1111-4111-8111-111111111111"}`,
			fleet:    true,
			records:  `{"type":"custom-title","customTitle":"CLAUDE_RULES"}` + "\n",
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
			body := test.records + `{"type":"user","cwd":"/work/example","message":{"content":"first"}}` + "\n" +
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

// The CLAUDE_RULES report end to end: a named mock Claude seat on a real tmux
// server takes a prompt and plays /clear, with pfm's own clear-kill and
// clear-name hooks installed in its settings and fired by the mock engine at
// SessionEnd(clear) and SessionStart(clear). The fleet scan then hides the
// pre-clear conversation and lists the successor as `{name} #2`.
func TestClearHidesTheNamedConversationAndListsItsNumberedSuccessor(t *testing.T) {
	const name = "CLAUDE_RULES"
	ladder := newWatchLadder(t)
	entry := func(command string) []any {
		return []any{map[string]any{
			"matcher": "", "hooks": []any{map[string]any{"type": "command", "command": command}},
		}}
	}
	settings, err := json.Marshal(map[string]any{"hooks": map[string]any{
		"SessionEnd":   entry(testPFMBinary + " internal clear-kill"),
		"SessionStart": entry(testPFMBinary + " internal clear-name"),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ladder.configDir, "settings.json"), settings, 0o600); err != nil {
		t.Fatal(err)
	}

	seat := ladder.start(name)
	seat.turn()
	original, project := seat.sid, filepath.Dir(seat.transcript)
	seat.typeLine("/clear")
	successor := waitForNewTranscript(t, project, filepath.Base(seat.transcript))
	// The mock plays its input in order: this turn runs only after /clear and
	// both of its hooks have finished.
	seat.transcript = successor
	seat.turn()

	var rows []compose.Row
	deadline := time.Now().Add(15 * time.Second)
	for {
		var err error
		if rows, err = pfmchat.Rows(context.Background(), io.Discard, nil); err != nil {
			t.Fatalf("scan the fleet after /clear: %v", err)
		}
		listed, named := false, false
		for _, row := range rows {
			listed = listed || (row.ID == original && !row.Killed)
			named = named || (row.ID == strings.TrimSuffix(filepath.Base(successor), ".jsonl") &&
				row.Name == name+" #2" && !row.Killed)
		}
		if !listed && named {
			return
		}
		if time.Now().After(deadline) {
			var seen []string
			for _, row := range rows {
				seen = append(seen, fmt.Sprintf("%s name=%q killed=%v", row.ID, row.Name, row.Killed))
			}
			t.Fatalf("after /clear want %s hidden and the successor listed as %q; rows: %q", original, name+" #2", seen)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitForNewTranscript waits for the one transcript in project that is not
// known: the successor session a /clear opens.
func waitForNewTranscript(t *testing.T, project, known string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		files, err := filepath.Glob(filepath.Join(project, "*.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if filepath.Base(file) != known {
				return file
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("no successor transcript beside %s in %s", known, project)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
