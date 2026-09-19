package gather

import (
	"testing"
)

// oxPane is the shape ProbeTmux returns for a pane on an OpenCode socket:
// tmux's automatic-rename picks the title up from the TUI's own terminal
// title escape, so the "OC | " prefix is environmental, not written by pfm.
func oxPane(socket, paneID, title, cwd string, pid int) ProbePane {
	return ProbePane{
		Socket:      socket,
		SessionName: socket,
		PaneTitle:   title,
		CurrentPath: cwd,
		PID:         pid,
		PaneID:      paneID,
	}
}

func openCodeProc(parent int, argv ...string) fakeProcess {
	return fakeProcess{cmdline: argv, stat: ProcStat{ParentPID: parent}}
}

func TestDetectOpenCodeTitleRung(t *testing.T) {
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		100: openCodeProc(1, "/bin/zsh"),
		101: openCodeProc(100, "opencode", "/work/api"),
	}}
	panes := []ProbePane{oxPane("ox-1789813424-3207648-22387", "%0", "OC | P:OPENCODE", "/work/api", 100)}
	sessions := []OpenCodeSession{
		{ID: "ses_a", Title: "P:OPENCODE", Directory: "/work/api", TimeCreatedMS: 1_000},
		{ID: "ses_b", Title: "P:OPENCODE", Directory: "/work/other", TimeCreatedMS: 2_000},
	}
	live, err := DetectOpenCode(proc, panes, sessions)
	if err != nil {
		t.Fatalf("DetectOpenCode: %v", err)
	}
	if len(live) != 1 {
		t.Fatalf("live = %#v, want exactly one seat", live)
	}
	seat := live[0]
	if seat.SessionID != "ses_a" {
		t.Fatalf("SessionID = %q, want ses_a (title+cwd rung)", seat.SessionID)
	}
	if seat.Socket != "ox-1789813424-3207648-22387" || seat.PaneID != "%0" ||
		seat.PID != 101 || seat.PanePID != 100 || seat.CWD != "/work/api" ||
		seat.PaneTitle != "OC | P:OPENCODE" || seat.SessionName != "ox-1789813424-3207648-22387" {
		t.Fatalf("seat = %#v, want the pane's own address and process", seat)
	}
}

func TestDetectOpenCodeArgvRung(t *testing.T) {
	for _, argv := range [][]string{
		{"opencode", "--session", "ses_argv", "/work/api"},
		{"opencode", "-s", "ses_argv", "/work/api"},
		{"opencode", "--session=ses_argv", "/work/api"},
	} {
		proc := &fakeProcFS{processes: map[int]fakeProcess{
			100: openCodeProc(1, "/bin/zsh"),
			101: {cmdline: argv, stat: ProcStat{ParentPID: 100}},
		}}
		// The title rung cannot answer: two sessions share the title AND cwd.
		panes := []ProbePane{oxPane("ox-1700000000-1-1", "%0", "OC | ambiguous", "/work/api", 100)}
		sessions := []OpenCodeSession{
			{ID: "ses_x", Title: "ambiguous", Directory: "/work/api"},
			{ID: "ses_y", Title: "ambiguous", Directory: "/work/api"},
			{ID: "ses_argv", Title: "something else", Directory: "/work/api"},
		}
		live, err := DetectOpenCode(proc, panes, sessions)
		if err != nil {
			t.Fatalf("DetectOpenCode(%v): %v", argv, err)
		}
		if len(live) != 1 || live[0].SessionID != "ses_argv" {
			t.Fatalf("DetectOpenCode(%v) = %#v, want one seat on ses_argv", argv, live)
		}
	}
}

func TestDetectOpenCodeBirthRung(t *testing.T) {
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		100: openCodeProc(1, "/bin/zsh"),
		101: openCodeProc(100, "opencode", "/work/api"),
	}}
	panes := []ProbePane{oxPane("ox-1700000000-1-1", "%0", "OC | untitled", "/work/api", 100)}
	sessions := []OpenCodeSession{
		// Created before the socket's birth window opened: not this pane's.
		{ID: "ses_old", Title: "", Directory: "/work/api", TimeCreatedMS: 1_699_000_000_000},
		// Created within 5s before the socket name was minted.
		{ID: "ses_new", Title: "", Directory: "/work/api", TimeCreatedMS: 1_699_999_998_000},
		// Right time, wrong directory.
		{ID: "ses_elsewhere", Title: "", Directory: "/work/other", TimeCreatedMS: 1_700_000_001_000},
	}
	live, err := DetectOpenCode(proc, panes, sessions)
	if err != nil {
		t.Fatalf("DetectOpenCode: %v", err)
	}
	if len(live) != 1 || live[0].SessionID != "ses_new" {
		t.Fatalf("live = %#v, want one seat on ses_new (birth rung)", live)
	}
}

func TestDetectOpenCodeAmbiguityFallsThroughToUnidentified(t *testing.T) {
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		100: openCodeProc(1, "/bin/zsh"),
		101: openCodeProc(100, "opencode", "/work/api"),
	}}
	panes := []ProbePane{oxPane("ox-1700000000-1-1", "%0", "OC | ambiguous", "/work/api", 100)}
	sessions := []OpenCodeSession{
		{ID: "ses_x", Title: "ambiguous", Directory: "/work/api", TimeCreatedMS: 1_700_000_001_000},
		{ID: "ses_y", Title: "ambiguous", Directory: "/work/api", TimeCreatedMS: 1_700_000_002_000},
	}
	live, err := DetectOpenCode(proc, panes, sessions)
	if err != nil {
		t.Fatalf("DetectOpenCode: %v", err)
	}
	if len(live) != 1 {
		t.Fatalf("live = %#v, want the seat listed even unidentified", live)
	}
	if live[0].SessionID != "" {
		t.Fatalf("SessionID = %q, want empty: every rung was ambiguous", live[0].SessionID)
	}
}

func TestDetectOpenCodeClaimsASessionOnlyOnce(t *testing.T) {
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		100: openCodeProc(1, "/bin/zsh"),
		101: openCodeProc(100, "opencode", "/work/api"),
		200: openCodeProc(1, "/bin/zsh"),
		201: openCodeProc(200, "opencode", "/work/api"),
	}}
	panes := []ProbePane{
		oxPane("ox-1700000002-1-1", "%0", "OC | shared", "/work/api", 200),
		oxPane("ox-1700000001-1-1", "%0", "OC | shared", "/work/api", 100),
	}
	sessions := []OpenCodeSession{{ID: "ses_one", Title: "shared", Directory: "/work/api"}}
	live, err := DetectOpenCode(proc, panes, sessions)
	if err != nil {
		t.Fatalf("DetectOpenCode: %v", err)
	}
	if len(live) != 2 {
		t.Fatalf("live = %#v, want both panes listed", live)
	}
	// Stable socket-name order claims first: ox-1700000001 precedes ox-1700000002.
	if live[0].Socket != "ox-1700000001-1-1" || live[0].SessionID != "ses_one" {
		t.Fatalf("first seat = %#v, want ox-1700000001-1-1 holding ses_one", live[0])
	}
	if live[1].SessionID != "" {
		t.Fatalf("second seat = %#v, want no claim: ses_one is taken", live[1])
	}
}

func TestDetectOpenCodeShellOnlyPaneIsNotLive(t *testing.T) {
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		100: openCodeProc(1, "/bin/zsh"),
	}}
	panes := []ProbePane{oxPane("ox-1700000000-1-1", "%0", "OC | gone", "/work/api", 100)}
	live, err := DetectOpenCode(proc, panes, []OpenCodeSession{{ID: "ses_a", Title: "gone", Directory: "/work/api"}})
	if err != nil {
		t.Fatalf("DetectOpenCode: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("live = %#v, want none: the ox- pane runs a shell, not OpenCode", live)
	}
}

func TestDetectOpenCodeIgnoresNonOpenCodeSockets(t *testing.T) {
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		100: openCodeProc(1, "/bin/zsh"),
		101: openCodeProc(100, "opencode", "/work/api"),
	}}
	panes := []ProbePane{oxPane("cc-1700000000-1-1", "%0", "OC | claude seat", "/work/api", 100)}
	live, err := DetectOpenCode(proc, panes, nil)
	if err != nil {
		t.Fatalf("DetectOpenCode: %v", err)
	}
	if len(live) != 0 {
		t.Fatalf("live = %#v, want none: a cc- socket is never an OpenCode seat", live)
	}
}

func TestDetectOpenCodeHonoursConfiguredBinary(t *testing.T) {
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		100: openCodeProc(1, "/bin/zsh"),
		101: openCodeProc(100, "/opt/oc/bin/opencode-dev", "/work/api"),
	}}
	panes := []ProbePane{oxPane("ox-1700000000-1-1", "%0", "OC | dev", "/work/api", 100)}
	sessions := []OpenCodeSession{{ID: "ses_a", Title: "dev", Directory: "/work/api"}}
	if live, err := DetectOpenCode(proc, panes, sessions); err != nil || len(live) != 0 {
		t.Fatalf("DetectOpenCode without the binary = %#v, %v; want none", live, err)
	}
	live, err := DetectOpenCode(proc, panes, sessions, "/opt/oc/bin/opencode-dev")
	if err != nil {
		t.Fatalf("DetectOpenCode: %v", err)
	}
	if len(live) != 1 || live[0].SessionID != "ses_a" {
		t.Fatalf("live = %#v, want one seat identified through the configured binary", live)
	}
}

func TestOpenCodePaneNameStripsTheTitlePrefix(t *testing.T) {
	tests := map[string]string{
		"OC | P:OPENCODE": "P:OPENCODE",
		"OC | ":           "",
		"P:OPENCODE":      "P:OPENCODE",
		"":                "",
	}
	for title, want := range tests {
		if got := OpenCodePaneName(title); got != want {
			t.Fatalf("OpenCodePaneName(%q) = %q, want %q", title, got, want)
		}
	}
}
