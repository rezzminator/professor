package hookentry

import (
	"bytes"
	"strings"
	"testing"
)

func TestOrchestratorWaitDeniesABashNoOpWait(t *testing.T) {
	for _, test := range []struct {
		name, payload string
		code          int
		want          string
	}{
		// Deny: each no-op form.
		{name: "echo literal", payload: `{"tool_name":"Bash","tool_input":{"command":"echo waiting"}}`, want: "permissionDecision\":\"deny\""},
		{name: "printf literal", payload: `{"tool_name":"Bash","tool_input":{"command":"printf done"}}`, want: "permissionDecision\":\"deny\""},
		{name: "true", payload: `{"tool_name":"Bash","tool_input":{"command":"true"}}`, want: "permissionDecision\":\"deny\""},
		{name: "colon", payload: `{"tool_name":"Bash","tool_input":{"command":":"}}`, want: "permissionDecision\":\"deny\""},
		{name: "sleep with surrounding whitespace", payload: `{"tool_name":"Bash","tool_input":{"command":"  sleep 30 "}}`, want: "permissionDecision\":\"deny\""},
		{name: "sleep with decimal and unit suffix", payload: `{"tool_name":"Bash","tool_input":{"command":"sleep 1.5m"}}`, want: "permissionDecision\":\"deny\""},

		// Pass: each exception.
		{name: "echo with redirect is not literal", payload: `{"tool_name":"Bash","tool_input":{"command":"echo x > file"}}`},
		{name: "echo with substitution is not literal", payload: `{"tool_name":"Bash","tool_input":{"command":"echo $X"}}`},
		{name: "sleep chained with another command", payload: `{"tool_name":"Bash","tool_input":{"command":"sleep 5 && ls"}}`},
		{name: "a loop around sleep is not a single no-op", payload: `{"tool_name":"Bash","tool_input":{"command":"until foo; do sleep 15; done"}}`},
		{name: "true chained with another command", payload: `{"tool_name":"Bash","tool_input":{"command":"true && git status"}}`},
		{name: "non-Bash tool is never denied", payload: `{"tool_name":"Read","tool_input":{"file_path":"/tmp/x"}}`},

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
			if !strings.Contains(stdout.String(), test.want) {
				t.Fatalf("stdout=%q, want it to contain %q", stdout.String(), test.want)
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
