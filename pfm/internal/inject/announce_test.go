package inject

import (
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestWaitingForNamesTheChainedHop(t *testing.T) {
	const suffix = " — no steady-idle fallback: an unobserved boundary leaves the steer undelivered"
	for _, test := range []struct {
		name   string
		engine string
		want   string
	}{
		{"codex", string(pfmengine.Codex), "engine codex · the current turn to end" + suffix},
		{"claude name", "claude", "the current turn to end"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := WaitingFor(test.engine); got != test.want {
				t.Fatalf("WaitingFor(%q) = %q, want %q", test.engine, got, test.want)
			}
		})
	}
}
