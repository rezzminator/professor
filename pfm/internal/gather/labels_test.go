package gather

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// captureStub answers pane captures from a table, and can fail one pane the
// way a pane that died mid-probe does.
type captureStub struct {
	screens map[string]string
	failing map[string]bool
}

func (stub captureStub) ListPanes(context.Context, string) ([]ProbePane, error) {
	return nil, errors.New("not used")
}

func (stub captureStub) CapturePane(
	_ context.Context,
	socket, paneID string,
) (string, error) {
	key := socket + "\x00" + paneID
	if stub.failing[key] {
		return "", errors.New("pane vanished")
	}
	return stub.screens[key], nil
}

func statusline(label string) string {
	return "some transcript text\n🥇 acct  🔖 " + label + " │ 42% · $1.20\n"
}

// The claude half of window naming, rung by rung: a labelled pane renames its
// window, a squatter and a viewport are not chats and never do, a window whose
// panes disagree keeps the name it has, and a pane that could not be captured
// freezes its window rather than letting a sibling speak for it.
func TestClaudeWindowRenames(t *testing.T) {
	pane := func(socket, session, window, paneID, windowName, command string) ProbePane {
		return ProbePane{
			Socket:         socket,
			SessionName:    session,
			WindowID:       window,
			WindowName:     windowName,
			PaneID:         paneID,
			CurrentCommand: command,
		}
	}

	cases := []struct {
		name    string
		panes   []ProbePane
		screens map[string]string
		failing map[string]bool
		want    map[string]string // window id -> target name
	}{
		{
			name:  "a labelled pane names its window",
			panes: []ProbePane{pane("cc-1-2-3", "cc-1-2-3", "@1", "%1", "zsh", "claude")},
			screens: map[string]string{
				"cc-1-2-3\x00%1": statusline("RESEARCH"),
			},
			want: map[string]string{"@1": "RESEARCH"},
		},
		{
			name:  "an unlabelled chat keeps the name it has",
			panes: []ProbePane{pane("cc-1-2-3", "cc-1-2-3", "@1", "%1", "zsh", "claude")},
			screens: map[string]string{
				"cc-1-2-3\x00%1": "just a shell prompt\n",
			},
			want: map[string]string{},
		},
		{
			name:  "a window already carrying its label is left alone",
			panes: []ProbePane{pane("cc-1-2-3", "cc-1-2-3", "@1", "%1", "RESEARCH", "claude")},
			screens: map[string]string{
				"cc-1-2-3\x00%1": statusline("RESEARCH"),
			},
			want: map[string]string{},
		},
		{
			name:  "a squatter session on a chat socket is not that chat",
			panes: []ProbePane{pane("cc-1-2-3", "someone-else", "@1", "%1", "zsh", "claude")},
			screens: map[string]string{
				"cc-1-2-3\x00%1": statusline("RESEARCH"),
			},
			want: map[string]string{},
		},
		{
			name:  "a viewport mirrors another chat's statusline",
			panes: []ProbePane{pane("cc-1-2-3", "cc-1-2-3", "@1", "%1", "zsh", "tmux")},
			screens: map[string]string{
				"cc-1-2-3\x00%1": statusline("SOMEBODY_ELSE"),
			},
			want: map[string]string{},
		},
		{
			name: "two differently labelled panes in one window cancel each other",
			panes: []ProbePane{
				pane("cc-1-2-3", "cc-1-2-3", "@1", "%1", "zsh", "claude"),
				pane("cc-1-2-3", "cc-1-2-3", "@1", "%2", "zsh", "claude"),
			},
			screens: map[string]string{
				"cc-1-2-3\x00%1": statusline("ALPHA"),
				"cc-1-2-3\x00%2": statusline("BETA"),
			},
			want: map[string]string{},
		},
		{
			name: "a pane that could not be captured freezes its window",
			panes: []ProbePane{
				pane("cc-1-2-3", "cc-1-2-3", "@1", "%1", "zsh", "claude"),
				pane("cc-1-2-3", "cc-1-2-3", "@1", "%2", "zsh", "claude"),
			},
			screens: map[string]string{
				"cc-1-2-3\x00%1": statusline("ALPHA"),
			},
			failing: map[string]bool{"cc-1-2-3\x00%2": true},
			want:    map[string]string{},
		},
		{
			name: "two windows converging on one name are both left alone",
			panes: []ProbePane{
				pane("cc-1-2-3", "cc-1-2-3", "@1", "%1", "zsh", "claude"),
				pane("cc-4-5-6", "cc-4-5-6", "@2", "%1", "zsh", "claude"),
			},
			screens: map[string]string{
				"cc-1-2-3\x00%1": statusline("TWINS"),
				"cc-4-5-6\x00%1": statusline("TWINS"),
			},
			want: map[string]string{},
		},
		{
			name:  "a codex socket is named from its thread, never from a capture",
			panes: []ProbePane{pane("cx-1-2-3", "cx-1-2-3", "@1", "%1", "zsh", "codex")},
			screens: map[string]string{
				"cx-1-2-3\x00%1": statusline("NOT_THIS"),
			},
			want: map[string]string{},
		},
		{
			name:  "a label longer than the window budget is clipped",
			panes: []ProbePane{pane("cc-1-2-3", "cc-1-2-3", "@1", "%1", "zsh", "claude")},
			screens: map[string]string{
				"cc-1-2-3\x00%1": statusline(strings.Repeat("A", 40)),
			},
			want: map[string]string{"@1": strings.Repeat("A", 24)},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			stub := captureStub{screens: testCase.screens, failing: testCase.failing}
			labels := CaptureClaudeLabels(context.Background(), stub, testCase.panes)
			renames := computeWindowRenames(
				testCase.panes,
				nil,
				labels,
				nil,
				nil,
			)
			if len(renames) != len(testCase.want) {
				t.Fatalf("renames = %#v, want %v", renames, testCase.want)
			}
			for _, rename := range renames {
				want, found := testCase.want[rename.WindowID]
				if !found || want != rename.TargetName {
					t.Fatalf(
						"window %s renamed to %q, want %v",
						rename.WindowID,
						rename.TargetName,
						testCase.want,
					)
				}
			}
		})
	}
}

// Both engines converge in ONE pass, and a claude window never steals the
// name a codex window is taking.
func TestBothEnginesConvergeInOnePass(t *testing.T) {
	panes := []ProbePane{
		{
			Socket:         "cc-1-2-3",
			SessionName:    "cc-1-2-3",
			WindowID:       "@1",
			WindowName:     "zsh",
			PaneID:         "%1",
			CurrentCommand: "claude",
		},
		{
			Socket:         "cx-4-5-6",
			SessionName:    "cx-4-5-6",
			WindowID:       "@2",
			WindowName:     "Codex",
			PaneID:         "%1",
			CurrentCommand: "codex",
		},
	}
	stub := captureStub{screens: map[string]string{
		"cc-1-2-3\x00%1": statusline("CLAUDE_SIDE"),
	}}
	labels := CaptureClaudeLabels(context.Background(), stub, panes)
	renames := computeWindowRenames(
		panes,
		[]LiveCodex{{Socket: "cx-4-5-6", PaneID: "%1", RolloutPath: "/r.jsonl"}},
		labels,
		func(string) string { return "CODEX_SIDE" },
		nil,
	)
	if len(renames) != 2 {
		t.Fatalf("renames = %#v, want one per engine", renames)
	}
	got := map[string]string{}
	for _, rename := range renames {
		got[rename.Socket] = rename.TargetName
	}
	if got["cc-1-2-3"] != "CLAUDE_SIDE" || got["cx-4-5-6"] != "CODEX_SIDE" {
		t.Fatalf("renames = %v", got)
	}
}
