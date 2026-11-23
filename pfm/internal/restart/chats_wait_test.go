package restart

import (
	"context"
	"errors"
	"fmt"
	"testing"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/headless"
)

// TestChatBackWaitsWithinOneSharedDeadline: every chat the restart brings
// back is polled until it is live again, all within one deadline; a read that
// fails is what was last seen, never the end of the wait, a restored launch
// that failed ends it at once, and a chat polled once the deadline is spent
// gets its one look. Once the Herdr server start
// failed, Herdr restored nothing, so a chat fails at once, never polled.
func TestChatBackWaitsWithinOneSharedDeadline(t *testing.T) {
	polls := map[string]int{}
	fleet := &fakeFleet{back: func(chat headless.Chat, socket string) (bool, string, error) {
		polls[chat.ID]++
		if socket != "/run/herdr.sock" {
			return false, "", fmt.Errorf("polled on %s", socket)
		}
		switch chat.ID {
		case "sess-slow":
			return polls[chat.ID] == 3, fmt.Sprintf("seat seat-slow poll %d", polls[chat.ID]), nil
		case "sess-unreadable":
			return false, "", errors.New("database is locked")
		case "sess-late":
			return true, "seat seat-late live", nil
		case "sess-failed":
			return false, "", fmt.Errorf("%w: seat seat-failed: the engine exited 1", pfmchat.ErrRestartFailed)
		}
		return false, "seat " + chat.SeatID + " reads lost", nil
	}}
	started := &herdrStarted{}
	wait := &chatWait{tools: serviceTools{clock: newTickClock()}, fleet: fleet, herdr: started}
	before := pfmchat.RestartSeatHost{PID: 41, Start: 7}
	for _, row := range []struct {
		id         string
		herdrFails bool
		want       string
		err        string
		polls      int
	}{
		{id: "sess-slow", want: "live again: seat seat-slow poll 3", polls: 3},
		{id: "sess-never", polls: 999, err: "not live again within 1m40s of the Herdr server start " +
			"(last seen: seat seat-never reads lost); reopen it by hand: pfm chat open sess-never"},
		{id: "sess-unreadable", polls: 1, err: "not live again within 1m40s of the Herdr server start " +
			"(last seen: database is locked); reopen it by hand: pfm chat open sess-unreadable"},
		{id: "sess-late", want: "live again: seat seat-late live", polls: 1},
		{id: "sess-failed", polls: 1, err: "its restored launch failed: seat seat-failed: the engine exited 1; " +
			"reopen it by hand: pfm chat open sess-failed"},
		{id: "sess-herdr-down", herdrFails: true, err: "the Herdr server did not start again, so Herdr restored " +
			"nothing; reopen it by hand: pfm chat open sess-herdr-down"},
	} {
		if row.herdrFails {
			start := started.track(func(context.Context) (string, error) {
				return "", errors.New("herdr server pid 9 not running 10s after start")
			})
			if _, err := start(context.Background()); err == nil {
				t.Fatal("the tracked Herdr start hid its failure")
			}
		}
		got, err := wait.back(context.Background(), headless.Chat{ID: row.id, SeatID: "seat-" + row.id[5:]},
			"/run/herdr.sock", before)
		gotErr := ""
		if err != nil {
			gotErr = err.Error()
		}
		if got != row.want || gotErr != row.err || polls[row.id] != row.polls {
			t.Fatalf("%s: back = %q, err %q, %d poll(s)\nwant %q, err %q, %d poll(s)",
				row.id, got, gotErr, polls[row.id], row.want, row.err, row.polls)
		}
	}
}
