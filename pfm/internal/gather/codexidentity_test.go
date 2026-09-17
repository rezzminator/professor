package gather

import (
	"context"
	"testing"
)

func codexPane(socket, paneID string) ProbePane {
	return ProbePane{
		Socket:         socket,
		SessionName:    socket,
		WindowID:       "@1",
		PaneID:         paneID,
		CurrentCommand: "codex",
	}
}

// A status line naming a thread reads as Name; a status line whose first
// field is a bare thread id — the shape a thread born from /clear always
// has, since it is never named — reads as ThreadID; a capture that errored
// sets Failed with neither field set, and Failed must never be read as "this
// pane runs nothing".
func TestCaptureCodexIdentity(t *testing.T) {
	panes := []ProbePane{
		codexPane("cx-1-2-3", "%1"),
		codexPane("cx-4-5-6", "%1"),
		codexPane("cx-7-8-9", "%1"),
	}
	stub := captureStub{
		screens: map[string]string{
			"cx-1-2-3\x00%1": "some transcript text\n" +
				"  ENGINE_BUILDER · ~/.professor · Full Access · gpt-5.6-sol xhigh · Context 66% used\n",
			"cx-4-5-6\x00%1": "some transcript text\n" +
				"  01a02e86-f64d-7253-bb68-1b8956cf9fd7 · /tmp · Full Access · gpt-5.6-luna medium\n",
		},
		failing: map[string]bool{"cx-7-8-9\x00%1": true},
	}

	got := CaptureCodexIdentity(context.Background(), stub, panes)
	if len(got) != 3 {
		t.Fatalf("CaptureCodexIdentity() = %#v, want three panes", got)
	}
	byPane := map[string]CodexIdentity{}
	for _, identity := range got {
		byPane[identity.Socket+"\x00"+identity.PaneID] = identity
	}

	named := byPane["cx-1-2-3\x00%1"]
	if named.Name != "ENGINE_BUILDER" || named.ThreadID != "" || named.Failed {
		t.Fatalf("named pane identity = %#v, want Name only", named)
	}

	unnamed := byPane["cx-4-5-6\x00%1"]
	if unnamed.ThreadID != "01a02e86-f64d-7253-bb68-1b8956cf9fd7" ||
		unnamed.Name != "" || unnamed.Failed {
		t.Fatalf("unnamed pane identity = %#v, want ThreadID only", unnamed)
	}

	failed := byPane["cx-7-8-9\x00%1"]
	if !failed.Failed || failed.Name != "" || failed.ThreadID != "" {
		t.Fatalf("failed pane identity = %#v, want Failed with neither field set", failed)
	}
}

// A claude pane, a squatter, and a viewport never carry a codex identity.
func TestCaptureCodexIdentityFiltersToLiveCodexPanes(t *testing.T) {
	panes := []ProbePane{
		{Socket: "cc-1-2-3", SessionName: "cc-1-2-3", WindowID: "@1", PaneID: "%1", CurrentCommand: "claude"},
		{Socket: "cx-1-2-3", SessionName: "someone-else", WindowID: "@1", PaneID: "%1", CurrentCommand: "codex"},
		{Socket: "cx-4-5-6", SessionName: "cx-4-5-6", WindowID: "@1", PaneID: "%1", CurrentCommand: "tmux"},
	}
	stub := captureStub{screens: map[string]string{
		"cc-1-2-3\x00%1": "NOT_CODEX · elsewhere\n",
		"cx-1-2-3\x00%1": "SQUATTER · elsewhere\n",
		"cx-4-5-6\x00%1": "VIEWPORT · elsewhere\n",
	}}
	if got := CaptureCodexIdentity(context.Background(), stub, panes); len(got) != 0 {
		t.Fatalf("CaptureCodexIdentity() = %#v, want no candidates", got)
	}
}
