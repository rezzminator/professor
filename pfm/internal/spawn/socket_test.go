package spawn

import (
	"regexp"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/engine"
)

func TestFreshSocketHonorsTestOverride(t *testing.T) {
	t.Setenv(TestFreshSocketEnv, "cc-fixture")
	if got := FreshSocket(engine.Claude); got != "cc-fixture" {
		t.Fatalf("FreshSocket() = %q, want override", got)
	}
}

func TestFreshSocketUsesEnginePrefix(t *testing.T) {
	t.Setenv(TestFreshSocketEnv, "")
	if got, want := regexp.MustCompile(`\d+`).
		ReplaceAllString(FreshSocket(engine.Codex), "N"),
		"cx-N-N-N"; got != want {
		t.Fatalf("FreshSocket() = %q, want %q", got, want)
	}
}
