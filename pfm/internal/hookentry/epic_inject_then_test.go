package hookentry

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/reload"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// A native reload that could not type its --then message leaves it stored for
// the reborn chat: that chat's first prompt carries it once, never a second
// time, and neither another session of the seat nor a prompt typed while the
// reload still holds the pane takes it.
func TestEpicInjectCarriesAnUndeliveredReloadFollowUpOnce(t *testing.T) {
	sid := t.TempDir()
	t.Setenv(paths.EnvSIDDir, sid)
	socket := filepath.Join(sid, "herdr.sock")
	backstop := filepath.Join(sid, "reload-herdr-seat-1.then")
	stored := `{"socket":"` + socket + `","pane":"w1:p2","session":"reborn-session","message":"Read the handoff in FULL"}`
	if err := os.WriteFile(backstop, []byte(stored), 0o600); err != nil {
		t.Fatal(err)
	}
	oldIdentify, oldWindow := epicInjectIdentify, epicInjectWindowName
	epicInjectIdentify = func(context.Context) (resolve.Identity, error) {
		return resolve.Identity{
			Backend: "herdr", SeatID: "seat-1", SocketPath: socket, Pane: "w1:p2", Session: "plain",
		}, nil
	}
	epicInjectWindowName = func(context.Context, resolve.Identity) (string, error) { return "plain", nil }
	t.Cleanup(func() { epicInjectIdentify, epicInjectWindowName = oldIdentify, oldWindow })
	call := func(session, prompt string) string {
		t.Helper()
		payload, err := json.Marshal(map[string]string{"session_id": session, "cwd": sid, "prompt": prompt})
		if err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if code := EpicInject(bytes.NewReader(payload), &stdout, &stderr); code != 0 || stderr.Len() != 0 {
			t.Fatalf("epic inject code=%d stderr=%q", code, stderr.String())
		}
		return stdout.String()
	}
	kept := func(when string) {
		t.Helper()
		if got, err := os.ReadFile(backstop); err != nil || string(got) != stored {
			t.Fatalf("%s: backstop %s = %q (%v), want it kept as %q", when, backstop, got, err, stored)
		}
	}
	if got := call("old-session", "go on"); got != "" {
		t.Fatalf("another session of the seat took the follow-up: %q", got)
	}
	kept("another session")
	lock, err := os.OpenFile(
		reload.LockPath(sid, fmt.Sprintf("herdr-%x", sha256.Sum256([]byte(socket+"\x00w1:p2"))), "seat"),
		os.O_CREATE|os.O_RDWR, 0o600,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	if got := call("reborn-session", "go on"); got != "" {
		t.Fatalf("a prompt typed while the reload holds the pane took the follow-up: %q", got)
	}
	kept("reload in flight")
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	want := `{"hookSpecificOutput":{"hookEventName":"UserPromptSubmit","additionalContext":` +
		`"PFM RELOAD FOLLOW-UP NOT TYPED: this chat was started by pfm chat reload --then, whose follow-up ` +
		`message below never reached this chat's input box. Carry it out as the user's instruction:\n` +
		`Read the handoff in FULL"}}` + "\n"
	for _, blocked := range []string{"e", "/e", "/pfm:reload --then 'next'", "/pfm:space work"} {
		if got := call("reborn-session", blocked); got != "" {
			t.Fatalf("prompt %q, which an intercept hook blocks, took the follow-up: %q", blocked, got)
		}
		kept("prompt " + blocked + " blocked")
	}
	if got := call("reborn-session", "go on"); got != want {
		t.Fatalf("first prompt of the reborn chat = %q, want %q", got, want)
	}
	if got := call("reborn-session", "go on"); got != "" {
		t.Fatalf("follow-up injected twice: %q", got)
	}
	if _, err := os.Stat(backstop); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backstop %s left after delivery: %v", backstop, err)
	}
}
