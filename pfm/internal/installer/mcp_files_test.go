package installer

import "testing"

func TestEnabledMCPNames(t *testing.T) {
	if names := enabledMCPNames(map[string]bool{"chat": true}); len(names) != 1 || names[0] != chatRegistrationName {
		t.Fatalf("enabled names=%v", names)
	}
	if names := enabledMCPNames(map[string]bool{"external": true}); len(names) != 0 {
		t.Fatalf("unregistered names=%v", names)
	}
}
