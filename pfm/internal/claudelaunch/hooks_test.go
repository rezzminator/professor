package claudelaunch

import (
	"strings"
	"testing"
)

func TestHookTemplates(t *testing.T) {
	home := t.TempDir()
	hooks := HookTemplates(home)
	if len(hooks) != 18 {
		t.Fatalf("registrations=%d, want 18", len(hooks))
	}
	callmeter := 0
	for _, hook := range hooks {
		if !strings.HasPrefix(hook.Command, home+"/.local/bin/pfm ") {
			t.Errorf("command=%q", hook.Command)
		}
		if hook.Name == "callmeter" {
			callmeter++
			if !hook.Async {
				t.Errorf("callmeter registration %s is synchronous", hook.Event)
			}
		}
	}
	if callmeter != 7 {
		t.Errorf("callmeter registrations=%d", callmeter)
	}
}
