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
