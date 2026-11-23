package restart

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// TestAChatUnitComesBackOnlyWhenHerdrCanRestoreIt: each chat's step is the
// chat package's own end (EndHow) under --no-restore, and otherwise too
// unless Herdr's restore can bring it back, the reason named; a chat it can
// is left for the Herdr server stop and verified back after the start.
func TestAChatUnitComesBackOnlyWhenHerdrCanRestoreIt(t *testing.T) {
	herdrChat := headless.Chat{
		Engine: pfmengine.Claude, Name: "native", ID: "sess-native", Backend: spawn.BackendHerdr, SeatID: "seat-1",
		Socket: "/run/herdr.sock", Pane: "w1:p2",
	}
	tmuxChat := headless.Chat{
		Engine: pfmengine.Codex,
		Name:   "builder",
		ID:     "sess-build",
		Socket: "pfm-cx-builder",
		Pane:   "%1",
	}
	otherServer := herdrChat
	otherServer.Socket = "/run/other.sock"
	const (
		herdrEnd = "HERDR_END"
		tmuxEnd  = "TMUX_END"
		running  = `{"server":{"running":true,"socket":"/run/herdr.sock"}}`
	)
	for _, row := range []struct {
		name          string
		noRestore     bool
		chat          headless.Chat
		onPath        bool
		lookErr       error
		status        string
		resume        bool
		resumeErr     error
		notRestorable string
		restorableErr error
		launched      bool
		stopHow       string
		notBack       string
	}{
		{
			name: "no restore ends a Herdr chat", noRestore: true, chat: herdrChat, onPath: true, status: running,
			resume: true, stopHow: herdrEnd,
		},
		{name: "no restore ends a tmux chat", noRestore: true, chat: tmuxChat, stopHow: tmuxEnd},
		{
			name: "a tmux chat", chat: tmuxChat, stopHow: tmuxEnd,
			notBack: "it runs in tmux, and only a chat in a Herdr pane comes back with Herdr's restore",
		},
		{
			name: "herdr not on PATH", chat: herdrChat, resume: true, stopHow: herdrEnd,
			notBack: "herdr is not on PATH, so no Herdr server restarts",
		},
		{
			name: "herdr unresolvable", chat: herdrChat, lookErr: errors.New("permission denied"), resume: true,
			stopHow: herdrEnd, notBack: "the Herdr server could not be read: resolve herdr: permission denied",
		},
		{
			name: "herdr status unreadable", chat: herdrChat, onPath: true, status: "", resume: true, stopHow: herdrEnd,
			notBack: "the Herdr server could not be read: herdr status --json: exit 1: no socket",
		},
		{
			name: "no Herdr server", chat: herdrChat, onPath: true, resume: true, stopHow: herdrEnd,
			status:  `{"server":{"running":false,"socket":""}}`,
			notBack: "no Herdr server is running, so none restarts",
		},
		{
			name: "another Herdr server", chat: otherServer, onPath: true, status: running, resume: true, stopHow: herdrEnd,
			notBack: "it runs on Herdr server /run/other.sock, not /run/herdr.sock, the one this restart restarts",
		},
		{
			name: "Herdr restore off", chat: herdrChat, onPath: true, status: running, resume: false, stopHow: herdrEnd,
			notBack: "Herdr's resume_agents_on_restore is off, so its pane would come back without the chat",
		},
		{
			name: "Herdr restore unreadable", chat: herdrChat, onPath: true, status: running,
			resumeErr: errors.New("parse config.toml: bare keys"), stopHow: herdrEnd,
			notBack: "whether Herdr resumes agents on restore could not be read: parse config.toml: bare keys",
		},
		{
			name: "seat cannot resume", chat: herdrChat, onPath: true, status: running, resume: true, stopHow: herdrEnd,
			notRestorable: "its seat seat-1 has an end pending", notBack: "its seat seat-1 has an end pending",
		},
		{
			name: "seat unreadable", chat: herdrChat, onPath: true, status: running, resume: true, stopHow: herdrEnd,
			restorableErr: errors.New("database is locked"),
			notBack:       "whether its seat can resume could not be read: database is locked",
		},
		{
			name: "brought back", chat: herdrChat, onPath: true, status: running, resume: true,
			stopHow: "left running: the Herdr server stop hangs up its pane, which Herdr keeps in its saved session " +
				"with the resume pfm registered",
		},
		{
			name: "the chat that typed the restart brought back", chat: herdrChat, onPath: true, status: running,
			resume: true, launched: true,
			stopHow: "left running: the Herdr server stop hangs up its pane, which Herdr keeps in its saved session " +
				"with the resume pfm registered",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			runner := &hostRunner{
				paths:   map[string]string{},
				lookErr: map[string]error{"herdr": row.lookErr},
				handle: func([]string) (deps.RunResult, error) {
					if row.status == "" {
						return exitWith(1, "no socket")
					}
					return answer(row.status)
				},
			}
			if row.onPath {
				runner.paths["herdr"] = "/opt/bin/herdr"
			}
			planned := pfmchat.RestartSeatHost{PID: 41, Start: 7, Birth: 3}
			fleet := &fakeFleet{
				chats:         []headless.Chat{row.chat},
				notRestorable: map[string]string{row.chat.ID: row.notRestorable},
				restorableErr: map[string]error{row.chat.ID: row.restorableErr},
				hosts:         map[string]pfmchat.RestartSeatHost{row.chat.ID: planned},
			}
			dependencies := testDeps(t, runner, newKernel(), fleet)
			dependencies.NoRestore = row.noRestore
			dependencies.ResumeOnRestore = func() (bool, error) { return row.resume, row.resumeErr }
			if row.launched {
				dependencies.LaunchedFrom = chatAddress{Socket: row.chat.Socket, Pane: row.chat.Pane}
			}
			finder := &discoverer{deps: dependencies}
			finder.findChats(context.Background())
			if len(finder.plan.Units) != 1 {
				t.Fatalf("plan holds %d units, want the one chat", len(finder.plan.Units))
			}
			unit := &finder.plan.Units[0]
			want := map[string]string{
				herdrEnd: "Herdr pane.close of the proved seat; verified: the seat is retired; not brought back",
				tmuxEnd:  "tmux kill-server on pfm-cx-builder; verified: tmux says the server is dead; not brought back",
			}[row.stopHow]
			found := "fleet scan: tmux socket pfm-cx-builder pane %1"
			if row.chat.Backend == spawn.BackendHerdr {
				found = "fleet scan: Herdr seat seat-1"
			}
			if row.launched {
				found += " (the chat that typed pfm restart)"
			}
			if unit.FoundBy != found {
				t.Fatalf("found by %q, want %q", unit.FoundBy, found)
			}
			if want == "" {
				want = row.stopHow
			} else if row.notBack != "" {
				want += ": " + row.notBack
			}
			if unit.StopHow != want || unit.Chat.NotBack != row.notBack {
				t.Fatalf("stop = %q, not back = %q\nwant %q, %q", unit.StopHow, unit.Chat.NotBack, want, row.notBack)
			}
			stopped, err := unit.Stop(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if row.stopHow != herdrEnd && row.stopHow != tmuxEnd {
				wantStart := "Herdr restores pane w1:p2 and runs its resume (pfm chat open --restored); " +
					"verified: seat seat-1 reads live on /run/herdr.sock with a new running seat host"
				if stopped != "left running for the Herdr server stop" || len(fleet.ended) != 0 ||
					!unit.restarts() || unit.StartHow != wantStart || unit.Before != "live in Herdr pane w1:p2" {
					t.Fatalf("a chat brought back: stop %q, ended %v, restarts %v, start %q, before %q",
						stopped, fleet.ended, unit.restarts(), unit.StartHow, unit.Before)
				}
				// Its start judges it back against the seat host its seat named at plan time.
				started, err := unit.Start(context.Background())
				if err != nil || started != "live again: seat seat-1 live" ||
					!slices.Equal(fleet.before, []pfmchat.RestartSeatHost{planned}) {
					t.Fatalf("a chat brought back: start %q, %v, polled with host %+v; want %q, host %+v",
						started, err, fleet.before, "live again: seat seat-1 live", planned)
				}
				return
			}
			if stopped != "ended" || !slices.Equal(fleet.ended, []string{row.chat.ID}) || unit.restarts() {
				t.Fatalf("an ended chat: stop %q, ended %v, restarts %v", stopped, fleet.ended, unit.restarts())
			}
		})
	}
}

// TestChatBackWaitsWithinOneSharedDeadline: every chat the restart brings
// back is polled until it is live again, all within one deadline; a read that
// fails is what was last seen, never the end of the wait, and a chat polled
// once the deadline is spent gets its one look. Once the Herdr server start
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
		{id: "sess-never", polls: 899, err: "not live again within 1m30s of the Herdr server start " +
			"(last seen: seat seat-never reads lost); reopen it by hand: pfm chat open sess-never"},
		{id: "sess-unreadable", polls: 1, err: "not live again within 1m30s of the Herdr server start " +
			"(last seen: database is locked); reopen it by hand: pfm chat open sess-unreadable"},
		{id: "sess-late", want: "live again: seat seat-late live", polls: 1},
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
