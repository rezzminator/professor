package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

const reminderTestChatID = "33333333-3333-4333-8333-333333333333"

// indexReminderChat plants an indexed Claude transcript the resolver finds by
// its session id, and returns that id.
func indexReminderChat(t *testing.T) string {
	t.Helper()
	root := jailTest(t)
	project := filepath.Join(root, "work", "project")
	transcriptDir := filepath.Join(root, "claude", "project")
	for _, directory := range []string{project, transcriptDir} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	content := `{"type":"user","cwd":` + strconv.Quote(project) +
		`,"message":{"content":"Reminder target prompt"}}` + "\n"
	transcriptPath := filepath.Join(transcriptDir, reminderTestChatID+".jsonl")
	if err := os.WriteFile(transcriptPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"index", "--full"}, &stdout, &stderr); code != 0 {
		t.Fatalf("index code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	return reminderTestChatID
}

func runReminder(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(append([]string{"chat", "reminder"}, args...), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestChatReminderSetLsRmRoundTrip(t *testing.T) {
	id := indexReminderChat(t)

	code, stdout, stderr := runReminder(t, "set", "--every", "2h", "--prompt", "check the build", id)
	if code != 0 || stdout != "1\n" {
		t.Fatalf("set code=%d stdout=%q stderr=%q, want 0 and the id alone", code, stdout, stderr)
	}

	code, stdout, stderr = runReminder(t, "ls")
	lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
	if code != 0 || len(lines) != 2 {
		t.Fatalf("ls code=%d stdout=%q stderr=%q, want header and one row", code, stdout, stderr)
	}
	if got := strings.Join(strings.Fields(lines[0]), " "); got != "ID CHAT EVERY NEXT LAST UNSEEN PROMPT" {
		t.Fatalf("ls header = %q", lines[0])
	}
	if !strings.Contains(lines[1], "check the build") || !strings.Contains(lines[1], "2h0m0s") {
		t.Fatalf("ls row = %q, want the prompt and the interval", lines[1])
	}

	code, stdout, stderr = runReminder(t, "ls", "--json")
	var listed []fleetdb.Reminder
	if err := json.Unmarshal([]byte(stdout), &listed); err != nil || code != 0 {
		t.Fatalf("ls --json code=%d stdout=%q stderr=%q err=%v", code, stdout, stderr, err)
	}
	if len(listed) != 1 || listed[0].SessionID != id || listed[0].Interval != 2*time.Hour ||
		listed[0].Prompt != "check the build" || listed[0].Engine != string(pfmengine.Claude) {
		t.Fatalf("listed = %+v", listed)
	}

	code, stdout, stderr = runReminder(t, "rm", "1")
	if code != 0 || stdout != "removed reminder 1\n" {
		t.Fatalf("rm code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if code, stdout, _ = runReminder(t, "ls"); code != 0 || stdout != "no reminders\n" {
		t.Fatalf("ls after rm code=%d stdout=%q, want 'no reminders'", code, stdout)
	}
	if code, _, stderr = runReminder(t, "rm", "1"); code != 1 || !strings.Contains(stderr, "no reminder 1") {
		t.Fatalf("second rm code=%d stderr=%q, want 1 and 'no reminder 1'", code, stderr)
	}
}

func TestChatReminderSetRefusals(t *testing.T) {
	id := indexReminderChat(t)
	cases := []struct {
		name     string
		args     []string
		wantCode int
		wantErr  string
	}{
		{"interval below minimum", []string{"set", "--every", "30s", "--prompt", "x", id}, 2, "minimum 1m"},
		{"interval garbage", []string{"set", "--every", "soon", "--prompt", "x", id}, 2, "weekly"},
		{"blank prompt", []string{"set", "--every", "1h", "--prompt", "  ", id}, 2, "prompt"},
		{"two chats", []string{"set", "--every", "1h", "--prompt", "x", id, id}, 2, "usage"},
		{"unknown chat", []string{"set", "--every", "1h", "--prompt", "x", "nobody"}, codeUnknownChat, "no chat named"},
		{"no subcommand", nil, 2, "usage"},
		{"unknown subcommand", []string{"bogus"}, 2, "usage"},
		{"rm without id", []string{"rm"}, 2, "usage"},
		{"rm non integer", []string{"rm", "abc"}, 2, "usage"},
	}
	for _, tc := range cases {
		code, stdout, stderr := runReminder(t, tc.args...)
		if code != tc.wantCode || !strings.Contains(stderr, tc.wantErr) {
			t.Errorf("%s: code=%d stdout=%q stderr=%q, want %d and %q",
				tc.name, code, stdout, stderr, tc.wantCode, tc.wantErr)
		}
	}
	if code, stdout, _ := runReminder(t, "ls"); code != 0 || stdout != "no reminders\n" {
		t.Fatalf("a refused set stored a reminder: code=%d stdout=%q", code, stdout)
	}
}

func TestChatReminderLsJSONEmptyIsAnArray(t *testing.T) {
	jailTest(t)
	code, stdout, stderr := runReminder(t, "ls", "--json")
	if code != 0 || strings.TrimSpace(stdout) != "[]" {
		t.Fatalf("code=%d stdout=%q stderr=%q, want []", code, stdout, stderr)
	}
}

func TestChatReminderSetWithoutChatNeedsACallerIdentity(t *testing.T) {
	jailTest(t)
	t.Setenv("TMUX", "")
	code, stdout, stderr := runReminder(t, "set", "--every", "1h", "--prompt", "x")
	if code != 1 || !strings.Contains(stderr, "cannot tell which chat this is; name the chat") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestChatReminderLsShowsAFailedFire(t *testing.T) {
	jailTest(t)
	ctx := context.Background()
	state := fleetdb.OpenSharedState(ctx, jailPaths(t))
	if err := state.Degraded(); err != nil {
		t.Fatal(err)
	}
	id, err := state.CreateReminder(ctx, fleetdb.Reminder{
		SessionID: "sess-1", Engine: "claude", Label: "docs", Prompt: strings.Repeat("long ", 30),
		Interval: time.Hour, Created: time.Now().Add(-3 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.RecordReminderFailure(ctx, id, time.Now(), "socket gone"); err != nil {
		t.Fatal(err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runReminder(t, "ls")
	if code != 0 || !strings.Contains(stdout, "  last fire failed ") || !strings.Contains(stdout, ": socket gone") ||
		!strings.Contains(stdout, "docs") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, line := range strings.Split(stdout, "\n") {
		if strings.HasPrefix(line, "1") && strings.Contains(line, "long") && len([]rune(line)) > 200 {
			t.Fatalf("prompt column not truncated: %q", line)
		}
	}
}

func TestReminderFireWithAnUnresolvableChatStaysDue(t *testing.T) {
	jailTest(t)
	ctx := context.Background()
	state := fleetdb.OpenSharedState(ctx, jailPaths(t))
	if err := state.Degraded(); err != nil {
		t.Fatal(err)
	}
	created := time.Now().Add(-3 * time.Hour)
	id, err := state.CreateReminder(ctx, fleetdb.Reminder{
		SessionID: "99999999-9999-4999-8999-999999999999", Engine: "claude", Label: "ghost", Prompt: "wake up",
		Interval: time.Hour, Created: created,
	})
	if err != nil {
		t.Fatal(err)
	}
	before, found, err := state.Reminder(ctx, id)
	if err != nil || !found {
		t.Fatalf("seeded reminder: found=%v err=%v", found, err)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{"internal", "reminder-fire"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "pfm internal reminder-fire: reminder 1 (ghost, 9999") ||
		!strings.Contains(stderr.String(), "stays due") || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	state = fleetdb.OpenSharedState(ctx, jailPaths(t))
	defer func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	}()
	after, _, err := state.Reminder(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if !after.NextFire.Equal(before.NextFire) || after.LastError == "" || after.Unseen {
		t.Fatalf("after a failed fire: %+v, want NextFire %v kept and LastError set", after, before.NextFire)
	}
}

func TestReminderFireNothingDueIsSilent(t *testing.T) {
	jailTest(t)
	var stdout, stderr bytes.Buffer
	code := run([]string{"internal", "reminder-fire"}, &stdout, &stderr)
	if code != 0 || stdout.Len()+stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q, want silent 0", code, stdout.String(), stderr.String())
	}
	if code := run([]string{"internal", "reminder-fire", "extra"}, &stdout, &stderr); code != 2 {
		t.Fatalf("extra arg code=%d, want 2", code)
	}
}
