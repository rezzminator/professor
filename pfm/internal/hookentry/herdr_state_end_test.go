package hookentry

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

func TestHerdrStateEndAfterTmuxServerGone(t *testing.T) {
	for _, event := range []string{"end", "working"} {
		t.Run(event, func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "cc-gone")
			var stderr bytes.Buffer
			var logged []string
			deps := herdrDeps{
				getenv: func(k string) string {
					return map[string]string{"TMUX": socket + ",1,0", "TMUX_PANE": "%0", "HERDR_BIN_PATH": "herdr"}[k]
				},
				// the error runTmuxForHerdr returns once the chat's tmux server is gone
				tmux: func(_ context.Context, socket string, args ...string) (string, error) {
					return "", fmt.Errorf(
						"tmux %s: exit status 1: no server running on %s",
						strings.Join(args, " "),
						socket,
					)
				},
				listPanes: listTmuxPanesForHerdr,
				now:       time.Now,
				clock:     clock.NewFake(time.Now()),
				logError:  func(_ string, _ time.Time, message string) error { logged = append(logged, message); return nil },
			}
			rc := reportToHerdr([]string{event}, strings.NewReader(`{"session_id":"s1"}`), &stderr, deps)
			got := strings.ReplaceAll(fmt.Sprintf("%d %q %s", rc, logged, &stderr), socket, "{socket}")
			if want := map[string]string{"end": "0 [] INFO pfm internal herdr-state end: tmux server {socket} already gone; foreground client exited: read tmux session of %0: tmux display-message -p -t %0 #{session_name}\t#{pane_pid}: exit status 1: no server running on {socket}\n", "working": "0 [\"pfm internal herdr-state working: read tmux session of %0: tmux display-message -p -t %0 #{session_name}\\t#{pane_pid}: exit status 1: no server running on {socket} (fail-open)\"] pfm internal herdr-state working: read tmux session of %0: tmux display-message -p -t %0 #{session_name}\t#{pane_pid}: exit status 1: no server running on {socket} (fail-open)\n"}[event]; got != want {
				t.Errorf("rc errors stderr = %q, want %q", got, want)
			}
		})
	}
}

func TestHerdrStatePayloadLimitIsExplicit(t *testing.T) {
	var stderr bytes.Buffer
	deps := herdrDeps{now: time.Now}
	input := strings.NewReader(`{"tool_response":"` + strings.Repeat("x", 64<<20) + `","agent_id":"child"}`)
	before := input.Len()
	if rc := reportToHerdr(
		[]string{"working"},
		input,
		&stderr,
		deps,
	); rc != 0 ||
		!strings.Contains(stderr.String(), "exceeds") {
		t.Fatalf("over-limit input: rc=%d stderr=%s", rc, &stderr)
	}
	if read := before - input.Len(); read != (64<<20)+1 {
		t.Fatalf("hook consumed %d bytes, want exactly the budget plus one probe byte", read)
	}
}
