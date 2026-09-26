package ui

import (
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

const fixtureNowNS = int64(1_800_000_000) * int64(time.Second)

func fixtureSnapshot(width int) Snapshot {
	ago := func(duration time.Duration) int64 {
		return fixtureNowNS - int64(duration)
	}
	return Snapshot{
		View:                compose.AllView,
		KilledCount:         1,
		PrimaryAccount:      2,
		AccountIDs:          []int{1, 2, 3},
		CodexPrimaryAccount: 3,
		CodexAccountIDs:     []int{1, 2, 3},
		CodexAccountEmojis:  map[int]string{1: "🥇", 2: "🥈", 3: "🥉"},
		Cache1H:             true,
		NowNS:               fixtureNowNS,
		Width:               width,
		Height:              17,
		// The production picker always wires an applier (a ⌃X with nowhere to
		// persist is refused), so the fixture carries one too.
		ApplyKill: func(KillChange) error { return nil },
		Rows: []compose.Row{
			{
				Kind:        compose.LiveClaude,
				ID:          "11111111-1111-4111-8111-111111111111",
				Socket:      "cc-1800000000-1-1",
				Name:        "123456789012345678901234567890X",
				LastPrompt:  "Implement the fleet picker without performing render-time I/O.",
				Project:     "alpha",
				CWD:         "/work/alpha",
				Size:        1536,
				PromptCount: 42,
				ActivityNS:  ago(5 * time.Second),
				Account:     2,
				C1H:         true,
				Attached:    true,
				Here:        true,
				ServerCount: 3,
			},
			{
				Kind:        compose.LiveCodex,
				ID:          "22222222-2222-4222-8222-222222222222",
				Socket:      "cx-1799999900-1-1",
				Name:        "RR",
				LastPrompt:  "Codex has a cached preview too.",
				Project:     "alpha",
				CWD:         "/work/alpha",
				Size:        3 * 1024 * 1024,
				PromptCount: 9,
				ActivityNS:  ago(20 * time.Minute),
			},
			{
				Kind:        compose.LiveSplit,
				Socket:      "cc-1799999800-1-1",
				Name:        "🚀🧭🛠️📦🧪✨ fleet",
				LastPrompt:  "Newest prompt across the split.",
				Project:     "alpha",
				CWD:         "/work/alpha",
				Size:        7200,
				PromptCount: 21,
				ActivityNS:  ago(2 * time.Hour),
				Accounts:    []int{1, 3},
				SplitCount:  3,
			},
			{
				Kind:        compose.Agent,
				ID:          "33333333-3333-4333-8333-333333333333",
				Name:        "Agent 界面 needle 列对齐测试名字",
				LastPrompt:  "Waiting for its next work package.",
				Project:     "beta",
				CWD:         "/work/beta",
				Size:        820,
				PromptCount: 7,
				ActivityNS:  ago(26 * time.Hour),
				Account:     3,
				Killed:      true,
			},
			{
				Kind:        compose.ResumeClaude,
				ID:          "44444444-4444-4444-8444-444444444444",
				Name:        "Resume Claude",
				LastPrompt:  "A resumable Claude prompt.",
				Project:     "beta",
				CWD:         "/work/beta",
				Size:        80 * 1024,
				PromptCount: 12,
				ActivityNS:  ago(3 * 24 * time.Hour),
				Account:     1,
			},
			{
				Kind:        compose.ResumeCodex,
				ID:          "55555555-5555-4555-8555-555555555555",
				Name:        "Resume Codex",
				Project:     "beta",
				CWD:         "/work/beta",
				Size:        5 * 1024,
				PromptCount: 4,
				ActivityNS:  ago(4 * 24 * time.Hour),
			},
			{
				Kind:       compose.NewClaude,
				Name:       "New Claude chat",
				Project:    "gamma",
				CWD:        "/work/gamma",
				ActivityNS: ago(6 * 24 * time.Hour),
			},
			{
				Kind:       compose.NewCodex,
				Name:       "New Codex chat",
				Project:    "gamma",
				CWD:        "/work/gamma",
				ActivityNS: ago(7 * 24 * time.Hour),
			},
		},
	}
}

func largeSnapshot(count int) Snapshot {
	rows := make([]compose.Row, count)
	for index := range rows {
		kind := compose.ResumeClaude
		if index%5 == 0 {
			kind = compose.ResumeCodex
		}
		rows[index] = compose.Row{
			Kind:        kind,
			ID:          fmt.Sprintf("%08x-0000-4000-8000-%012x", index, index),
			Name:        fmt.Sprintf("deterministic needle row %05d", index),
			LastPrompt:  fmt.Sprintf("cached prompt %05d", index),
			Project:     fmt.Sprintf("project-%03d", index%100),
			CWD:         fmt.Sprintf("/work/project-%03d", index%100),
			Size:        int64(index * 4096),
			PromptCount: int64(index % 200),
			ActivityNS:  fixtureNowNS - int64(index)*int64(time.Second),
			Account:     index%3 + 1,
		}
	}
	return Snapshot{
		Rows:           rows,
		View:           compose.AllView,
		PrimaryAccount: 1,
		AccountIDs:     []int{1, 2, 3},
		NowNS:          fixtureNowNS,
		Width:          120,
		Height:         30,
	}
}
