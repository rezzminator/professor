package chat

import (
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/store"
)

const (
	ghostSocket     = "cc-1791063475-33847-21156"
	ghostPane       = "%0"
	ghostLaunchID   = "e125ce10-0000-4000-8000-000000000001"
	ghostResumedID  = "57974ea7-0000-4000-8000-000000000002"
	ghostLaunchPID  = 4242
	ghostSeatConfig = "/accounts/2"
)

// ghostLaunchInput is one pfm-launched Claude pane on a non-primary seat:
// argv still names its launch session (--session-id ghostLaunchID), while the
// pane's crumb names the thread it resumed from inside (ghostResumedID). The
// launch session's transcript is given by launch — nil means Claude never
// wrote one (no prompt was ever sent there).
func ghostLaunchInput(crumbID string, launch *store.Transcript) compose.Input {
	transcripts := []store.Transcript{{
		UUID:        ghostResumedID,
		Path:        "/accounts/2/projects/work/" + ghostResumedID + ".jsonl",
		CWD:         "/work",
		CustomTitle: "Pfm doctor update",
		FirstPrompt: "Pfm doctor update",
		PromptCount: 4,
		Size:        400,
		MTimeNS:     2000,
	}}
	if launch != nil {
		transcripts = append(transcripts, *launch)
	}
	return compose.Input{
		Transcripts: transcripts,
		ClaudeSeats: []compose.ClaudeSeat{
			{Account: 1, ConfigDir: "/accounts/1"},
			{Account: 2, ConfigDir: ghostSeatConfig},
		},
		Snapshot: gather.Snapshot{
			Panes: []gather.ProbePane{{
				Socket: ghostSocket, SessionName: "cc-pfm", PaneID: ghostPane, PID: 100,
			}},
			Crumbs: []gather.Crumb{{
				Filename:       ghostSocket + "." + ghostPane,
				Socket:         ghostSocket,
				PaneID:         ghostPane,
				TranscriptPath: "/accounts/2/projects/work/" + crumbID + ".jsonl",
			}},
			ClaudeProcesses: []gather.ClaudeProcess{{
				PID: ghostLaunchPID, PanePID: 100, Socket: ghostSocket, PaneID: ghostPane,
				ConfigDir: ghostSeatConfig,
			}},
			Agents: []gather.Agent{{
				PID: ghostLaunchPID, PanePID: 100, Socket: ghostSocket, PaneID: ghostPane,
				SessionID: ghostLaunchID, ConfigDir: ghostSeatConfig,
			}},
		},
		Options: compose.Options{View: compose.AllView, PrimaryAccount: 1, NowNS: 5000},
	}
}

func rowsOnPane(rows []compose.Row) []compose.Row {
	var onPane []compose.Row
	for index := range rows {
		row := rows[index]
		if row.Socket == ghostSocket && row.PaneID == ghostPane {
			onPane = append(onPane, row)
		}
	}
	return onPane
}

// TestResumedPaneDropsItsAbandonedLaunchThread: a pane launched with
// --session-id A that resumed thread B from inside is one chat. The launch
// session A — empty, and superseded by the crumb the same pane writes for B —
// is no second live row, so the socket resolves to B alone.
func TestResumedPaneDropsItsAbandonedLaunchThread(t *testing.T) {
	for _, launch := range []struct {
		name       string
		transcript *store.Transcript
	}{
		{name: "never written", transcript: nil},
		{name: "written without a prompt", transcript: &store.Transcript{
			UUID: ghostLaunchID, Path: "/accounts/2/projects/work/" + ghostLaunchID + ".jsonl",
		}},
	} {
		t.Run(launch.name, func(t *testing.T) {
			rows := compose.Compose(ghostLaunchInput(ghostResumedID, launch.transcript)).Rows
			onPane := rowsOnPane(rows)
			if len(onPane) != 1 || onPane[0].ID != ghostResumedID {
				t.Fatalf("pane %s %s rows = %#v, want the one resumed thread %s",
					ghostSocket, ghostPane, onPane, ghostResumedID)
			}
			match, found, err := resolve.ResolveRosterName(RosterCandidates(rows), ghostSocket)
			if err != nil || !found || match.ID != ghostResumedID {
				t.Fatalf("resolve %s = %#v found=%v err=%v, want thread %s",
					ghostSocket, match, found, err, ghostResumedID)
			}
		})
	}
}

// TestPaneRunningItsLaunchThreadKeepsIt: the launch session is dropped only
// when the pane's crumb names another thread. A pane still running the very
// session it was launched with keeps exactly that one row.
func TestPaneRunningItsLaunchThreadKeepsIt(t *testing.T) {
	rows := compose.Compose(ghostLaunchInput(ghostLaunchID, nil)).Rows
	onPane := rowsOnPane(rows)
	if len(onPane) != 1 || onPane[0].ID != ghostLaunchID {
		t.Fatalf("pane rows = %#v, want the launch thread %s alone", onPane, ghostLaunchID)
	}
}

// TestPaneKeepsAnotherSessionThatHasPrompts: a second session id on the same
// pane that carries prompts is a real conversation (a headless child, a
// background job), never an abandoned launch, so it keeps its row.
func TestPaneKeepsAnotherSessionThatHasPrompts(t *testing.T) {
	rows := compose.Compose(ghostLaunchInput(ghostResumedID, &store.Transcript{
		UUID: ghostLaunchID, Path: "/accounts/2/projects/work/" + ghostLaunchID + ".jsonl",
		CWD: "/work", FirstPrompt: "child work", PromptCount: 1, Size: 10, MTimeNS: 1000,
	})).Rows
	ids := map[string]bool{}
	for _, row := range rowsOnPane(rows) {
		ids[row.ID] = true
	}
	if len(ids) != 2 || !ids[ghostResumedID] || !ids[ghostLaunchID] {
		t.Fatalf("pane row ids = %v, want both %s and %s", ids, ghostResumedID, ghostLaunchID)
	}
}
