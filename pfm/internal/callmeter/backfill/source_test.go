package backfill

import (
	"path/filepath"
	"testing"
)

// TestSourceOf: a subagents/agent-{id}.jsonl is that sub-agent of the session
// whose directory holds it; any other transcript is a chat named by its file.
func TestSourceOf(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "tmp", "demo-config", "projects", "-tmp-demo-proj")
	chat := filepath.Join(root, "sess-1.jsonl")
	agent := filepath.Join(root, "sess-1", "subagents", "agent-a1.jsonl")
	if got := sourceOf(chat); got.session != "sess-1" || got.agentID != "" || got.configDir != "/tmp/demo-config" {
		t.Errorf("sourceOf(chat) = %+v", got)
	}
	if got := sourceOf(agent); got.session != "sess-1" || got.agentID != "a1" {
		t.Errorf("sourceOf(agent) = %+v", got)
	}
}
