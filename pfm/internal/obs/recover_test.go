package obs

import (
	"errors"
	"strings"
	"testing"
)

func TestRecoveredNamesTheLabelTypeAndBoundedMessage(t *testing.T) {
	for _, row := range []struct {
		label string
		value any
		want  string
	}{
		{"tool call", errors.New("index out of range [3] with length 2"), "panic in tool call: *errors.errorString: index out of range [3] with length 2"},
		{"tool huge", strings.Repeat("x", MaxValueBytes+500), "panic in tool huge: string: {2048×x}…(truncated 500 bytes)"},
	} {
		if got := runs(Recovered(row.label, row.value).Error()); got != row.want {
			t.Errorf("Recovered(%q) = %q, want %q", row.label, got, row.want)
		}
	}
}

// TestRecoveredRefusesACredentialShapedPanicMessage: a panic value that
// carries something secret-shaped never reaches the returned error text —
// it is client-visible (an MCP tool error), not just a log field.
func TestRecoveredRefusesACredentialShapedPanicMessage(t *testing.T) {
	err := Recovered("tool call", errors.New("Bearer sk-ant-api03-PLANTEDPANICSECRET"))
	if err.Error() != "panic in tool call: *errors.errorString: <redacted>" {
		t.Fatalf("Recovered leaked a credential-shaped panic message: %q", err.Error())
	}
}
