package gather

import (
	"reflect"
	"testing"
)

// TestDetectCrumblessLiveNoCrumbEmitsOneEntry is the red-first proof for the
// booting-chat blind spot (TESTPLAN.md "Residual known divergences" #9): a
// live Claude pane whose socket carries no crumb at all — the shape of a chat
// still sitting at a startup prompt — must surface as exactly one
// CrumblessLive entry carrying the pane's own display fields, not silently
// vanish the way crumb-driven gather used to leave it.
func TestDetectCrumblessLiveNoCrumbEmitsOneEntry(t *testing.T) {
	panes := []ProbePane{{
		Socket:      "cc-1700000000-111-222",
		SessionName: "cc-1700000000-111-222",
		WindowID:    "@1",
		WindowName:  "claude",
		PaneID:      "%1",
		PID:         500,
		CurrentPath: "/work/booting-project",
	}}
	claudeProcesses := []ClaudeProcess{{
		PID:     900,
		PanePID: 500,
		Socket:  "cc-1700000000-111-222",
		PaneID:  "%1",
	}}
	proc := &fakeProcFS{processes: map[int]fakeProcess{
		500: {birth: 1700000005},
	}}

	entries := DetectCrumblessLive(proc, claudeProcesses, nil, panes)
	want := []CrumblessLive{{
		Socket:        "cc-1700000000-111-222",
		SessionName:   "cc-1700000000-111-222",
		WindowID:      "@1",
		WindowName:    "claude",
		PaneID:        "%1",
		PID:           900,
		CWD:           "/work/booting-project",
		PaneStartUnix: 1700000005,
	}}
	if !reflect.DeepEqual(entries, want) {
		t.Fatalf("DetectCrumblessLive() = %#v, want %#v", entries, want)
	}
}

// TestDetectCrumblessLiveSocketCrumbSuppressesTheWholeSocket proves the fix's
// dedup contract: a crumb resolving ANYWHERE on the socket — pane-scoped or
// socket-scoped — means the ordinary live row already covers it, so no pane
// on that socket gets a crumbless entry, even one whose own pane has no crumb
// of its own.
func TestDetectCrumblessLiveSocketCrumbSuppressesTheWholeSocket(t *testing.T) {
	panes := []ProbePane{
		{Socket: "cc-1-2-3", PaneID: "%1", PID: 500},
		{Socket: "cc-1-2-3", PaneID: "%2", PID: 501},
	}
	claudeProcesses := []ClaudeProcess{
		{PID: 900, PanePID: 500, Socket: "cc-1-2-3", PaneID: "%1"},
		{PID: 901, PanePID: 501, Socket: "cc-1-2-3", PaneID: "%2"},
	}
	proc := &fakeProcFS{}

	// A pane-scoped crumb on %1 only — %2 still has no crumb of its own.
	crumbs := []Crumb{{Socket: "cc-1-2-3", PaneID: "%1", TranscriptPath: "/t.jsonl"}}
	if entries := DetectCrumblessLive(proc, claudeProcesses, crumbs, panes); len(entries) != 0 {
		t.Fatalf("pane-scoped crumb on one pane left the socket booting: %#v", entries)
	}

	// A socket-scoped crumb (no pane id) covers the whole socket too.
	socketCrumb := []Crumb{{Socket: "cc-1-2-3", TranscriptPath: "/t.jsonl"}}
	if entries := DetectCrumblessLive(proc, claudeProcesses, socketCrumb, panes); len(entries) != 0 {
		t.Fatalf("socket-scoped crumb left the socket booting: %#v", entries)
	}

	// No crumb at all: dedup by socket still emits exactly ONE entry, not one
	// per crumbless pane.
	entries := DetectCrumblessLive(proc, claudeProcesses, nil, panes)
	if len(entries) != 1 || entries[0].PaneID != "%1" {
		t.Fatalf("DetectCrumblessLive() = %#v, want one entry for the first pane", entries)
	}
}

// TestDetectCrumblessLiveAcceptsTheNewSocketShape covers the exact live shape
// observed the night the bug was found: a teammate spawned through cc-new-*,
// wedged at the MCP-approval prompt.
func TestDetectCrumblessLiveAcceptsTheNewSocketShape(t *testing.T) {
	panes := []ProbePane{{
		Socket: "cc-new-fixture-1",
		PaneID: "%1",
		PID:    500,
	}}
	claudeProcesses := []ClaudeProcess{{
		PID: 900, PanePID: 500, Socket: "cc-new-fixture-1", PaneID: "%1",
	}}
	entries := DetectCrumblessLive(&fakeProcFS{}, claudeProcesses, nil, panes)
	if len(entries) != 1 || entries[0].Socket != "cc-new-fixture-1" {
		t.Fatalf("DetectCrumblessLive() = %#v, want the cc-new-* socket", entries)
	}
}

// TestDetectCrumblessLiveRejectsNonClaudeSocketShapes proves the detector
// stays scoped to valid cc-* sockets: a cx-* socket (a different engine, even
// though it shares the crumb-name grammar) and a malformed cc-* name are both
// declined.
func TestDetectCrumblessLiveRejectsNonClaudeSocketShapes(t *testing.T) {
	panes := []ProbePane{
		{Socket: "cx-1-2-3", PaneID: "%1", PID: 500},
		{Socket: "cc-not-a-real-socket", PaneID: "%2", PID: 501},
	}
	claudeProcesses := []ClaudeProcess{
		{PID: 900, PanePID: 500, Socket: "cx-1-2-3", PaneID: "%1"},
		{PID: 901, PanePID: 501, Socket: "cc-not-a-real-socket", PaneID: "%2"},
	}
	if entries := DetectCrumblessLive(&fakeProcFS{}, claudeProcesses, nil, panes); len(entries) != 0 {
		t.Fatalf("DetectCrumblessLive() = %#v, want neither non-cc-shaped socket", entries)
	}
}
