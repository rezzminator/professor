package hookentry

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// The deny lines the hook prints, whole: one per reason.
const (
	denyWait        = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Waiting needs no command. End your message with one line and no tool call: while an agent you spawned is still running you do not return, and its return wakes you."}}` + "\n"
	denyReturnWatch = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"A sub-agent's return needs no watcher. End your message with one line and no tool call: its completion notice wakes you."}}` + "\n"
)

// denyOf names the deny line a row of testdata/orchestrator_wait.json expects.
var denyOf = map[string]string{"": "", "wait": denyWait, "returnWatch": denyReturnWatch}

// The rows' payloads are data, never shell text this file runs: the hook's subject is the
// commands it reads, `sleep` among them. Rows run in the file's order: each no-op form
// denied, each exception passed, every loop outside the judged set silent, the empty
// payload failing open.
func TestOrchestratorWaitDeniesABashNoOpWait(t *testing.T) {
	var rows []struct {
		Name    string          `json:"name"`
		Payload json.RawMessage `json:"payload"`
		Want    string          `json:"want"`
	}
	readHookentryData(t, "orchestrator_wait.json", &rows)
	if len(rows) != 24 {
		t.Fatalf("testdata/orchestrator_wait.json holds %d rows, want 24", len(rows))
	}
	for _, row := range rows {
		test := struct{ name, payload, want string }{row.Name, string(row.Payload), denyOf[row.Want]}
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := OrchestratorWait(strings.NewReader(test.payload), &stdout, &stderr)
			if code != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q, want code=0", code, stdout.String(), stderr.String())
			}
			if test.want == "" {
				if stdout.Len() != 0 {
					t.Fatalf("stdout=%q, want no output", stdout.String())
				}
				return
			}
			if stdout.String() != test.want {
				t.Fatalf("stdout=%q, want %q", stdout.String(), test.want)
			}
		})
	}
}

// TestOrchestratorWaitLogsAMalformedPayloadInsteadOfSwallowingIt pins the
// same fail-open-but-loud contract every sibling hook keeps: a malformed
// payload is not indistinguishable from "nothing to deny" — it is named on
// stderr in the same "pfm internal <hook>: decode hook payload (fail-open):
// %v" voice.
func TestOrchestratorWaitLogsAMalformedPayloadInsteadOfSwallowingIt(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := OrchestratorWait(strings.NewReader("not-json"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("OrchestratorWait() = %d, want 0 (fail-open)", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%q, want no output on a malformed payload", stdout.String())
	}
	if !strings.Contains(stderr.String(), "decode hook payload") {
		t.Fatalf("stderr = %q, want it to name the decode failure instead of swallowing it", stderr.String())
	}
}

// TestOrchestratorWaitReadFailureIsLoggedToo pins the same fail-open-but-loud
// contract for a stdin read error, not just a decode error.
func TestOrchestratorWaitReadFailureIsLoggedToo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := OrchestratorWait(failingReader{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("OrchestratorWait() = %d, want 0 (fail-open)", code)
	}
	if !strings.Contains(stderr.String(), "read hook payload") {
		t.Fatalf("stderr = %q, want it to name the read failure instead of swallowing it", stderr.String())
	}
}
