package servicemanager

import (
	"context"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
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
	t.Run("namespace", func(t *testing.T) {
		t.Setenv(paths.EnvUnitPrefix, "journey-atlas")
		runner := &scriptedRunner{replies: map[string][]reply{"--user": {exited(0, "active")}}}
		answer := UserSystemctl(context.Background(), runner, "is-active", "pfm-mcp.service")
		if got, want := answer.Command(), "systemctl --user is-active journey-atlas-mcp.service"; got != want {
			t.Fatalf("Command = %q, want %q", got, want)
		}
	})
	t.Run("empty prefix", func(t *testing.T) {
		t.Setenv(paths.EnvUnitPrefix, "")
		answer := UserSystemctl(context.Background(), runner, "is-active", "pfm-mcp.service")
		want := `PFM_UNIT_PREFIX must be 1..63 lowercase letters, digits or hyphens, starting with a letter (got "")`
		if err := answer.Err(); err == nil || err.Error() != want {
			t.Fatalf("Err = %v, want %q", err, want)
		}
	})
}
