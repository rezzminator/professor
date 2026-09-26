package hookentry

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// failingReader always returns an error, for pinning a hook's handling of a
// stdin read failure distinct from a decode failure.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("simulated read failure") }

// TestClearKillLogsAMalformedPayloadInsteadOfSwallowingIt pins L3-F18:
// ClearKill's decode and empty-payload branches returned 0 with no trace on
// stderr — fail-open is right for a hook, but the missing line meant `/clear`
// silently not recording a kill left no evidence anywhere. Every sibling
// (ExitClose, CompactNudge, EpicInject, ReloadIntercept, ExitIntercept) logs
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
