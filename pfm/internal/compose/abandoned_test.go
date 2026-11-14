package compose

import (
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestAbandonedLaunch pins the one rule that drops an agent's launch session:
// its own pane's crumb names another thread, and the launch thread is empty.
func TestAbandonedLaunch(t *testing.T) {
	current := &composer{livePaneThreads: map[string]string{
		targetKey("cc-1-1-1", "%0"): "resumed",
	}, input: Input{HerdrSeats: []gather.HerdrSeat{
		{
			Seat: fleetdb.Seat{
				Backend:     herdrBackend,
				State:       "live",
				Engine:      "cc",
				SeatHostPID: 50,
				SessionID:   "resumed",
			},
		},
		{
			Seat: fleetdb.Seat{
				Backend:     herdrBackend,
				State:       "live",
				Engine:      "cc",
				SeatHostPID: 60,
				SessionID:   "first",
			},
			Continued: "next",
		},
		{
			Seat: fleetdb.Seat{
				Backend:     herdrBackend,
				State:       "lost",
				Engine:      "cc",
				SeatHostPID: 70,
				SessionID:   "resumed",
			},
		},
		{
			Seat: fleetdb.Seat{
				Backend:     herdrBackend,
				State:       "live",
				Engine:      "cc",
				SeatHostPID: 80,
				SessionID:   "resumed",
			},
			Err: errors.New("herdr socket unreachable"),
		},
	}}}
	launch := gather.Agent{Socket: "cc-1-1-1", PaneID: "%0", SessionID: "launch"}
	for _, test := range []struct {
		name       string
		agent      gather.Agent
		transcript store.Transcript
		indexed    bool
		want       bool
	}{
		{name: "superseded, never written", agent: launch, want: true},
		{name: "superseded, written without a prompt", agent: launch, indexed: true, want: true},
		{
			name: "superseded but has prompts", agent: launch, indexed: true,
			transcript: store.Transcript{PromptCount: 1},
		},
		{
			// /clear or /resume inside the pane's own Claude: that one
			// process runs the crumb's thread now, so the launch id in its
			// argv names a conversation nothing runs, prompts or not.
			name:    "the pane's own process moved on from a launch thread with prompts",
			agent:   gather.Agent{PID: 7, PanePID: 7, Socket: "cc-1-1-1", PaneID: "%0", SessionID: "launch"},
			indexed: true, transcript: store.Transcript{PromptCount: 3}, want: true,
		},
		{
			name:    "a child process in the pane keeps a launch thread with prompts",
			agent:   gather.Agent{PID: 8, PanePID: 7, Socket: "cc-1-1-1", PaneID: "%0", SessionID: "launch"},
			indexed: true, transcript: store.Transcript{PromptCount: 1},
		},
		{
			name:  "pane runs its launch thread",
			agent: gather.Agent{Socket: "cc-1-1-1", PaneID: "%0", SessionID: "resumed"},
		},
		{
			name:  "pane carries no crumb",
			agent: gather.Agent{Socket: "cc-2-1-1", PaneID: "%0", SessionID: "launch"},
		},
		{name: "no pane", agent: gather.Agent{SessionID: "launch"}},
		{
			// An in-app /resume in a Herdr pane: the seat names the resumed thread, the engine's argv the launch.
			name:    "a native seat's engine moved on from its launch thread",
			agent:   gather.Agent{PID: 9, ParentPID: 50, SessionID: "launch"},
			indexed: true, transcript: store.Transcript{PromptCount: 3}, want: true,
		},
		{name: "a native seat's engine runs the seat's thread", agent: gather.Agent{PID: 9, ParentPID: 50, SessionID: "resumed"}},
		{
			name:  "a native seat's engine runs the segment its chat continued in",
			agent: gather.Agent{PID: 9, ParentPID: 60, SessionID: "next"},
		},
		{
			name:  "a native seat's engine still on the segment its chat left",
			agent: gather.Agent{PID: 9, ParentPID: 60, SessionID: "first"}, want: true,
		},
		{name: "a lost seat's host", agent: gather.Agent{PID: 9, ParentPID: 70, SessionID: "launch"}},
		// A seat whose probe failed proves no host alive: its recorded pid may be reused.
		{name: "an unproven seat's host", agent: gather.Agent{PID: 9, ParentPID: 80, SessionID: "launch"}},
		{name: "beneath no seat host", agent: gather.Agent{PID: 9, ParentPID: 51, SessionID: "launch"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := current.abandonedLaunch(test.agent, test.transcript, test.indexed); got != test.want {
				t.Fatalf("abandonedLaunch = %v, want %v", got, test.want)
			}
		})
	}
}

// TestBootingPaneHoldsItsLaunchedClaudeOnce: a Claude pfm launched carries its session in argv from birth,
// so until its statusline first renders (held at the folder-trust dialog, say) gather finds its process
// both crumbless-live and as an agent. The pane is one chat, its booting row; its session makes no second
// live row, which would leave its name matching two chats.
func TestBootingPaneHoldsItsLaunchedClaudeOnce(t *testing.T) {
	const session = "6f0c2a8e-0000-4000-8000-000000000001"
	input := Input{
		Snapshot: gather.Snapshot{
			CrumblessLive: []gather.CrumblessLive{{
				Socket: "cc-100-1-1", SessionName: "cc-100-1-1", WindowName: "trust-held",
				PaneID: "%0", PID: 900, CWD: testjail.Place("/work/fresh"),
			}},
			Agents: []gather.Agent{{
				PID: 900, PanePID: 800, Socket: "cc-100-1-1", PaneID: "%0", SessionID: session,
				ConfigDir: testjail.Place("/accounts/1"),
			}},
		},
		Options: Options{View: AllView, CurrentDir: testjail.Place("/work/projd")},
	}
	output := Compose(input)
	var onPane []string
	for index := range output.Rows {
		if row := output.Rows[index]; row.Socket == "cc-100-1-1" || row.ID == session {
			onPane = append(onPane, fmt.Sprintf("%v %s", row.Kind, row.ID))
		}
	}
	if want := []string{fmt.Sprintf("%v cc-100-1-1", Booting)}; !slices.Equal(onPane, want) {
		t.Fatalf("rows of the booting pane = %q, want %q", onPane, want)
	}
}
