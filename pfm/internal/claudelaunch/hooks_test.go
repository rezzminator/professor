package claudelaunch

import (
	"strings"
	"testing"
)

func TestHookTemplates(t *testing.T) {
	home := t.TempDir()
	hooks := HookTemplates(home)
	if len(hooks) != 11 {
		t.Fatalf("registrations=%d, want 11", len(hooks))
	}
	for _, hook := range hooks {
		if !strings.HasPrefix(hook.Command, home+"/.local/bin/pfm ") {
			t.Errorf("command=%q", hook.Command)
		}
	}
}
