package servicemanager

import (
	"context"
	"strings"
	"testing"
)

func TestUserSystemctl(t *testing.T) {
	runner := &scriptedRunner{replies: map[string][]reply{"--user": {exited(3, "inactive")}}}
	answer := UserSystemctl(context.Background(), runner, "is-active", "x.service")
	if got := strings.Join(runner.calls, "|"); got != "systemctl --user is-active x.service" {
		t.Fatalf("ran %q, want systemctl --user is-active x.service", got)
	}
	if got := answer.Command(); got != "systemctl --user is-active x.service" {
		t.Fatalf("Command = %q, want systemctl --user is-active x.service", got)
	}
	if err := answer.Err(); err == nil || err.Error() != "systemctl exited 3: inactive" {
		t.Fatalf("Err = %v, want systemctl exited 3: inactive", err)
	}
}
