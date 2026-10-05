package ui

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

// deckFleet is a believable working fleet — four projects, three engines, ages
// from seconds to weeks — for the tests and probes that need a list with a
// shape. Every name and prompt is invented.
func deckFleet(width, height int) Snapshot {
	ago := func(duration time.Duration) int64 { return fixtureNowNS - int64(duration) }
	id := func(index int) string {
		return fmt.Sprintf("%08x-0000-4000-8000-%012x", 0xa0000000+index, index)
	}
	rows := []compose.Row{
		{Kind: compose.ProfessorUpdate, Name: "v0.63.0 is out", Project: "~"},
		{Kind: compose.NewClaude, Name: "New Claude chat", Project: "~"},
		{
			Kind:        compose.LiveClaude,
			ID:          id(1),
			Socket:      "cc-1",
			PaneID:      "%1",
			Name:        "P:BUILDER",
			Project:     "atlas",
			CWD:         "/home/tester/atlas",
			Size:        2_400_000,
			PromptCount: 214,
			ActivityNS:  ago(4 * time.Second),
			Account:     1,
			C1H:         true,
			LastPrompt:  "Wire the tempo axis into the list panel and keep the cursor row visible when the viewport scrolls.",
		},
		{
			Kind: compose.LiveClaude, ID: id(2), Socket: "cc-2", PaneID: "%2", Name: "P:AUDIT", Project: "atlas",
			CWD: "/home/tester/atlas", Size: 880_000, PromptCount: 61, ActivityNS: ago(3 * time.Minute), Account: 2,
			LastPrompt: "Audit the retry loop in the scheduler for lost wakeups.",
		},
		{
			Kind:        compose.LiveCodex,
			ID:          id(3),
			Socket:      "cx-3",
			PaneID:      "%3",
			Name:        "P:BUILDER-2",
			Project:     "atlas",
			CWD:         "/home/tester/atlas/cmd",
			Size:        410_000,
			PromptCount: 33,
			ActivityNS:  ago(17 * time.Minute),
			Account:     3,
		},
		{
			Kind: compose.ResumeClaude, ID: id(4), Name: "schema migration plan", Project: "atlas",
			CWD: "/home/tester/atlas/db", Size: 1_100_000, PromptCount: 88, ActivityNS: ago(5 * time.Hour), Account: 1,
			LastPrompt: "Draft the additive migration for the comms ledger and list what a rollback has to undo.",
		},
		{
			Kind: compose.ResumeClaude, ID: id(5), Name: "release notes", Project: "atlas",
			CWD: "/home/tester/atlas", Size: 90_000, PromptCount: 9, ActivityNS: ago(30 * time.Hour), Account: 2,
		},
		{
			Kind:        compose.LiveClaude,
			ID:          id(6),
			Socket:      "cc-6",
			PaneID:      "%6",
			Name:        "harbor gateway",
			Project:     "harbor",
			CWD:         "/home/tester/harbor",
			Size:        3_900_000,
			PromptCount: 305,
			ActivityNS:  ago(41 * time.Minute),
			Account:     1,
			Attached:    true,
			Here:        true,
			LastPrompt:  "Why does the gateway drop the first frame after a reconnect?",
		},
		{
			Kind:        compose.ResumeCodex,
			ID:          id(7),
			Name:        "fuzz the parser",
			Project:     "harbor",
			CWD:         "/home/tester/harbor/parse",
			Size:        220_000,
			PromptCount: 27,
			ActivityNS:  ago(2 * 24 * time.Hour),
			Account:     2,
		},
		{
			Kind: compose.Agent, ID: id(8), Name: "RR crawler", Project: "harbor",
			CWD: "/home/tester/harbor", Size: 40_000, PromptCount: 4, ActivityNS: ago(9 * time.Minute),
		},
		{
			Kind: compose.ResumeClaude, ID: id(9), Name: "lumen: colour ramps", Project: "lumen",
			CWD: "/home/tester/lumen", Size: 600_000, PromptCount: 52, ActivityNS: ago(6 * 24 * time.Hour), Account: 3,
			LastPrompt: "Compare perceptual ramps for the dark theme and pick one that survives 256 colours.",
		},
		{
			Kind: compose.ResumeOpenCode, ID: id(10), Name: "lumen: legend", Project: "lumen",
			CWD: "/home/tester/lumen", PromptCount: 12, ActivityNS: ago(9 * 24 * time.Hour),
		},
		{
			Kind:        compose.ResumeClaude,
			ID:          id(11),
			Name:        "quartz benchmark sweep",
			Project:     "quartz",
			CWD:         "/home/tester/quartz",
			Size:        5_200_000,
			PromptCount: 410,
			ActivityNS:  ago(21 * 24 * time.Hour),
			Account:     1,
		},
		{
			Kind: compose.ResumeClaude, ID: id(12), Name: "old idea", Project: "quartz", Killed: true,
			CWD: "/home/tester/quartz", Size: 12_000, PromptCount: 2, ActivityNS: ago(28 * 24 * time.Hour),
		},
	}
	return Snapshot{
		Rows:                rows,
		View:                compose.AllView,
		KilledCount:         1,
		SuppressedCount:     3,
		PrimaryAccount:      2,
		AccountIDs:          []int{1, 2, 3},
		CodexPrimaryAccount: 3,
		CodexAccountIDs:     []int{1, 2, 3},
		CodexAccountEmojis:  map[int]string{1: "🥇", 2: "🥈", 3: "🥉"},
		AccountEmojis:       map[int]string{1: "🥇", 2: "🥈", 3: "🥉"},
		Cache1H:             true,
		NowNS:               fixtureNowNS,
		Width:               width,
		Height:              height,
		Home:                "/home/tester",
		MergeNewChat:        true,
		ApplyKill:           func(KillChange) error { return nil },
	}
}

// deckModel builds the deck fleet's model at the given size.
func deckModel(width, height int) Model {
	return NewModel(deckFleet(width, height))
}

// selectChat moves the cursor, key by key, until the named chat is selected —
// the way a person gets there.
func selectChat(t *testing.T, model Model, name string) Model {
	t.Helper()
	for range len(model.filtered) + 1 {
		if row, ok := model.selectedRow(); ok && row.Name == name {
			return model
		}
		model, _ = applyKey(t, model, specialKey(tea.KeyDown))
	}
	t.Fatalf("no chat named %q reachable in the list", name)
	return model
}
