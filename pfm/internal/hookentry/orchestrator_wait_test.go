package hookentry

import (
	"bytes"
	"strings"
	"testing"
)

// The deny lines the hook prints, whole: one per reason.
const (
	denyWait        = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Waiting needs no command. End your message with one line and no tool call: while an agent you spawned is still running you do not return, and its return wakes you."}}` + "\n"
	denyReturnWatch = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"A sub-agent's return needs no watcher. End your message with one line and no tool call: its completion notice wakes you."}}` + "\n"
)

func TestOrchestratorWaitDeniesABashNoOpWait(t *testing.T) {
	for _, test := range []struct {
		name, payload string
		code          int
		want          string
	}{
		// Deny: each no-op form.
		{name: "echo literal", payload: `{"agent_type":"flights:speccer","tool_name":"Bash","tool_input":{"command":"echo waiting"}}`, want: denyWait},
		{name: "printf literal", payload: `{"agent_type":"flights:speccer","tool_name":"Bash","tool_input":{"command":"printf done"}}`, want: denyWait},
		{name: "true", payload: `{"agent_type":"flights:speccer","tool_name":"Bash","tool_input":{"command":"true"}}`, want: denyWait},
		{name: "colon", payload: `{"agent_type":"flights:speccer","tool_name":"Bash","tool_input":{"command":":"}}`, want: denyWait},
		{name: "sleep with surrounding whitespace", payload: `{"agent_type":"flights:speccer","tool_name":"Bash","tool_input":{"command":"  sleep 30 "}}`, want: denyWait},
		{name: "sleep with decimal and unit suffix", payload: `{"agent_type":"flights:speccer","tool_name":"Bash","tool_input":{"command":"sleep 1.5m"}}`, want: denyWait},

		{name: "background watch on a child return file", payload: `{"agent_type":"flights:speccer","tool_name":"Bash","tool_input":{"command":"timeout 14400 bash -c 'until [ -s \"$1\" ]; do sleep 20; done; cat \"$1\"' _ /flights/f/returns/unit-r1.md","run_in_background":true}}`, want: denyReturnWatch},
		{name: "while-not loop on a returns path", payload: `{"agent_type":"flights:speccer","tool_name":"Bash","tool_input":{"command":"while [ ! -f /flights/f/returns/gate-r1.md ]; do sleep 5; done"}}`, want: denyReturnWatch},
		{name: "test-form loop on a returns path", payload: `{"agent_type":"flights:speccer","tool_name":"Bash","tool_input":{"command":"until test -e flights/f/returns/gate-r1.md; do sleep 5; done"}}`, want: denyReturnWatch},

		// Pass: each exception.
		{name: "the same file loop on a command output file", payload: `{"agent_type":"flights:speccer","tool_name":"Bash","tool_input":{"command":"timeout 600 bash -c 'until [ -s \"$1\" ]; do sleep 20; done; cat \"$1\"' _ /tmp/professor/gate/out.log"}}`},
		{name: "the same file loop on an output file under a *-returns directory", payload: `{"agent_type":"flights:speccer","tool_name":"Bash","tool_input":{"command":"timeout 600 bash -c 'until [ -s \"$1\" ]; do sleep 20; done; cat \"$1\"' _ /tmp/acme-returns/gate/out.log"}}`},
		{name: "listing a returns directory", payload: `{"agent_type":"flights:speccer","tool_name":"Bash","tool_input":{"command":"ls -1 /flights/f/returns/"}}`},
		{name: "reading a return file", payload: `{"agent_type":"flights:speccer","tool_name":"Bash","tool_input":{"command":"cat /flights/f/returns/unit-r1.md"}}`},
		{name: "seat returns monitor loop", payload: `{"agent_type":"flights:speccer","tool_name":"Bash","tool_input":{"command":"bash -c 'while :; do now=$(ls -1 $G); comm -13 <(echo \"$seen\") <(echo \"$now\"); sleep 15; done' _ /flights/f/returns/*"}}`},
		{name: "echo with redirect is not literal", payload: `{"agent_type":"flights:speccer","tool_name":"Bash","tool_input":{"command":"echo x > file"}}`},
		{name: "echo with substitution is not literal", payload: `{"agent_type":"flights:speccer","tool_name":"Bash","tool_input":{"command":"echo $X"}}`},
		{name: "sleep chained with another command", payload: `{"agent_type":"flights:speccer","tool_name":"Bash","tool_input":{"command":"sleep 5 && ls"}}`},
		{name: "a loop around sleep is not a single no-op", payload: `{"agent_type":"flights:speccer","tool_name":"Bash","tool_input":{"command":"until foo; do sleep 15; done"}}`},
		{name: "true chained with another command", payload: `{"agent_type":"flights:speccer","tool_name":"Bash","tool_input":{"command":"true && git status"}}`},
		{name: "non-Bash tool is never denied", payload: `{"agent_type":"flights:speccer","tool_name":"Read","tool_input":{"file_path":"/tmp/x"}}`},

		// Silent: every loop outside the judged set.
		{name: "main chat with no agent type", payload: `{"tool_name":"Bash","tool_input":{"command":"echo waiting"}}`},
		{name: "main chat watching a returns path", payload: `{"tool_name":"Bash","tool_input":{"command":"while [ ! -f /flights/f/returns/gate-r1.md ]; do sleep 5; done"}}`},
		{name: "another agent type", payload: `{"agent_type":"flights:foreman","tool_name":"Bash","tool_input":{"command":"sleep 30"}}`},

		// Fail-open edges.
		{name: "empty payload", payload: ``},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := OrchestratorWait(strings.NewReader(test.payload), &stdout, &stderr)
			if code != test.code {
				t.Fatalf("code=%d stdout=%q stderr=%q, want code=%d", code, stdout.String(), stderr.String(), test.code)
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
