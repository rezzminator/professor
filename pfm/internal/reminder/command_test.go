package reminder

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
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
