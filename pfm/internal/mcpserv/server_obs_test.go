package mcpserv

import (
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestRegisteredToolsRecordUnderTheMCPComponent proves every mcp.AddTool
// registration in register() is wrapped by obs.Tool: a real in-process call
// writes exactly one mcp.call record under the mcp component, tool and kind
// named, never the excerpt itself.
func TestRegisteredToolsRecordUnderTheMCPComponent(t *testing.T) {
	setupBackendFixture(t)
	_, recorder := obs.Test(t)
	service := newFixtureService(t)
	defer func() {
		if err := service.Close(); err != nil {
			t.Errorf("close service: %v", err)
		}
	}()
	client := connectInMemory(t, service.Server())
	_ = callTool[FindOutput](t, client.clientSession, "chat_find", FindInput{Excerpt: "alpha unique"})
	var found *obs.Record
	for _, record := range recorder.Records() {
		if record.Message == "mcp.call" {
			found = &record
		}
	}
	if found == nil {
		t.Fatalf("no mcp.call record: %s", recorder.Raw())
	}
	for key, want := range map[string]any{obs.FieldComp: "mcp", "kind": "tool", "tool": "chat_find"} {
		if got, _ := found.Field(key); got != want {
			t.Fatalf("mcp.call record %s = %v, want %v: %v", key, got, want, found.Fields)
		}
	}
	if strings.Contains(recorder.Raw(), "alpha unique") {
		t.Fatalf("the excerpt value reached the activity log: %s", recorder.Raw())
	}
}
