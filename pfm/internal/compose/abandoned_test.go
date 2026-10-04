package compose

import (
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// TestAbandonedLaunch pins the one rule that drops an agent's launch session:
// its own pane's crumb names another thread, and the launch thread is empty.
func TestAbandonedLaunch(t *testing.T) {
	current := &composer{livePaneThreads: map[string]string{
		targetKey("cc-1-1-1", "%0"): "resumed",
	}}
	launch := gather.Agent{Socket: "cc-1-1-1", PaneID: "%0", SessionID: "launch"}
	for _, test := range []struct {
		name       string
		agent      gather.Agent
		transcript store.Transcript
		indexed    bool
		want       bool
	}{
		{name: "superseded, never written", agent: launch, want: true},
		{name: "superseded, written without a prompt", agent: launch, indexed: true, want: true},
		{
			name: "superseded but has prompts", agent: launch, indexed: true,
			transcript: store.Transcript{PromptCount: 1},
		},
		{
			name:  "pane runs its launch thread",
			agent: gather.Agent{Socket: "cc-1-1-1", PaneID: "%0", SessionID: "resumed"},
		},
		{
			name:  "pane carries no crumb",
			agent: gather.Agent{Socket: "cc-2-1-1", PaneID: "%0", SessionID: "launch"},
		},
		{name: "no pane", agent: gather.Agent{SessionID: "launch"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := current.abandonedLaunch(test.agent, test.transcript, test.indexed); got != test.want {
				t.Fatalf("abandonedLaunch = %v, want %v", got, test.want)
			}
		})
	}
}
