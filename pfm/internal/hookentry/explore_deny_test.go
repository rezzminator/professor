package hookentry

import (
	"bytes"
	"strings"
	"testing"
)

func TestExploreDenyFailsOpenAndSteersExploreToTracer(t *testing.T) {
	for _, test := range []struct {
		name, payload string
		code          int
		want          string
	}{
		{name: "explore is denied", payload: `{"tool_input":{"subagent_type":"Explore","model":"sonnet"}}`, want: "permissionDecision\":\"deny\""},
		{name: "tracer child allowance", payload: `{"tool_input":{"subagent_type":"Explore","model":"haiku"}}`},
		{name: "other agent allowed", payload: `{"tool_input":{"subagent_type":"tracer","model":"sonnet"}}`},
		{name: "malformed fails open", payload: "not-json"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := ExploreDeny(strings.NewReader(test.payload), &stdout, &stderr)
			if code != test.code || !strings.Contains(stdout.String(), test.want) {
				t.Fatalf(
					"code=%d stdout=%q stderr=%q, want code=%d and %q",
					code,
					stdout.String(),
					stderr.String(),
					test.code,
					test.want,
				)
			}
		})
	}
}

// TestExploreDenyLogsAMalformedPayloadInsteadOfSwallowingIt pins L3-F18:
// fail-open is right for a PreToolUse hook, but the missing stderr line
// left a malformed payload indistinguishable from "nothing to deny" — every
// sibling (ExitClose, CompactNudge, EpicInject, ReloadIntercept,
// ExitIntercept) logs its decode error in the same
// "pfm internal <hook>: decode hook payload (fail-open): %v" voice;
// ExploreDeny now does too.
func TestExploreDenyLogsAMalformedPayloadInsteadOfSwallowingIt(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := ExploreDeny(strings.NewReader("not-json"), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("ExploreDeny() = %d, want 0 (fail-open)", code)
	}
	if !strings.Contains(stderr.String(), "decode hook payload") {
		t.Fatalf("stderr = %q, want it to name the decode failure instead of swallowing it", stderr.String())
	}
}

// TestExploreDenyReadFailureIsLoggedToo pins the same fail-open-but-loud
// contract for a stdin read error, not just a decode error.
func TestExploreDenyReadFailureIsLoggedToo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := ExploreDeny(failingReader{}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("ExploreDeny() = %d, want 0 (fail-open)", code)
	}
	if !strings.Contains(stderr.String(), "read hook payload") {
		t.Fatalf("stderr = %q, want it to name the read failure instead of swallowing it", stderr.String())
	}
}
