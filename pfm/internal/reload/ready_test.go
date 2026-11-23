package reload

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
			_, err := runHerdrOnFake(t, context.Background(), f.request, f.options, &stderr)
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

// rebornShell is the incident frame: Herdr's restore typed the old chat's
// resume line into a shell, so once the fullscreen Claude left, the pane showed
// that shell line while Herdr still reported a settled agent.
const rebornShell = "~/work on develop\n❯ claude --resume 02c1b522-4536-4346-9d9f-c435bd370bca"

// The reborn --then wait judges only a frame the reborn Claude drew: a shell
// line is never its input box, a draft is polled to the bound, and a follow-up
// the reload could not type stays stored for the chat's first prompt.
func TestRunHerdrThenReadsOnlyTheRebornFrame(t *testing.T) {
	const then = "Read the handoff in FULL and continue from its Next steps"
	for _, tc := range []struct {
		name string
		// frames are served one per pane read after /exit; the last one stays.
		frames []string
		want   string
	}{
		{name: "shell-line-then-empty-box", frames: []string{rebornShell, rebornShell, "❯ "}},
		{
			name: "bare-shell-prompt-never-counts", frames: []string{"~/work on develop\n❯ "},
			want: "native reload --then: native reload: chat still busy or input box not ready; " +
				"the --then message was not sent",
		},
		{
			name: "draft-held-through-bound", frames: []string{"❯ parked human draft"},
			want: `native reload --then: native reload: composer holds a draft: "parked human draft"; ` +
				"nothing was typed and the draft is preserved (dim placeholder text is exempt)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReloadHerdrFixture(t)
			f.request.Then = then
			backstop := filepath.Join(f.options.SIDDir, "reload-herdr-"+f.seat.SeatID+".then")
			storedAtExit, agentReadsAtExit := false, 0
			var serve func()
			serve = func() {
				f.text = tc.frames[0]
				if len(tc.frames) > 1 {
					tc.frames = tc.frames[1:]
					f.onRead = serve
				}
			}
			f.onExit = func() {
				_, err := os.Stat(backstop)
				storedAtExit, agentReadsAtExit = err == nil, f.agentReads
				f.onRead = serve
			}
			var stderr strings.Builder
			_, err := runHerdrOnFake(t, context.Background(), f.request, f.options, &stderr)
			if tc.want == "" && (err != nil || f.prompted != then) {
				t.Fatalf("RunHerdr error=%v prompted=%q, want %q delivered", err, f.prompted, then)
			}
			if tc.want != "" {
				if err == nil || err.Error() != tc.want || f.prompted != "" {
					t.Fatalf("RunHerdr error=%v prompted=%q, want %q and nothing prompted", err, f.prompted, tc.want)
				}
				if polled := f.agentReads - agentReadsAtExit; polled != f.options.IdleTries {
					t.Fatalf("reborn wait read Herdr %d times, want the whole bound of %d", polled, f.options.IdleTries)
				}
			}
			if !storedAtExit {
				t.Fatalf("--then message not stored in %s before /exit", backstop)
			}
			stored, readErr := os.ReadFile(backstop)
			if tc.want == "" {
				if !errors.Is(readErr, os.ErrNotExist) {
					t.Fatalf("backstop %s = %q (%v) after delivery, want it removed", backstop, stored, readErr)
				}
				return
			}
			want := `{"socket":"` + f.request.SocketPath + `","pane":"w6:p3","session":"` +
				f.request.SessionID + `","message":"` + then + `"}`
			if readErr != nil || string(stored) != want {
				t.Fatalf("backstop %s = %q (%v), want %q", backstop, stored, readErr, want)
			}
			notice := "pfm chat reload: the --then message is kept in " + backstop + " for the chat's first prompt\n"
			if !strings.HasSuffix(stderr.String(), notice) {
				t.Fatalf("RunHerdr stderr = %q, want it to end with %q", stderr.String(), notice)
			}
		})
	}
}
