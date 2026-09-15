package compose

import (
	"testing"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/store"
)

// TestLabelKilledChatLeavesTheDefaultListing pins the rename-to-kill rule: a
// chat whose label starts with "_KILL" is out of the default list, in the
// killed view, and counted as killed — with no row in the killed table.
func TestLabelKilledChatLeavesTheDefaultListing(t *testing.T) {
	worker := transcript(
		"labelled",
		"/accounts/1/projects/alpha/labelled.jsonl",
		"/work/alpha",
		"_KILL headless worker",
		100,
		5,
		900,
	)
	plain := transcript(
		"plain",
		"/accounts/1/projects/alpha/plain.jsonl",
		"/work/alpha",
		"Ordinary chat",
		100,
		5,
		800,
	)
	input := Input{
		Transcripts:  []store.Transcript{worker, plain},
		AccountRoots: fixtureAccountRoots(),
		Options:      Options{View: DefaultView, PrimaryAccount: 1},
	}

	output := Compose(input)
	if _, found := rowByID(output.Rows, "labelled"); found {
		t.Fatalf("_KILL-labelled chat listed by default: %#v", output.Rows)
	}
	if _, found := rowByID(output.Rows, "plain"); !found {
		t.Fatalf("ordinary chat missing from the default list: %#v", output.Rows)
	}
	if output.KilledCount != 1 {
		t.Fatalf("killed count = %d, want 1", output.KilledCount)
	}
	if output.SuppressedCount != 0 {
		t.Fatalf("suppressed count = %d, want 0", output.SuppressedCount)
	}

	input.Options.View = KilledView
	killed := Compose(input)
	row, found := rowByID(killed.Rows, "labelled")
	if !found {
		t.Fatalf("_KILL-labelled chat missing from the killed view: %#v", killed.Rows)
	}
	if !row.Killed || !row.NameKilled {
		t.Fatalf("label kill not marked on the row: %#v", row)
	}
	if _, found := rowByID(killed.Rows, "plain"); found {
		t.Fatalf("ordinary chat leaked into the killed view: %#v", killed.Rows)
	}
}

// TestLabelKillCoversLiveAndCodexRowsAndFoldsCase covers the two row shapes a
// headless worker actually takes — a live Claude pane and a resumable Codex
// lineage — and the lower-case spelling.
func TestLabelKillCoversLiveAndCodexRowsAndFoldsCase(t *testing.T) {
	live := transcript(
		"live-worker",
		"/accounts/1/projects/alpha/live-worker.jsonl",
		"/work/alpha",
		"_kill live worker",
		100,
		5,
		900,
	)
	rollout := store.Rollout{
		ID:          "019f-worker",
		Path:        "/codex/rollout-2026-01-01T00-00-00-019f-worker.jsonl",
		Size:        800,
		MTimeNS:     1300,
		CWD:         "/work/alpha",
		UserThread:  true,
		FirstPrompt: "unnamed",
		PromptCount: 4,
	}
	input := Input{
		Transcripts: []store.Transcript{live},
		Rollouts:    []store.Rollout{rollout},
		CxNames:     map[string]string{"019f-worker": "_KILL codex worker"},
		Snapshot: gather.Snapshot{
			Panes: []gather.Pane{{Socket: "cc-1-2-3", PaneID: "%1"}},
			Crumbs: []gather.Crumb{{
				Filename:       "cc-1-2-3",
				Socket:         "cc-1-2-3",
				PaneID:         "%1",
				TranscriptPath: live.Path,
			}},
		},
		AccountRoots: fixtureAccountRoots(),
		Options:      Options{View: DefaultView, PrimaryAccount: 1},
	}

	output := Compose(input)
	if row, found := rowByID(output.Rows, "live-worker"); found {
		t.Fatalf("live _kill chat listed by default: %#v", row)
	}
	if row, found := rowByID(output.Rows, "019f-worker"); found {
		t.Fatalf("Codex _KILL chat listed by default: %#v", row)
	}
	if output.KilledCount != 2 {
		t.Fatalf("killed count = %d, want 2", output.KilledCount)
	}
}

func TestLegacyKillLabelAndKilledTableRowRemainKilled(t *testing.T) {
	labelled := transcript(
		"legacy-label",
		"/accounts/1/projects/alpha/legacy-label.jsonl",
		"/work/alpha",
		"_HIDE legacy worker",
		100,
		5,
		900,
	)
	stored := transcript(
		"legacy-store",
		"/accounts/1/projects/alpha/legacy-store.jsonl",
		"/work/alpha",
		"Stored kill",
		100,
		5,
		800,
	)
	input := Input{
		Transcripts:  []store.Transcript{labelled, stored},
		Killed:       []store.Killed{{ID: stored.UUID, Engine: pfmengine.Claude}},
		AccountRoots: fixtureAccountRoots(),
		Options:      Options{View: DefaultView, PrimaryAccount: 1},
	}
	if output := Compose(input); output.KilledCount != 2 {
		t.Fatalf("legacy kill count = %d, want 2", output.KilledCount)
	} else if _, found := rowByID(output.Rows, labelled.UUID); found {
		t.Fatalf("legacy label kill listed by default: %#v", output.Rows)
	} else if _, found := rowByID(output.Rows, stored.UUID); found {
		t.Fatalf("legacy stored kill listed by default: %#v", output.Rows)
	}
	input.Options.View = KilledView
	output := Compose(input)
	if len(output.Rows) != 2 {
		t.Fatalf("legacy kills missing from killed view: %#v", output.Rows)
	}
	for _, row := range output.Rows {
		if !row.Killed {
			t.Fatalf("legacy kill not marked killed: %#v", row)
		}
	}
}

// TestSplitRowKeepsItsJoinedName guards the one exclusion: a split row's Name
// is a join of its panes' names, so a "_KILL…" first member must not take the
// whole socket out of the list.
func TestSplitRowKeepsItsJoinedName(t *testing.T) {
	first := transcript(
		"split-a",
		"/accounts/1/projects/alpha/split-a.jsonl",
		"/work/alpha",
		"_KILL left pane",
		100,
		5,
		900,
	)
	second := transcript(
		"split-b",
		"/accounts/1/projects/alpha/split-b.jsonl",
		"/work/alpha",
		"Right pane",
		100,
		5,
		901,
	)
	input := Input{
		Transcripts: []store.Transcript{first, second},
		Snapshot: gather.Snapshot{
			Panes: []gather.Pane{
				{Socket: "cc-9-9-9", PaneID: "%1"},
				{Socket: "cc-9-9-9", PaneID: "%2"},
			},
			Crumbs: []gather.Crumb{
				{
					Filename:       "cc-9-9-9-%1",
					Socket:         "cc-9-9-9",
					PaneID:         "%1",
					TranscriptPath: first.Path,
				},
				{
					Filename:       "cc-9-9-9-%2",
					Socket:         "cc-9-9-9",
					PaneID:         "%2",
					TranscriptPath: second.Path,
				},
			},
		},
		AccountRoots: fixtureAccountRoots(),
		Options:      Options{View: DefaultView, PrimaryAccount: 1},
	}

	output := Compose(input)
	splits := rowsByKind(output.Rows, LiveSplit)
	if len(splits) != 1 {
		t.Fatalf("split row missing from the default list: %#v", output.Rows)
	}
	if splits[0].Killed || splits[0].NameKilled {
		t.Fatalf("split row killed by a member's label: %#v", splits[0])
	}
}
