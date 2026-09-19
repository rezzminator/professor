package compose

import (
	"testing"

	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/store"
)

func openCodeLiveInput(seats []gather.LiveOpenCode, panes []gather.ProbePane) Input {
	return Input{
		Snapshot:         gather.Snapshot{Panes: panes, OpenCode: seats},
		OpenCodeSessions: openCodeFixture(),
		Options:          Options{View: AllView},
	}
}

func openCodePane(socket, paneID, cwd string, pid int) gather.ProbePane {
	return gather.ProbePane{
		Socket:      socket,
		SessionName: "ox-session",
		PaneTitle:   "OC | live one",
		CurrentPath: cwd,
		PID:         pid,
		PaneID:      paneID,
	}
}

func TestIdentifiedLiveOpenCodeAbsorbsItsResumeRow(t *testing.T) {
	socket := "ox-1700000000-42-7"
	output := Compose(openCodeLiveInput(
		[]gather.LiveOpenCode{{
			Socket: socket, SessionName: "ox-session", PaneID: "%0",
			PID: 101, PanePID: 100, CWD: "/work/a",
			PaneTitle: "OC | live one", SessionID: "ses_live",
		}},
		[]gather.ProbePane{openCodePane(socket, "%0", "/work/a", 100)},
	))
	seen := make([]Row, 0, 2)
	for _, row := range output.Rows {
		if row.ID == "ses_live" {
			seen = append(seen, row)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("rows for ses_live = %#v, want exactly one (the live row absorbs the resume row)", seen)
	}
	row := seen[0]
	if row.Kind != LiveOpenCode {
		t.Fatalf("Kind = %s, want live-opencode", row.Kind)
	}
	if row.Socket != socket || row.PaneID != "%0" || row.SessionName != "ox-session" {
		t.Fatalf("row = %+v, want the seat's tmux address", row)
	}
	if len(row.PanePIDs) != 1 || row.PanePIDs[0] != 100 {
		t.Fatalf("PanePIDs = %v, want the pane's own pid", row.PanePIDs)
	}
	if row.Name != "live one" || row.CWD != "/work/a" || row.Project != "a" {
		t.Fatalf("row = %+v, want the indexed session's name and directory", row)
	}
	if row.ServerCount != 1 {
		t.Fatalf("ServerCount = %d, want 1", row.ServerCount)
	}
	if !row.Kind.IsAddressable() {
		t.Fatal("a live OpenCode row must be addressable")
	}
}

func TestUnidentifiedLiveOpenCodeRowUsesItsPaneTitle(t *testing.T) {
	socket := "ox-1700000000-42-8"
	output := Compose(openCodeLiveInput(
		[]gather.LiveOpenCode{{
			Socket: socket, SessionName: "ox-session", PaneID: "%3",
			PID: 201, PanePID: 200, CWD: "/work/z",
			PaneTitle: "OC | nameless seat",
		}},
		[]gather.ProbePane{openCodePane(socket, "%3", "/work/z", 200)},
	))
	var row Row
	found := false
	for _, candidate := range output.Rows {
		if candidate.Kind == LiveOpenCode {
			row, found = candidate, true
		}
	}
	if !found {
		t.Fatalf("no live-opencode row in %#v", output.Rows)
	}
	if row.ID != socket {
		t.Fatalf("ID = %q, want the socket name: no session could be pinned to it", row.ID)
	}
	if row.Name != "nameless seat" {
		t.Fatalf("Name = %q, want the pane title minus its OC prefix", row.Name)
	}
	if row.CWD != "/work/z" || row.Project != "z" {
		t.Fatalf("row = %+v, want the pane's own directory for project grouping", row)
	}
	if row.ActivityNS != 1_700_000_000*1_000_000_000 {
		t.Fatalf("ActivityNS = %d, want the socket birth fallback", row.ActivityNS)
	}
}

// An unidentified seat whose pane title is NOT OpenCode's own (a fresh TUI
// still wearing the terminal's title — on a live host, the machine hostname)
// is listed under its socket name: the row's ID. A row wearing a name that
// resolves to nothing is a chat nobody can address, which is the original
// defect wearing a new costume.
func TestUnidentifiedLiveOpenCodeRowWithoutTheOCPrefixIsNamedForItsSocket(t *testing.T) {
	socket := "ox-1700000000-42-11"
	output := Compose(openCodeLiveInput(
		[]gather.LiveOpenCode{{
			Socket: socket, SessionName: "ox-session", PaneID: "%3",
			PID: 201, PanePID: 200, CWD: "/work/z",
			PaneTitle: "my-host-01",
		}},
		[]gather.ProbePane{openCodePane(socket, "%3", "/work/z", 200)},
	))
	for _, row := range output.Rows {
		if row.Kind != LiveOpenCode {
			continue
		}
		if row.Name != socket || row.ID != socket {
			t.Fatalf("row = %+v, want the socket name for both ID and Name", row)
		}
		return
	}
	t.Fatalf("no live-opencode row in %#v", output.Rows)
}

// A running TUI the user is typing into must never be suppressed as "empty":
// an unidentified seat carries no counters at all, and even an identified one
// is only as full as the engine's own store has caught up to.
func TestLiveOpenCodeRowsSurviveTheDefaultView(t *testing.T) {
	socket := "ox-1700000000-42-7"
	input := openCodeLiveInput(
		[]gather.LiveOpenCode{{
			Socket: socket, SessionName: "ox-session", PaneID: "%0",
			PID: 101, PanePID: 100, CWD: "/work/a", PaneTitle: "OC | fresh seat",
		}},
		[]gather.ProbePane{openCodePane(socket, "%0", "/work/a", 100)},
	)
	input.Options = Options{View: DefaultView}
	output := Compose(input)
	for _, row := range output.Rows {
		if row.Kind == LiveOpenCode {
			return
		}
	}
	t.Fatalf("no live-opencode row in the default view: %#v", output.Rows)
}

func TestLiveOpenCodeSeatWithNoPaneIsNotARow(t *testing.T) {
	// The same rule liveCodexRows enforces: a responsive socket is not a live
	// chat unless the pane is in THIS snapshot too.
	output := Compose(openCodeLiveInput(
		[]gather.LiveOpenCode{{
			Socket: "ox-1700000000-42-9", PaneID: "%0", PID: 301, PanePID: 300,
			CWD: "/work/a", PaneTitle: "OC | live one", SessionID: "ses_live",
		}},
		nil,
	))
	for _, row := range output.Rows {
		if row.Kind == LiveOpenCode {
			t.Fatalf("live-opencode row %+v survived without a pane", row)
		}
	}
	resumes := 0
	for _, row := range output.Rows {
		if row.ID == "ses_live" && row.Kind == ResumeOpenCode {
			resumes++
		}
	}
	if resumes != 1 {
		t.Fatalf("resume rows for ses_live = %d, want 1: it stays resumable", resumes)
	}
}

func TestTwoServersForOneOpenCodeSessionCollapseToTheNewer(t *testing.T) {
	older, newer := "ox-1700000000-42-7", "ox-1700000500-42-7"
	output := Compose(openCodeLiveInput(
		[]gather.LiveOpenCode{
			{
				Socket: older, SessionName: "old", PaneID: "%0", PID: 101, PanePID: 100,
				CWD: "/work/a", PaneTitle: "OC | live one", SessionID: "ses_live",
			},
			{
				Socket: newer, SessionName: "new", PaneID: "%0", PID: 201, PanePID: 200,
				CWD: "/work/a", PaneTitle: "OC | live one", SessionID: "ses_live",
			},
		},
		[]gather.ProbePane{
			openCodePane(older, "%0", "/work/a", 100),
			openCodePane(newer, "%0", "/work/a", 200),
		},
	))
	seen := make([]Row, 0, 2)
	for _, row := range output.Rows {
		if row.ID == "ses_live" {
			seen = append(seen, row)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("rows for ses_live = %#v, want one collapsed row", seen)
	}
	if seen[0].Socket != newer || seen[0].ServerCount != 2 {
		t.Fatalf("row = %+v, want the newer socket and ServerCount 2", seen[0])
	}
}

// A kill must never land on an identity that stops meaning anything — the
// same rule the Booting guard states. An unidentified live OpenCode row is
// keyed on its SOCKET, so a tombstone written against it would outlive the
// moment the seat's session is finally pinned down.
func TestKillNeverLandsOnASocketKeyedOpenCodeRow(t *testing.T) {
	socket := "ox-1700000000-42-7"
	input := openCodeLiveInput(
		[]gather.LiveOpenCode{{
			Socket: socket, SessionName: "ox-session", PaneID: "%0",
			PID: 101, PanePID: 100, CWD: "/work/a", PaneTitle: "OC | nameless",
		}},
		[]gather.ProbePane{openCodePane(socket, "%0", "/work/a", 100)},
	)
	input.Killed = []store.Killed{{ID: socket, Engine: "ox"}}
	for _, row := range Compose(input).Rows {
		if row.Kind == LiveOpenCode && row.Killed {
			t.Fatalf("row %+v took a kill keyed on its own socket name", row)
		}
	}
}
