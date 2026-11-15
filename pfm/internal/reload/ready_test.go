package reload

import (
	"context"
	"strings"
	"testing"
)

// The reborn Claude starts before Herdr registers it: --then waits for the
// registration; before /exit the same refusal means the target is gone.
func TestRunHerdrReadyAgentRegistration(t *testing.T) {
	const refused = "native reload read agent: herdr refused agent.get pfm-seat: agent_not_found: " +
		"agent target w6:p3 not found"
	for _, tc := range []struct {
		name, mode, want string
		then             bool
	}{
		{name: "then-waits-for-agent-registration", mode: "agent-not-found-after-reboot", then: true},
		{
			name: "then-agent-never-registers", mode: "agent-never-registers", then: true,
			want: "native reload --then: native reload: Herdr never reported the chat's state (" + refused +
				"); the --then message was not sent",
		},
		{name: "unregistered-target-refuses-exit", mode: "agent-not-found-before-exit", want: refused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReloadHerdrFixture(t)
			f.mode = tc.mode
			if tc.then {
				f.request.Then = "resume the port"
			}
			var stderr strings.Builder
			_, err := RunHerdr(context.Background(), f.request, f.options, &stderr)
			if tc.want != "" {
				if err == nil || err.Error() != tc.want {
					t.Fatalf("RunHerdr error=%v, want %q", err, tc.want)
				}
				if tc.mode == "agent-not-found-before-exit" && (f.submitted || len(f.typed) != 0) {
					t.Fatalf("refused target: submitted=%t typed=%q, want nothing typed", f.submitted, f.typed)
				}
				return
			}
			if err != nil {
				t.Fatalf("RunHerdr: %v", err)
			}
			if !f.registered || f.prompted != f.request.Then {
				t.Fatalf(
					"registered=%t prompted=%q, want one refusal then %q",
					f.registered,
					f.prompted,
					f.request.Then,
				)
			}
		})
	}
}
