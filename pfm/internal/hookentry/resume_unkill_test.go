package hookentry

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/store"
)

func TestResumeUnkillLogsAMalformedPayloadInsteadOfSwallowingIt(t *testing.T) {
	var stderr strings.Builder
	code := ResumeUnkill(nil, strings.NewReader("{not json"), &stderr)
	if code != 0 {
		t.Fatalf("ResumeUnkill() = %d, want 0 (fail-open)", code)
	}
	if !strings.Contains(stderr.String(), "decode hook payload") {
		t.Fatalf("stderr = %q, want the decode failure named", stderr.String())
	}
}

func TestResumeUnkillReadFailureIsLoggedToo(t *testing.T) {
	var stderr strings.Builder
	code := ResumeUnkill(nil, failingReader{}, &stderr)
	if code != 0 {
		t.Fatalf("ResumeUnkill() = %d, want 0 (fail-open)", code)
	}
	if !strings.Contains(stderr.String(), "read hook payload") {
		t.Fatalf("stderr = %q, want the read failure named", stderr.String())
	}
}

func TestResumeUnkillIgnoresEverySessionStartButResume(t *testing.T) {
	for _, payload := range []string{
		`{"hook_event_name":"SessionStart","source":"startup","session_id":"11111111-1111-4111-8111-111111111111"}`,
		`{"hook_event_name":"SessionStart","source":"clear","session_id":"11111111-1111-4111-8111-111111111111"}`,
		`{"hook_event_name":"SessionEnd","reason":"resume","session_id":"11111111-1111-4111-8111-111111111111"}`,
	} {
		var stderr strings.Builder
		if code := ResumeUnkill(nil, strings.NewReader(payload), &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatalf("ResumeUnkill(%s) = %d stderr=%q, want a silent 0", payload, code, stderr.String())
		}
	}
}

func TestResumeUnkillRejectsASessionIDThatIsNotAUUID(t *testing.T) {
	var stderr strings.Builder
	payload := `{"hook_event_name":"SessionStart","source":"resume","session_id":"not-an-id"}`
	if code := ResumeUnkill(nil, strings.NewReader(payload), &stderr); code != 0 {
		t.Fatalf("ResumeUnkill() = %d, want 0 (fail-open)", code)
	}
	if !strings.Contains(stderr.String(), "is not a session id") {
		t.Fatalf("stderr = %q, want the invalid session id named", stderr.String())
	}
}

// TestResumeUnkillLiftsAKilledCodexThreadOnACodexResumePayload pins the Codex
// half of the resume door: Codex's SessionStart hook input (codex-cli
// session-start.command.input) carries the resumed thread's id as session_id
// and source "resume" for `codex resume`, the in-app /resume and pfm's own
// resume alike. A kill on the lineage root lifts whether the payload names the
// root or an indexed member of its lineage; every other source keeps it.
func TestResumeUnkillLiftsAKilledCodexThreadOnACodexResumePayload(t *testing.T) {
	const (
		root   = "019a1b2c-3d4e-7f60-8a1b-2c3d4e5f6071"
		member = "019a1b2c-3d4e-7f60-8a1b-2c3d4e5f6072"
	)
	codexPayload := func(source, id string) string {
		return `{"session_id":"` + id + `","transcript_path":"/work/rollout-` + id + `.jsonl",` +
			`"cwd":"/work/example","hook_event_name":"SessionStart","model":"gpt-6-astra",` +
			`"permission_mode":"default","source":"` + source + `"}`
	}
	for _, test := range []struct {
		name       string
		payload    string
		wantKilled bool
	}{
		{name: "resume of the killed root lifts the kill", payload: codexPayload("resume", root)},
		{name: "resume through a lineage member lifts the kill", payload: codexPayload("resume", member)},
		{name: "startup keeps the kill", payload: codexPayload("startup", root), wantKilled: true},
		{name: "fork keeps the kill", payload: codexPayload("fork", root), wantKilled: true},
		{name: "compact keeps the kill", payload: codexPayload("compact", root), wantKilled: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PFM_STATE_DB", filepath.Join(dir, "shared.db"))
			ctx := context.Background()
			database, err := store.Open()
			if err != nil {
				t.Fatal(err)
			}
			for _, rollout := range []store.Rollout{
				{
					ID: root, Path: filepath.Join(dir, "rollout-"+root+".jsonl"), UserThread: true,
					SessionID: root, CWD: "/work/example", Size: 1, PromptCount: 2,
				},
				{
					ID: member, Path: filepath.Join(dir, "rollout-"+member+".jsonl"), UserThread: true,
					SessionID: root, CWD: "/work/example", Size: 1, PromptCount: 3,
				},
			} {
				if err := database.UpsertRollout(ctx, rollout); err != nil {
					t.Fatal(err)
				}
			}
			kill := store.Killed{ID: root, Engine: pfmengine.Codex, KilledAt: 1791050854}
			if err := database.Kill(ctx, kill); err != nil {
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}

			var stderr strings.Builder
			if code := ResumeUnkill(nil, strings.NewReader(test.payload), &stderr); code != 0 {
				t.Fatalf("ResumeUnkill() = %d stderr=%q, want 0", code, stderr.String())
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
			_, killed, err := database.Killed(ctx, root)
			if err != nil {
				t.Fatal(err)
			}
			if killed != test.wantKilled {
				t.Fatalf("Codex root killed = %v, want %v (stderr=%q)", killed, test.wantKilled, stderr.String())
			}
		})
	}
}
