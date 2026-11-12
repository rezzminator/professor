package chat

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// A `claude --resume` run in a Herdr pane, the line Herdr's restore types,
// rebinds its session's native seat only when that seat's supervisor is gone:
// a session with no native seat, or with a live one, births as it did before.
func TestResumeRestoredSeatTakesOnlyADeadSeat(t *testing.T) {
	const session = "6fafd8a7-5059-4288-bf58-23e60c5fdb88"
	for _, test := range []struct {
		name, state, wantStderr string
		alive, handled          bool
		code                    int
	}{
		{name: "a session with no native seat births"},
		{name: "a live supervisor keeps its seat", state: "live", alive: true},
		{
			name: "a dead supervisor's seat is rebound", state: "lost", handled: true, code: 1,
			wantStderr: "pfm chat open: account 1: {home}/.cc/1 does not exist — run pfm install\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			testjail.Fleet(t)
			socket := nativeChatSocket(t, func(req nativeChatRequest) (any, string) {
				if value, code, ok := nativeChatProof(req, "w6:p3"); ok {
					return value, code
				}
				return nil, "unexpected_method"
			})
			runtime, db, seat := nativeChatSeat(t, socket, "lost")
			if test.state == "" {
				if err := db.DeleteSeat(context.Background(), seat.SeatID); err != nil {
					t.Fatal(err)
				}
			} else {
				nativeChatHost(t, db, seat, test.alive)
			}
			var stdout, stderr bytes.Buffer
			handled, code := ResumeRestoredSeat(context.Background(), session, &stdout, &stderr, runtime)
			// The rebound open is OpenRestoredID's: this fixture has no account, which it names first.
			got := strings.ReplaceAll(stderr.String(), runtime.Paths.Home, "{home}")
			if handled != test.handled || code != test.code || got != test.wantStderr {
				t.Fatalf("ResumeRestoredSeat(%s) = handled %v, code %d, stderr %q; want %v, %d, %q",
					session, handled, code, got, test.handled, test.code, test.wantStderr)
			}
		})
	}
}
