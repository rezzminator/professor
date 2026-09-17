package spawn

import (
	"strings"
	"testing"

	"hostops/pfm/internal/engine"
)

func TestFreshSocketHonorsTestOverride(t *testing.T) {
	t.Setenv(TestFreshSocketEnv, "cc-fixture")
	if got := FreshSocket(engine.Claude); got != "cc-fixture" {
		t.Fatalf("FreshSocket() = %q, want override", got)
	}
}

func TestFreshSocketUsesEnginePrefix(t *testing.T) {
	t.Setenv(TestFreshSocketEnv, "")
	if got := FreshSocket(engine.Codex); !strings.HasPrefix(got, "cx-") {
		t.Fatalf("FreshSocket() = %q, want cx- prefix", got)
	}
}
