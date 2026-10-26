package mcpserv

import (
	"context"
	"strings"
	"testing"
)

// A call naming no target — blanks included — is refused before any engine
// is built, never sent to an empty name.
func TestChatCallRefusesWithoutTarget(t *testing.T) {
	for _, input := range []CallInput{
		{Message: "body"},
		{Target: "  ", Targets: []string{"", " "}, Message: "body"},
	} {
		_, _, err := (&Service{}).chatCall(context.Background(), nil, input)
		if err == nil || !strings.Contains(err.Error(), "needs a target or targets") {
			t.Fatalf("%+v: err=%v, want the missing-target refusal", input, err)
		}
	}
}
