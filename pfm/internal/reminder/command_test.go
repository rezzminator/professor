package reminder

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func unreachableNotFound(name string, _ io.Writer) int { panic("chat lookup reached for " + name) }

func TestRunReminderCommandRefusesBadUsageBeforeTouchingAnyStore(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr string
	}{
		{"no verb", nil, "pfm chat reminder set --every"},
		{"unknown verb", []string{"bogus"}, `unknown command "bogus"`},
		{"interval below minimum", []string{"set", "--every", "30s", "--prompt", "x"}, "minimum 1m"},
		{"interval garbage", []string{"set", "--every", "soon", "--prompt", "x"}, "weekly, <N>d"},
		{"blank prompt", []string{"set", "--every", "1h", "--prompt", " "}, "--prompt must not be empty"},
		{"two chats", []string{"set", "--every", "1h", "--prompt", "x", "a", "b"}, "usage: pfm chat reminder set"},
		{"ls stray argument", []string{"ls", "extra"}, "usage: pfm chat reminder ls"},
		{"rm without id", []string{"rm"}, "usage: pfm chat reminder rm <id>"},
		{"rm non integer", []string{"rm", "abc"}, "usage: pfm chat reminder rm <id>"},
		{"rm zero", []string{"rm", "0"}, "not a reminder id"},
	}
	for _, tc := range cases {
		var stdout, stderr bytes.Buffer
		code := RunReminderCommand(tc.args, &stdout, &stderr, nil, unreachableNotFound)
		if code != 2 || !strings.Contains(stderr.String(), tc.wantErr) || stdout.Len() != 0 {
			t.Errorf("%s: code=%d stdout=%q stderr=%q, want 2 and %q",
				tc.name, code, stdout.String(), stderr.String(), tc.wantErr)
		}
	}
}

func TestWriteReminderTableRendersColumnsAndAFailedFire(t *testing.T) {
	when := time.Date(2026, 10, 3, 9, 30, 0, 0, time.Local)
	reminders := []fleetdb.Reminder{
		{
			ID: 1, SessionID: "12345678-aaaa", Label: "docs", Prompt: "line one\nline two", Interval: 24 * time.Hour,
			NextFire: when, LastFired: when.Add(-time.Hour), Unseen: true,
		},
		{
			ID: 2, SessionID: "abcdefghijkl", Prompt: strings.Repeat("x", 80), Interval: 90 * time.Minute,
			NextFire:  when,
			LastError: "socket gone", LastErrorAt: when,
		},
	}
	var out bytes.Buffer
	if err := writeReminderTable(&out, reminders); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("lines = %q, want header, two rows and one failure line", lines)
	}
	if got := strings.Join(strings.Fields(lines[0]), " "); got != "ID CHAT EVERY NEXT LAST UNSEEN PROMPT" {
		t.Errorf("header = %q", lines[0])
	}
	wantRow := "1 docs 1d 2026-10-03 09:30 2026-10-03 08:30 yes line one line two"
	if got := strings.Join(strings.Fields(lines[1]), " "); got != wantRow {
		t.Errorf("row 1 = %q, want %q", got, wantRow)
	}
	if !strings.Contains(lines[2], "abcdefgh") || strings.Contains(lines[2], "abcdefghi") ||
		!strings.Contains(lines[2], "1h30m0s") || !strings.HasSuffix(lines[2], strings.Repeat("x", 59)+"…") {
		t.Errorf("row 2 = %q, want the 8-char session, the interval and a 60-rune prompt", lines[2])
	}
	if lines[3] != "  last fire failed 2026-10-03 09:30: socket gone" {
		t.Errorf("failure line = %q", lines[3])
	}
}

func TestWriteReminderTableEmptyIsSaidOutLoud(t *testing.T) {
	var out bytes.Buffer
	if err := writeReminderTable(&out, nil); err != nil || out.String() != "no reminders\n" {
		t.Fatalf("out=%q err=%v", out.String(), err)
	}
}

// standInsideCodexThread makes the command's caller a Codex chat whose
// CODEX_THREAD_ID is thread, in a scratch fleet whose rollout index holds the
// given rollouts.
func standInsideCodexThread(t *testing.T, thread string, rollouts ...store.Rollout) {
	t.Helper()
	testjail.Fleet(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	for index := range rollouts {
		if err := database.UpsertRollout(context.Background(), rollouts[index]); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	previous := callerIdentity
	callerIdentity = func(context.Context, *pfmconfig.Runtime) (resolve.Identity, bool) {
		return resolve.Identity{Engine: string(pfmengine.Codex), ID: thread, Source: "env-codex"}, true
	}
	t.Cleanup(func() { callerIdentity = previous })
}

func storedReminders(t *testing.T) []fleetdb.Reminder {
	t.Helper()
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	state := fleetdb.OpenSharedState(context.Background(), values)
	defer func() {
		if err := state.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	if err := state.Degraded(); err != nil {
		t.Fatal(err)
	}
	reminders, err := state.Reminders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return reminders
}

// A reminder set from inside a Codex chat is keyed on the chat's lineage root,
// the id its fleet row carries and a fire matches: CODEX_THREAD_ID may name a
// resumed member of the lineage, and a reminder keyed on it would never fire.
func TestReminderSetFromInsideCodexChatKeysTheLineageRoot(t *testing.T) {
	const (
		root   = "0d0d0d0d-0d0d-4d0d-8d0d-0d0d0d0d0d0d"
		member = "0e0e0e0e-0e0e-4e0e-8e0e-0e0e0e0e0e0e"
	)
	standInsideCodexThread(t, member,
		store.Rollout{ID: root, Path: "/work/rollout-root.jsonl", UserThread: true, PromptCount: 1},
		store.Rollout{
			ID: member, Path: "/work/rollout-member.jsonl", ParentThread: root,
			UserThread: true, PromptCount: 2,
		},
	)
	var stdout, stderr bytes.Buffer
	code := RunReminderCommand(
		[]string{"set", "--every", "1h", "--prompt", "check the build"}, &stdout, &stderr, nil, unreachableNotFound,
	)
	if code != 0 {
		t.Fatalf("set from inside Codex thread: code=%d stderr=%q", code, stderr.String())
	}
	reminders := storedReminders(t)
	if len(reminders) != 1 {
		t.Fatalf("stored reminders = %+v, want one", reminders)
	}
	if got := reminders[0]; got.SessionID != root || got.SetByID != root || got.Engine != string(pfmengine.Codex) {
		t.Fatalf("stored chat id = %q, set by %q, engine %q; want the lineage root %q for both, engine %q",
			got.SessionID, got.SetByID, got.Engine, root, pfmengine.Codex)
	}
}

// A Codex thread the index cannot map to its chat is refused out loud and
// stores nothing: a reminder keyed on it could never fire.
func TestReminderSetFromInsideUnindexedCodexThreadIsRefused(t *testing.T) {
	const thread = "0f0f0f0f-0f0f-4f0f-8f0f-0f0f0f0f0f0f"
	standInsideCodexThread(t, thread)
	var stdout, stderr bytes.Buffer
	code := RunReminderCommand(
		[]string{"set", "--every", "1h", "--prompt", "check the build"}, &stdout, &stderr, nil, unreachableNotFound,
	)
	if code != 1 || stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "holds no Codex conversation with thread "+thread) {
		t.Fatalf("code=%d stdout=%q stderr=%q, want 1 and the unindexed-thread refusal",
			code, stdout.String(), stderr.String())
	}
	if reminders := storedReminders(t); len(reminders) != 0 {
		t.Fatalf("stored reminders = %+v, want none", reminders)
	}
}
