package mcpserv

import (
	"strings"
	"testing"

	"hostops/pfm/internal/inject"
)

// chat_self_compact's ambient-identity remedy must name its actual CLI twin
// — `pfm chat self-compact` (Task D) — rather than the generic
// noAmbientCallerRemedy's "run the equivalent `pfm chat ...` command", which
// never says which subcommand, or the pre-Task-D recipe that told the reader
// to CLI-inject a live /compact into its own pane (exactly the choreography
// that caused the 2026-09-03 eaten-draft incident).
func TestSelfCompactAmbientRefusalNamesAWorkingRemedy(t *testing.T) {
	service := metadataIdentityService(t)
	protocol := connectInMemory(t, service.Server())
	refused := callToolWithMeta[InjectOutput](
		t, protocol.clientSession, "chat_self_compact", nil,
		map[string]any{"focus": "hold the goal", "then": "resume the wave"},
	)
	if refused.Status != "not_found" || refused.Code != inject.CodeUnknown {
		t.Fatalf("metadata-free chat_self_compact = %+v", refused)
	}
	if !strings.Contains(refused.Message, "pfm chat self-compact") {
		t.Fatalf("refusal does not name the self-compact CLI twin: %q", refused.Message)
	}
	if strings.Contains(refused.Message, "equivalent `pfm chat ...` command") {
		t.Fatalf("refusal still carries the generic dangling-pointer wording: %q", refused.Message)
	}
	if strings.Contains(refused.Message, "chat_self_compact has no") {
		t.Fatalf("refusal still claims no CLI twin exists: %q", refused.Message)
	}
	if strings.Contains(refused.Message, "chat inject") ||
		strings.Contains(refused.Message, "--force-now") ||
		strings.Contains(refused.Message, "$(pfm whoami)") {
		t.Fatalf("refusal still names the retired inject-a-live-/compact recipe: %q", refused.Message)
	}
	// stdlib flag parsing stops at the first positional — every flag must
	// precede the <focus> placeholder, or it folds into the focus text.
	command := "pfm chat self-compact "
	commandStart := strings.Index(refused.Message, command)
	if commandStart == -1 {
		t.Fatalf("refusal does not contain the self-compact command: %q", refused.Message)
	}
	thenIndex := strings.Index(refused.Message[commandStart:], "--then")
	focusIndex := strings.Index(refused.Message[commandStart:], "'<focus>'")
	if thenIndex == -1 || focusIndex == -1 {
		t.Fatalf("refusal is missing an expected token: %q", refused.Message)
	}
	if thenIndex >= focusIndex {
		t.Fatalf("refusal places --then after the positional focus, breaking stdlib flag parsing: %q", refused.Message)
	}
}
