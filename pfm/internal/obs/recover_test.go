package obs

import (
	"errors"
	"strings"
	"testing"
)

// TestRecoveredNamesTheLabelTypeAndBoundedMessage pins Recovered's shape: a
// caller-supplied label, the panic value's dynamic type, and its message —
// capped, never a stack dump of the panicking call's arguments.
func TestRecoveredNamesTheLabelTypeAndBoundedMessage(t *testing.T) {
	err := Recovered("tool chat_inject", errors.New("index out of range [3] with length 2"))
	for _, want := range []string{"tool chat_inject", "index out of range"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Recovered error = %q, want it to contain %q", err.Error(), want)
		}
	}
}

// TestRecoveredCapsAnOversizeMessage: a panic value whose text is longer than
// the field cap is truncated the same way every other logged value is.
func TestRecoveredCapsAnOversizeMessage(t *testing.T) {
	long := strings.Repeat("x", MaxValueBytes+500)
	err := Recovered("tool huge", long)
	if len(err.Error()) > MaxValueBytes+200 {
		t.Fatalf("Recovered did not bound an oversize panic message: len=%d", len(err.Error()))
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("Recovered error = %q, want a truncation marker", err.Error())
	}
}

// TestRecoveredRefusesACredentialShapedPanicMessage: a panic value that
// carries something secret-shaped never reaches the returned error text —
// it is client-visible (an MCP tool error), not just a log field.
func TestRecoveredRefusesACredentialShapedPanicMessage(t *testing.T) {
	err := Recovered("tool chat_inject", errors.New("Bearer sk-ant-api03-PLANTEDPANICSECRET"))
	if strings.Contains(err.Error(), "PLANTEDPANICSECRET") {
		t.Fatalf("Recovered leaked a credential-shaped panic message: %q", err.Error())
	}
	if !strings.Contains(err.Error(), redactedValue) {
		t.Fatalf("Recovered error = %q, want the redacted marker", err.Error())
	}
}
