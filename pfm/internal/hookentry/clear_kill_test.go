package hookentry

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// failingReader always returns an error, for pinning a hook's handling of a
// stdin read failure distinct from a decode failure.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("simulated read failure") }

// TestClearKillLogsAMalformedPayloadInsteadOfSwallowingIt pins L3-F18:
// ClearKill's decode and empty-payload branches returned 0 with no trace on
// stderr — fail-open is right for a hook, but the missing line meant `/clear`
// silently not recording a kill left no evidence anywhere. Every sibling
// (ExitClose, EpicInject, ReloadIntercept, ExitIntercept) logs
// its decode error in the same "pfm internal <hook>: decode hook payload
// (fail-open): %v" voice; ClearKill now does too.
func TestClearKillLogsAMalformedPayloadInsteadOfSwallowingIt(t *testing.T) {
	var stderr bytes.Buffer
	code := ClearKill(nil, strings.NewReader("{not json"), &stderr)
	if code != 0 {
		t.Fatalf("ClearKill() = %d, want 0 (fail-open)", code)
	}
	if !strings.Contains(stderr.String(), "decode hook payload") {
		t.Fatalf("stderr = %q, want it to name the decode failure instead of swallowing it", stderr.String())
	}
}

// TestClearKillReadFailureIsLoggedToo pins the same fail-open-but-loud
// contract for a stdin read error, not just a decode error.
func TestClearKillReadFailureIsLoggedToo(t *testing.T) {
	var stderr bytes.Buffer
	code := ClearKill(nil, failingReader{}, &stderr)
	if code != 0 {
		t.Fatalf("ClearKill() = %d, want 0 (fail-open)", code)
	}
	if !strings.Contains(stderr.String(), "read hook payload") {
		t.Fatalf("stderr = %q, want it to name the read failure instead of swallowing it", stderr.String())
	}
}

// TestClearKillEmptySessionIDStaysSilent is the control: a well-formed hook
// payload that simply names no session is a routine "nothing to do", not an
// error — it must not be logged.
func TestClearKillEmptySessionIDStaysSilent(t *testing.T) {
	var stderr bytes.Buffer
	code := ClearKill(nil, strings.NewReader(`{"hook_event_name":"SessionEnd","reason":"clear"}`), &stderr)
	if code != 0 {
		t.Fatalf("ClearKill() = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want silence for a routine absent session id", stderr.String())
	}
}

// A chat /cleared before any index pass saw it is indexed from the hook's own
// cwd and recorded as killed, never left listed as live.
func TestClearKillRecordsAChatClearedBeforeItsFirstIndexPass(t *testing.T) {
	root := testjail.Fleet(t)
	const id = "33333333-3333-4333-8333-333333333333"
	cwd := filepath.Join(root, "work", "desk")
	project := filepath.Join(root, pfmengine.MustLookup(pfmengine.Claude).LongName, strings.ReplaceAll(cwd, "/", "-"))
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	record := `{"type":"user","cwd":"` + cwd + `","sessionId":"` + id + `","message":{"content":"hello"}}` + "\n"
	if err := os.WriteFile(filepath.Join(project, id+".jsonl"), []byte(record), 0o600); err != nil {
		t.Fatal(err)
	}
	payload := `{"hook_event_name":"SessionEnd","reason":"clear","session_id":"` + id + `","cwd":"` + cwd + `"}`
	var stderr bytes.Buffer
	if code := ClearKill(nil, strings.NewReader(payload), &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("ClearKill() = %d stderr=%q, want 0 and silence", code, stderr.String())
	}
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	killed, found, err := database.Killed(context.Background(), id)
	if err != nil || !found || killed.ID != id {
		t.Fatalf("kill record = %+v found=%v err=%v, want session %s killed", killed, found, err, id)
	}
}
