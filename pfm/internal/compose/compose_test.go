package compose

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/store"
)

func TestComposeCanonicalizesSymlinkedAccountRoots(t *testing.T) {
	root := t.TempDir()
	physical := filepath.Join(root, "physical")
	alias := filepath.Join(root, "alias")
	if err := os.MkdirAll(filepath.Join(physical, "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(physical, alias); err != nil {
		t.Fatal(err)
	}
	transcripts := []store.Transcript{
		transcript("one", filepath.Join(alias, "projects", "one.jsonl"), "/work/one", "one", 1, 1, 1),
		transcript("two", filepath.Join(root, "two", "projects", "two.jsonl"), "/work/two", "two", 1, 1, 2),
	}
	output := Compose(Input{
		Transcripts: transcripts,
		AccountRoots: []AccountRoot{
			{Account: 1, Path: filepath.Join(alias, "projects")},
			{Account: 2, Path: filepath.Join(root, "two", "projects")},
		},
		Options: Options{View: AllView},
	})
	for _, want := range []struct {
		id      string
		account int
	}{{"one", 1}, {"two", 2}} {
		row, ok := rowByID(output.Rows, want.id)
		if !ok || row.Account != want.account {
			t.Fatalf("row %s = %#v, want account %d", want.id, row, want.account)
		}
	}
}

func TestPromptBaselineKillLiftsButPermanentKillDoesNot(t *testing.T) {
	baseline := int64(2)
	chat := transcript(
		"clear-killed",
		"/accounts/1/projects/clear/clear-killed.jsonl",
		"/work/clear",
		"Clear killed",
		100,
		3,
		1000,
	)
	input := Input{
		Transcripts: []store.Transcript{chat},
		Killed: []store.Killed{{
			ID: "clear-killed", Engine: pfmengine.Claude, BaselinePrompts: &baseline,
		}},
		Options: Options{View: DefaultView},
	}
	if _, found := rowByID(Compose(input).Rows, chat.UUID); !found {
		t.Fatal("a clear-killed chat stayed killed after its prompt count advanced")
	}
	input.Killed[0].BaselinePrompts = nil
	if _, found := rowByID(Compose(input).Rows, chat.UUID); found {
		t.Fatal("an explicit permanent kill lifted after prompt growth")
	}
}

func TestCrumbPrecedenceAndSocketTrust(t *testing.T) {
	input := fixtureInput(DefaultView)
	output := Compose(input)

	paneRow, found := rowByID(output.Rows, "pane")
	if !found || paneRow.Kind != LiveClaude || paneRow.Socket != "cc-200-1-1" {
		t.Fatalf("pane crumb did not win: %#v", paneRow)
	}
	socketRow, found := rowByID(output.Rows, "live-socket")
	if !found || socketRow.Kind != LiveClaude || socketRow.Socket != "cc-100-1-1" {
		t.Fatalf("trusted live socket crumb missing: %#v", socketRow)
	}

	distrusted := transcript(
		"gone",
		"/accounts/1/projects/gone/gone.jsonl",
		"/work/gone",
		"Stale socket crumb",
		10,
		1,
		1400,
	)
	input.Transcripts = append(input.Transcripts, distrusted)
	input.Snapshot.Panes = append(input.Snapshot.Panes, gather.ProbePane{
		Socket: "cc-500-1-1",
		PaneID: "%5",
	})
	input.Snapshot.Crumbs = append(input.Snapshot.Crumbs, gather.Crumb{
		Filename:       "cc-500-1-1",
		Socket:         "cc-500-1-1",
		TranscriptPath: distrusted.Path,
	})

	output = Compose(input)
	gone, found := rowByID(output.Rows, "gone")
	if !found || gone.Kind != ResumeClaude || gone.Socket != "" {
		t.Fatalf("stale socket crumb was trusted: %#v", gone)
	}
}

func TestLiveEnrichmentJoinsByIdentityAcrossPathAliases(t *testing.T) {
	claude := transcript(
		"aliased-claude",
		"/canonical/projects/live/aliased-claude.jsonl",
		"/work/proja",
		"Aliased Claude",
		900,
		37,
		1200,
	)
	codex := store.Rollout{
		ID:          "019f-live-codex",
		Path:        "/canonical/codex/rollout-2026-01-01T00-00-00-019f-live-codex.jsonl",
		Size:        800,
		MTimeNS:     1300,
		CWD:         "/work/projb-fe",
		UserThread:  true,
		FirstPrompt: "Aliased Codex",
		PromptCount: 21,
	}
	input := Input{
		Transcripts: []store.Transcript{claude},
		Rollouts:    []store.Rollout{codex},
		Snapshot: gather.Snapshot{
			Panes: []gather.ProbePane{
				{Socket: "cc-1-2-3", PaneID: "%1"},
				{Socket: "cx-4-5-6", PaneID: "%2"},
			},
			Crumbs: []gather.Crumb{{
				Filename: "cc-1-2-3.%1",
				Socket:   "cc-1-2-3",
				PaneID:   "%1",
				TranscriptPath: "/alias/projects/live/" +
					"aliased-claude.jsonl",
			}},
			Codex: []gather.LiveCodex{{
				Socket: "cx-4-5-6",
				PaneID: "%2",
				RolloutPath: "/alias/codex/" +
					"rollout-2026-01-01T00-00-00-019f-live-codex.jsonl",
			}},
		},
		Options: Options{
			View:       AllView,
			CurrentDir: "/work/host-ops",
		},
	}
	output := Compose(input)
	claudeRow, found := rowByID(output.Rows, claude.UUID)
	if !found ||
		claudeRow.Kind != LiveClaude ||
		claudeRow.Project != "proja" ||
		claudeRow.PromptCount != 37 {
		t.Fatalf("aliased live Claude enrichment = %#v", claudeRow)
	}
	codexRow, found := rowByID(output.Rows, codex.ID)
	if !found ||
		codexRow.Kind != LiveCodex ||
		codexRow.Project != "projb-fe" ||
		codexRow.PromptCount != 21 {
		t.Fatalf("aliased live Codex enrichment = %#v", codexRow)
	}
}

func TestLiveProjectFallsBackToPaneCurrentPath(t *testing.T) {
	output := Compose(Input{
		Snapshot: gather.Snapshot{
			Panes: []gather.ProbePane{{
				Socket:      "cc-7-8-9",
				PaneID:      "%7",
				CurrentPath: "/work/proja",
			}},
			Crumbs: []gather.Crumb{{
				Filename:       "cc-7-8-9.%7",
				Socket:         "cc-7-8-9",
				PaneID:         "%7",
				TranscriptPath: "/missing/zero-prompt.jsonl",
			}},
		},
		Options: Options{View: AllView, CurrentDir: "/work/host-ops"},
	})
	row, found := rowByID(output.Rows, "zero-prompt")
	if !found || row.Kind != LiveClaude || row.Project != "proja" ||
		row.PromptCount != 0 {
		t.Fatalf("pane-path fallback row = %#v", row)
	}
}

func TestSplitMergeAndNewestServerCollapse(t *testing.T) {
	input := splitFixtureInput()
	output := Compose(input)
	split := rowsByKind(output.Rows, LiveSplit)
	if len(split) != 1 {
		t.Fatalf("split rows = %#v, want one", split)
	}
	if split[0].Name != "One+Two" ||
		split[0].Size != 350 ||
		split[0].PromptCount != 5 ||
		split[0].SplitCount != 2 ||
		split[0].Killed ||
		!reflect.DeepEqual(split[0].Accounts, []int{1, 2}) {
		t.Fatalf("merged split = %#v", split[0])
	}

	collapsed, found := rowByID(output.Rows, "duplicate")
	if !found ||
		collapsed.Kind != LiveClaude ||
		collapsed.Socket != "cc-300-1-1" ||
		collapsed.ServerCount != 2 {
		t.Fatalf("collapsed duplicate = %#v", collapsed)
	}
}

func splitFixtureInput() Input {
	splitOne := transcript(
		"split-one",
		"/accounts/1/projects/split/split-one.jsonl",
		"/work/split",
		"One",
		100,
		2,
		100,
	)
	splitTwo := transcript(
		"split-two",
		"/accounts/2/projects/split/split-two.jsonl",
		"/work/split",
		"Two",
		250,
		3,
		200,
	)
	duplicate := transcript(
		"duplicate",
		"/accounts/1/projects/dup/duplicate.jsonl",
		"/work/dup",
		"Duplicate",
		500,
		8,
		300,
	)
	baseline := int64(100)
	return Input{
		Transcripts: []store.Transcript{splitOne, splitTwo, duplicate},
		Killed: []store.Killed{
			{ID: "split-one", Engine: "cc", BaselinePrompts: &baseline},
			{ID: "split-two", Engine: "cc", BaselinePrompts: &baseline},
		},
		AccountRoots: fixtureAccountRoots(),
		Snapshot: gather.Snapshot{
			Panes: []gather.ProbePane{
				{Socket: "cc-100-1-1", PaneID: "%1", PaneTitle: "one"},
				{Socket: "cc-100-1-1", PaneID: "%2", PaneTitle: "two"},
				{Socket: "cc-200-1-1", PaneID: "%3"},
				{Socket: "cc-300-1-1", PaneID: "%4"},
			},
			Crumbs: []gather.Crumb{
				{
					Filename:       "cc-100-1-1.%1",
					Socket:         "cc-100-1-1",
					PaneID:         "%1",
					TranscriptPath: splitOne.Path,
				},
				{
					Filename:       "cc-100-1-1.%2",
					Socket:         "cc-100-1-1",
					PaneID:         "%2",
					TranscriptPath: splitTwo.Path,
				},
				{
					Filename:       "cc-200-1-1",
					Socket:         "cc-200-1-1",
					TranscriptPath: duplicate.Path,
				},
				{
					Filename:       "cc-300-1-1",
					Socket:         "cc-300-1-1",
					TranscriptPath: duplicate.Path,
				},
			},
			ClaudeProcesses: []gather.ClaudeProcess{
				{Socket: "cc-200-1-1", PaneID: "%3"},
				{Socket: "cc-300-1-1", PaneID: "%4"},
			},
		},
		Options: Options{CurrentDir: "/work/split"},
	}
}

func TestPermanentAndPromptBaselineKillsAcrossViews(t *testing.T) {
	defaultOutput := Compose(fixtureInput(DefaultView))
	for _, id := range []string{"killed", "cx-killed"} {
		if _, found := rowByID(defaultOutput.Rows, id); found {
			t.Fatalf("killed row %q leaked into default view", id)
		}
	}
	if _, found := rowByID(defaultOutput.Rows, "grown"); !found {
		t.Fatal("prompt-baseline kill did not lift after the transcript grew")
	}
	killedOutput := Compose(fixtureInput(KilledView))
	if got, want := rowIDs(killedOutput.Rows), []string{"cx-killed", "killed"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("killed row IDs = %q, want %q", got, want)
	}
	for _, row := range killedOutput.Rows {
		if !row.Killed {
			t.Fatalf("KilledView row is not tagged killed: %#v", row)
		}
	}

	allOutput := Compose(fixtureInput(AllView))
	for _, id := range []string{"bg", "zero", "promptless", "killed", "grown", "cx-killed"} {
		if _, found := rowByID(allOutput.Rows, id); !found {
			t.Fatalf("-a omitted %q", id)
		}
	}
}

func TestBGAndEmptySuppressionNeverKillsAgent(t *testing.T) {
	output := Compose(fixtureInput(DefaultView))
	for _, id := range []string{"bg", "zero", "promptless"} {
		if _, found := rowByID(output.Rows, id); found {
			t.Fatalf("default view included suppressed %q", id)
		}
	}
	agent, found := rowByID(output.Rows, "agent")
	if !found || agent.Kind != Agent || !agent.BG || agent.Size != 0 || agent.PromptCount != 0 {
		t.Fatalf("empty bg agent was suppressed: %#v", agent)
	}
	if output.KilledCount != 2 || output.SuppressedCount != 3 {
		t.Fatalf(
			"honest header counts killed/empty=%d/%d, want 2/3",
			output.KilledCount,
			output.SuppressedCount,
		)
	}
}

func TestResumeCapsApplyAfterFiltering(t *testing.T) {
	input := capsFixtureInput()
	defaultOutput := Compose(input)
	if got := len(rowsByKind(defaultOutput.Rows, ResumeClaude)); got != 30 {
		t.Fatalf("default Claude resumes = %d, want 30", got)
	}
	if got := len(rowsByKind(defaultOutput.Rows, ResumeCodex)); got != 15 {
		t.Fatalf("default Codex resumes = %d, want 15", got)
	}
	if defaultOutput.KilledCount != 0 || defaultOutput.SuppressedCount != 10 {
		t.Fatalf(
			"killed/suppressed = %d/%d, want 0/10 capped rows",
			defaultOutput.KilledCount,
			defaultOutput.SuppressedCount,
		)
	}
	if _, found := rowByID(defaultOutput.Rows, "claude-00"); found {
		t.Fatal("oldest capped Claude row was retained")
	}
	if _, found := rowByID(defaultOutput.Rows, "codex-00"); found {
		t.Fatal("oldest capped Codex row was retained")
	}

	input.Options.View = AllView
	allOutput := Compose(input)
	if got := len(rowsByKind(allOutput.Rows, ResumeClaude)); got != 35 {
		t.Fatalf("-a Claude resumes = %d, want 35", got)
	}
	if got := len(rowsByKind(allOutput.Rows, ResumeCodex)); got != 20 {
		t.Fatalf("-a Codex resumes = %d, want 20", got)
	}
}

// TestBootingRowSurfacesFromCrumblessLiveAndResistsHiding is the compose-level
// half of the booting-chat fix: a crumbless-live entry gather emits must turn
// into exactly one Booting row, visible in DefaultView (unlike an ordinary
// empty-transcript live row, which the emptiness test would suppress), and
// immune to a kill entry that happens to share its socket-as-id — a booting
// row is never killable because its "id" stops meaning anything the moment
// the crumb lands and the row becomes an ordinary live one.
func TestBootingRowSurfacesFromCrumblessLiveAndResistsHiding(t *testing.T) {
	input := Input{
		Snapshot: gather.Snapshot{
			CrumblessLive: []gather.CrumblessLive{{
				Socket:        "cc-new-fixture-1",
				SessionName:   "cc-new-fixture-1",
				WindowID:      "@1",
				WindowName:    "Promptless worker",
				PaneID:        "%1",
				PID:           900,
				CWD:           "/work/booting-project",
				PaneStartUnix: 0,
			}},
		},
		Killed:  []store.Killed{{ID: "cc-new-fixture-1", Engine: "cc"}},
		Options: Options{View: DefaultView, CurrentDir: "/work/host-ops"},
	}

	output := Compose(input)
	row, found := rowByID(output.Rows, "cc-new-fixture-1")
	if !found {
		t.Fatalf(
			"DefaultView omitted the booting row: %#v",
			rowsByKind(output.Rows, Booting),
		)
	}
	if row.Kind != Booting ||
		row.Socket != "cc-new-fixture-1" ||
		row.PaneID != "%1" ||
		row.Name != "Promptless worker" ||
		row.Project != "booting-project" ||
		row.CWD != "/work/booting-project" ||
		row.Killed {
		t.Fatalf("booting row = %#v", row)
	}
	if output.KilledCount != 0 {
		t.Fatalf(
			"a kill entry keyed on the crumbless socket was honored: killed=%d",
			output.KilledCount,
		)
	}
}

func TestLiveCodexRequiresProcessAndCurrentPane(t *testing.T) {
	input := fixtureInput(AllView)
	input.Snapshot.Codex = nil
	output := Compose(input)
	if _, found := rowByID(output.Rows, "cx-live"); !found {
		t.Fatal("socket-only Codex rollout did not fall back to resume")
	}
	for _, row := range output.Rows {
		if row.ID == "cx-live" && row.Kind == LiveCodex {
			t.Fatalf("socket-only rollout stayed live: %#v", row)
		}
	}

	input = fixtureInput(AllView)
	panes := input.Snapshot.Panes[:0]
	for _, pane := range input.Snapshot.Panes {
		if pane.Socket != "cx-300-1-1" {
			panes = append(panes, pane)
		}
	}
	input.Snapshot.Panes = panes
	output = Compose(input)
	row, found := rowByID(output.Rows, "cx-live")
	if !found || row.Kind != ResumeCodex || row.Socket != "" {
		t.Fatalf("fd-walk process with vanished pane = %#v, found=%t", row, found)
	}
}

func capsFixtureInput() Input {
	input := Input{
		AccountRoots: fixtureAccountRoots(),
		Options:      Options{CurrentDir: "/work/caps"},
	}
	for index := 0; index < 35; index++ {
		input.Transcripts = append(input.Transcripts, transcript(
			idNumber("claude", index),
			"/accounts/1/projects/caps/"+idNumber("claude", index)+".jsonl",
			"/work/caps",
			idNumber("Claude", index),
			100,
			1,
			int64(index+1),
		))
	}
	for index := 0; index < 20; index++ {
		input.Rollouts = append(input.Rollouts, store.Rollout{
			ID:          idNumber("codex", index),
			Path:        "/codex/" + idNumber("codex", index) + ".jsonl",
			Size:        100,
			MTimeNS:     int64(index + 1),
			CWD:         "/work/caps",
			UserThread:  true,
			FirstPrompt: idNumber("Codex", index),
			PromptCount: 1,
		})
	}
	return input
}

func TestNaturalProjectOrderAndProjectDirTarget(t *testing.T) {
	input := fixtureInput(DefaultView)
	output := Compose(input)
	if len(output.ProjectOrder) < 3 {
		t.Fatalf("ProjectOrder = %q, want several blocks", output.ProjectOrder)
	}
	for _, row := range output.Rows[:2] {
		if row.Project != output.ProjectOrder[0] ||
			row.CWD != output.ProjectDirs[output.ProjectOrder[0]] {
			t.Fatalf("new row has stale launch target: %#v", row)
		}
	}
	if got := output.ProjectDirs["alpha"]; got != "/work/alpha" {
		t.Fatalf("PWD-seeded ProjectDirs[alpha] = %q", got)
	}

	again := Compose(input)
	if !reflect.DeepEqual(output.Rows, again.Rows) ||
		output.KilledCount != again.KilledCount ||
		output.SuppressedCount != again.SuppressedCount {
		t.Fatal("composition is not deterministic/idempotent for identical input")
	}
}

func fixtureInput(view View) Input {
	killedBaseline := int64(3)
	staleBaseline := int64(4)
	transcripts := []store.Transcript{
		transcript(
			"live-socket",
			"/accounts/1/projects/alpha/live-socket.jsonl",
			"/work/alpha",
			"Socket Live",
			100,
			5,
			900,
		),
		transcript(
			"pane",
			"/accounts/2/projects/alpha/pane.jsonl",
			"/work/alpha",
			"Pane Wins",
			200,
			7,
			1000,
		),
		transcript(
			"killed",
			"/accounts/1/projects/beta/killed.jsonl",
			"/work/beta",
			"Killed",
			100,
			3,
			800,
		),
		transcript(
			"grown",
			"/accounts/1/projects/gamma/grown.jsonl",
			"/work/gamma",
			"Grown Killed",
			100,
			5,
			700,
		),
		{
			UUID:        "bg",
			Path:        "/accounts/1/projects/beta/bg.jsonl",
			Size:        100,
			MTimeNS:     600,
			CWD:         "/work/beta",
			CustomTitle: "BG Twin",
			PromptCount: 2,
			IsBG:        true,
		},
		transcript(
			"zero",
			"/accounts/1/projects/beta/zero.jsonl",
			"/work/beta",
			"Zero",
			0,
			0,
			500,
		),
		transcript(
			"promptless",
			"/accounts/1/projects/delta/promptless.jsonl",
			"/work/delta",
			"Promptless",
			100,
			0,
			400,
		),
		{
			UUID:        "agent",
			Path:        "/accounts/3/projects/agents/agent.jsonl",
			MTimeNS:     1100,
			CWD:         "/work/agents",
			CustomTitle: "Worker",
			IsBG:        true,
		},
		transcript(
			"resume",
			"/accounts/1/projects/newest/resume.jsonl",
			"/work/newest",
			"Resume",
			100,
			4,
			1200,
		),
	}
	rollouts := []store.Rollout{
		{
			ID:          "cx-live",
			Path:        "/codex/sessions/rollout-2026-01-01T00-00-00-cx-live.jsonl",
			Size:        300,
			MTimeNS:     1300,
			CWD:         "/work/codex",
			UserThread:  true,
			FirstPrompt: "Codex live fallback",
			PromptCount: 8,
		},
		{
			ID:          "cx-resume",
			Path:        "/codex/sessions/rollout-2026-01-01T00-00-00-cx-resume.jsonl",
			Size:        250,
			MTimeNS:     650,
			CWD:         "/work/beta",
			UserThread:  true,
			FirstPrompt: "Codex Resume",
			PromptCount: 6,
		},
		{
			ID:          "cx-killed",
			Path:        "/codex/sessions/rollout-2026-01-01T00-00-00-cx-killed.jsonl",
			Size:        150,
			MTimeNS:     550,
			CWD:         "/work/beta",
			UserThread:  true,
			FirstPrompt: "Codex Killed",
			PromptCount: 2,
		},
		{
			ID:          "cx-subagent",
			Path:        "/codex/sessions/subagents/rollout-cx-subagent.jsonl",
			Size:        100,
			MTimeNS:     2000,
			CWD:         "/work/sub",
			FirstPrompt: "Subagent",
			PromptCount: 1,
		},
	}
	return Input{
		Transcripts: transcripts,
		Rollouts:    rollouts,
		CxNames: map[string]string{
			"cx-live": "Codex Live",
		},
		Killed: []store.Killed{
			{
				ID:              "killed",
				Engine:          "cc",
				BaselinePrompts: &killedBaseline,
			},
			{
				// Prompt count 5 passed this /clear baseline, so the row is
				// visible again in default/all views.
				ID:              "grown",
				Engine:          "cc",
				BaselinePrompts: &staleBaseline,
			},
			{
				ID:     "cx-killed",
				Engine: "cx",
			},
		},
		AccountRoots: fixtureAccountRoots(),
		Snapshot: gather.Snapshot{
			Panes: []gather.ProbePane{
				{
					Socket:      "cc-100-1-1",
					SessionName: "cc-old",
					PaneTitle:   "Claude Code",
					PaneID:      "%1",
				},
				{
					Socket:      "cc-200-1-1",
					SessionName: "cc-pane",
					PaneTitle:   "Pane title",
					PaneID:      "%2",
					Attached:    true,
				},
				{
					Socket:      "cx-300-1-1",
					SessionName: "cx-live",
					PaneID:      "%3",
				},
				{
					Socket:      "cc-400-1-1",
					SessionName: "agent",
					PaneID:      "%4",
				},
			},
			Crumbs: []gather.Crumb{
				{
					Filename:       "cc-100-1-1",
					Socket:         "cc-100-1-1",
					TranscriptPath: transcripts[0].Path,
				},
				{
					Filename:       "cc-200-1-1",
					Socket:         "cc-200-1-1",
					TranscriptPath: transcripts[0].Path,
				},
				{
					Filename:       "cc-200-1-1.%2",
					Socket:         "cc-200-1-1",
					PaneID:         "%2",
					TranscriptPath: transcripts[1].Path,
				},
			},
			Codex: []gather.LiveCodex{{
				PID:         300,
				Socket:      "cx-300-1-1",
				PaneID:      "%3",
				RolloutPath: rollouts[0].Path,
			}},
			ClaudeProcesses: []gather.ClaudeProcess{
				{PID: 100, Socket: "cc-100-1-1", PaneID: "%1"},
				{PID: 200, Socket: "cc-200-1-1", PaneID: "%2"},
				{PID: 400, Socket: "cc-400-1-1", PaneID: "%4"},
			},
			Agents: []gather.Agent{{
				PID:       400,
				Socket:    "cc-400-1-1",
				PaneID:    "%4",
				SessionID: "agent",
				ConfigDir: "/accounts/3",
			}},
			Cache1HSockets: []string{"cc-200-1-1"},
		},
		Options: Options{
			View:                view,
			CurrentDir:          "/work/alpha",
			CurrentSocket:       "cc-200-1-1",
			PrimaryAccount:      2,
			CodexAccountIDs:     []int{1},
			PrimaryCodexAccount: 1,
			NowNS:               5000,
		},
	}
}

func transcript(
	id, path, cwd, name string,
	size, prompts, modified int64,
) store.Transcript {
	return store.Transcript{
		UUID:        id,
		Path:        path,
		Size:        size,
		MTimeNS:     modified,
		CWD:         cwd,
		CustomTitle: name,
		FirstPrompt: name,
		LastPrompt:  name,
		PromptCount: prompts,
	}
}

func fixtureAccountRoots() []AccountRoot {
	return []AccountRoot{
		{Account: 1, Path: "/accounts/1"},
		{Account: 2, Path: "/accounts/2"},
		{Account: 3, Path: "/accounts/3"},
	}
}

func rowByID(rows []Row, id string) (Row, bool) {
	for index := range rows {
		row := rows[index]
		if row.ID == id {
			return row, true
		}
	}
	return Row{}, false
}

func rowsByKind(rows []Row, kind Kind) []Row {
	var result []Row
	for index := range rows {
		row := rows[index]
		if row.Kind == kind {
			result = append(result, row)
		}
	}
	return result
}

func rowIDs(rows []Row) []string {
	var ids []string
	for index := range rows {
		row := rows[index]
		if row.ID != "" {
			ids = append(ids, row.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

func idNumber(prefix string, number int) string {
	const digits = "0123456789"
	return prefix + "-" + string([]byte{
		digits[(number/10)%10],
		digits[number%10],
	})
}

// A paginated Codex conversation writes NO rollout file, so the live process
// holds no rollout path and only gather's own resolution names it. Deriving
// the row's id from the path alone minted the EMPTY id: an unkillable row
// (applyKill refuses an empty id) that never marked its conversation live, so
// the same chat came back a second time as a resume row underneath itself.
func TestLiveCodexWithoutARolloutFileIsOneKillableRow(t *testing.T) {
	paginated := store.Rollout{
		ID:          "019fed79-74c9-7c62-a224-4581ba81d4f6",
		Path:        "/codex/sessions/019fed79-74c9-7c62-a224-4581ba81d4f6.jsonl",
		CWD:         "/work/proja",
		UserThread:  true,
		LineageRoot: "019fed79-74c9-7c62-a224-4581ba81d4f6",
		FirstPrompt: "paginated first prompt",
		PromptCount: 1,
		MTimeNS:     1_700_000_000_000_000_000,
		Size:        0,
	}
	output := Compose(Input{
		Rollouts: []store.Rollout{paginated},
		Snapshot: gather.Snapshot{
			Codex: []gather.LiveCodex{{
				PID:      300,
				Socket:   "cx-300-1-1",
				PaneID:   "%3",
				ThreadID: paginated.ID,
			}},
			Panes: []gather.ProbePane{{
				Socket:      "cx-300-1-1",
				SessionName: "cx-300-1-1",
				PaneID:      "%3",
				CurrentPath: "/work/proja",
			}},
		},
		Options: Options{View: AllView},
	})

	codexRows := make([]Row, 0, len(output.Rows))
	for _, row := range output.Rows {
		if row.Kind == LiveCodex || row.Kind == ResumeCodex {
			codexRows = append(codexRows, row)
		}
	}
	if len(codexRows) != 1 {
		t.Fatalf("Codex rows = %#v, want the conversation rowed exactly once", codexRows)
	}
	if codexRows[0].Kind != LiveCodex || codexRows[0].ID != paginated.ID {
		t.Fatalf("live Codex row = %#v, want the resolved thread id", codexRows[0])
	}
	if codexRows[0].PromptCount != 1 || codexRows[0].CWD != "/work/proja" {
		t.Fatalf("live Codex row lost its indexed content: %#v", codexRows[0])
	}

	// An id is what a kill keys on, so the row is killable now.
	killed := Compose(Input{
		Rollouts: []store.Rollout{paginated},
		Killed:   []store.Killed{{ID: paginated.ID, Engine: pfmengine.Codex}},
		Snapshot: gather.Snapshot{
			Codex: []gather.LiveCodex{{
				PID:      300,
				Socket:   "cx-300-1-1",
				PaneID:   "%3",
				ThreadID: paginated.ID,
			}},
			Panes: []gather.ProbePane{{
				Socket:      "cx-300-1-1",
				SessionName: "cx-300-1-1",
				PaneID:      "%3",
				CurrentPath: "/work/proja",
			}},
		},
		Options: Options{View: DefaultView},
	})
	for _, row := range killed.Rows {
		if row.Kind == LiveCodex || row.Kind == ResumeCodex {
			t.Fatalf("killed live Codex row is still listed: %#v", row)
		}
	}
}

// THE BUG. Codex >=0.146.1 keeps a paginated thread's content in its own
// sqlite state store; such a thread's rollout .jsonl stays empty or
// header-only, so gather/index parse it to prompt_count=0 and first_prompt=""
// before the index has any chance to enrich it from the store. A row that is
// still genuinely live in tmux must never be suppressed by an emptiness test
// meant to catch abandoned zero-byte spawns — Agent and Booting rows already
// carry this exemption (compose.go's own comment says so); a live Codex row
// needs it for exactly the same reason.
func TestLiveCodexIsExemptFromEmptinessSuppression(t *testing.T) {
	const threadID = "019fed7f-aac7-7650-8a8b-96ab595179ab"
	output := Compose(Input{
		Snapshot: gather.Snapshot{
			Codex: []gather.LiveCodex{{
				PID:      400,
				Socket:   "cx-400-1-1",
				PaneID:   "%4",
				ThreadID: threadID,
			}},
			Panes: []gather.ProbePane{{
				Socket:      "cx-400-1-1",
				SessionName: "cx-400-1-1",
				PaneID:      "%4",
				CurrentPath: "/work/paginated",
			}},
		},
		Options: Options{View: DefaultView},
	})
	row, found := rowByID(output.Rows, threadID)
	if !found || row.Kind != LiveCodex {
		t.Fatalf(
			"live Codex row with prompt_count=0 vanished from the default view: found=%t row=%#v",
			found,
			row,
		)
	}
}

// A live Codex row inherits background classification from its rollout: a
// machine-spawned thread caught live in a pane must stay suppressed exactly
// like its resume-shape twin (index's TestMachineSpawnedCodexThreadsAreBackground)
// — the liveness exemption above waives the emptiness test, not the
// background one.
func TestLiveMachineSpawnedCodexStaysSuppressed(t *testing.T) {
	const threadID = "019fed7f-aac7-7650-8a8b-96ab595179ac"
	output := Compose(Input{
		Rollouts: []store.Rollout{{
			ID:         threadID,
			UserThread: true,
			IsBG:       true,
		}},
		Snapshot: gather.Snapshot{
			Codex: []gather.LiveCodex{{
				PID:      401,
				Socket:   "cx-401-1-1",
				PaneID:   "%5",
				ThreadID: threadID,
			}},
			Panes: []gather.ProbePane{{
				Socket:      "cx-401-1-1",
				SessionName: "cx-401-1-1",
				PaneID:      "%5",
				CurrentPath: "/work/machine",
			}},
		},
		Options: Options{View: DefaultView},
	})
	if _, found := rowByID(output.Rows, threadID); found {
		t.Fatal("a live machine-spawned Codex row leaked into the default view")
	}
}

// TestKillOnAnyLineageMemberKillsTheWholeRow guards the RAW-id write shape
// an older writer produced: it kills the RAW id of whatever rollout file the
// live process currently holds, which on a resumed multi-file lineage is the
// CHILD's id, never the ROOT compose keys every Codex row on
// (composer.rolloutRow). A kill the `kill` manager itself writes is already
// normalized onto the root; this proves the read side honors a kill that
// isn't.
func TestKillOnAnyLineageMemberKillsTheWholeRow(t *testing.T) {
	root := store.Rollout{
		ID:          "root-thread",
		Path:        "/codex/sessions/rollout-2026-01-01T00-00-00-root-thread.jsonl",
		Size:        100,
		MTimeNS:     100,
		CWD:         "/work/proja",
		UserThread:  true,
		SessionID:   "root-thread",
		FirstPrompt: "root prompt",
		PromptCount: 1,
	}
	// The resumed child: same conversation, a LATER file, linked back to the
	// root by session_id — exactly what `codex resume` produces.
	child := store.Rollout{
		ID:          "child-thread",
		Path:        "/codex/sessions/rollout-2026-01-02T00-00-00-child-thread.jsonl",
		Size:        200,
		MTimeNS:     200,
		CWD:         "/work/proja",
		UserThread:  true,
		SessionID:   "root-thread",
		FirstPrompt: "root prompt",
		PromptCount: 2,
	}
	rollouts := []store.Rollout{root, child}

	visible := Compose(Input{
		Rollouts: rollouts,
		Options:  Options{View: DefaultView},
	})
	if _, found := rowByID(visible.Rows, "root-thread"); !found {
		t.Fatalf("unkilled lineage is missing: %#v", visible.Rows)
	}

	// The kill lands on the CHILD id, raw, exactly as that writer wrote it —
	// never normalized onto the root.
	killedOnChild := Compose(Input{
		Rollouts: rollouts,
		Killed:   []store.Killed{{ID: "child-thread", Engine: pfmengine.Codex}},
		Options:  Options{View: DefaultView},
	})
	if _, found := rowByID(killedOnChild.Rows, "root-thread"); found {
		t.Fatalf(
			"a kill written raw on the resumed child left the lineage listed: %#v",
			killedOnChild.Rows,
		)
	}

	// KilledView must show the same lineage, still keyed on the root — a
	// child-keyed kill is not a second, orphaned killed row.
	killedView := Compose(Input{
		Rollouts: rollouts,
		Killed:   []store.Killed{{ID: "child-thread", Engine: pfmengine.Codex}},
		Options:  Options{View: KilledView},
	})
	row, found := rowByID(killedView.Rows, "root-thread")
	if !found || !row.Killed {
		t.Fatalf("KilledView lost the child-keyed kill: %#v", killedView.Rows)
	}
	for _, other := range killedView.Rows {
		if other.ID == "child-thread" {
			t.Fatalf(
				"a child-keyed kill rowed a second, orphaned entry: %#v",
				killedView.Rows,
			)
		}
	}
}

// threeHopLineage builds root ← mid ← leaf, the exact repro chain: each
// rollout's SessionID names its own immediate parent (resolveCodexRoot in
// store/lineage.go walks that one hop at a time), so root is TWO hops from
// leaf — outside CxName's own one-hop own→session→parent walk. leaf is the
// newest by MTimeNS, so lineage.Newest is leaf.
func threeHopLineage() (root, mid, leaf store.Rollout) {
	root = store.Rollout{
		ID:          "root-thread",
		Path:        "/codex/sessions/rollout-2026-01-01T00-00-00-root-thread.jsonl",
		Size:        100,
		MTimeNS:     100,
		CWD:         "/work/proja",
		UserThread:  true,
		SessionID:   "root-thread",
		FirstPrompt: "root prompt",
		PromptCount: 1,
	}
	mid = store.Rollout{
		ID:          "mid-thread",
		Path:        "/codex/sessions/rollout-2026-01-02T00-00-00-mid-thread.jsonl",
		Size:        200,
		MTimeNS:     200,
		CWD:         "/work/proja",
		UserThread:  true,
		SessionID:   "root-thread",
		FirstPrompt: "mid prompt",
		PromptCount: 2,
	}
	leaf = store.Rollout{
		ID:          "leaf-thread",
		Path:        "/codex/sessions/rollout-2026-01-03T00-00-00-leaf-thread.jsonl",
		Size:        300,
		MTimeNS:     300,
		CWD:         "/work/proja",
		UserThread:  true,
		SessionID:   "mid-thread",
		FirstPrompt: "leaf prompt",
		PromptCount: 3,
	}
	return root, mid, leaf
}

// TestDeepCodexLineageNamesFromItsRoot is BUG 3's red-first repro: a lineage
// named only at its root, three hops deep, must display the root's name, not
// the newest member's first prompt. rolloutRow swapped rollout = lineage.Newest
// BEFORE calling naming.CxName, and CxName walks only one hop
// (own → session → parent) — so a name two hops up, at the root, was
// unreachable and the row fell back to the leaf's own first prompt.
func TestDeepCodexLineageNamesFromItsRoot(t *testing.T) {
	root, mid, leaf := threeHopLineage()
	rollouts := []store.Rollout{root, mid, leaf}

	output := Compose(Input{
		Rollouts: rollouts,
		CxNames:  map[string]string{root.ID: "Root Name"},
		Options:  Options{View: AllView},
	})
	row, found := rowByID(output.Rows, root.ID)
	if !found {
		t.Fatalf("lineage row missing: %#v", output.Rows)
	}
	if row.Name != "Root Name" {
		t.Fatalf("row name = %q, want the root's name %q", row.Name, "Root Name")
	}
	// The newest member still drives everything else about the row: renaming
	// only fixed the NAME lookup, not which file's stats the row reports.
	if row.Path != leaf.Path || row.CWD != leaf.CWD ||
		row.Size != leaf.Size || row.PromptCount != leaf.PromptCount ||
		row.ActivityNS != leaf.MTimeNS {
		t.Fatalf("row lost the newest member's own fields: %#v", row)
	}

	// A chain named only at the MID hop (one hop from leaf, root itself
	// unnamed) must still resolve — the root-first lookup falling through to
	// the newest member's own one-hop walk must not regress the case that
	// already worked.
	midNamed := Compose(Input{
		Rollouts: rollouts,
		CxNames:  map[string]string{mid.ID: "Mid Name"},
		Options:  Options{View: AllView},
	})
	midRow, found := rowByID(midNamed.Rows, root.ID)
	if !found || midRow.Name != "Mid Name" {
		t.Fatalf("mid-named row = %#v, want name %q", midRow, "Mid Name")
	}
}
