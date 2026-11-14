package chat

import (
	"errors"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/headless"
)

// TestRowStateNamesEveryLivenessWord pins a listed row's liveness word, what caller matching reads. A kill that
// closed nothing still wrote its tombstone: such a row says killed-but-live, never one of its two halves.
func TestRowStateNamesEveryLivenessWord(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		row  compose.Row
		want string
	}{
		{"live claude", compose.Row{Kind: compose.LiveClaude}, "idle"},
		{"live codex", compose.Row{Kind: compose.LiveCodex}, "idle"},
		{"live opencode", compose.Row{Kind: compose.LiveOpenCode}, "idle"},
		{"booting", compose.Row{Kind: compose.Booting}, "booting"},
		{"resumable", compose.Row{Kind: compose.ResumeClaude}, "resumable"},
		{"killed resumable", compose.Row{Kind: compose.ResumeClaude, Killed: true}, "resumable"},
		{"killed live claude", compose.Row{Kind: compose.LiveClaude, Killed: true}, "killed-but-live"},
		{"killed live codex", compose.Row{Kind: compose.LiveCodex, Killed: true}, "killed-but-live"},
		{"killed live opencode", compose.Row{Kind: compose.LiveOpenCode, Killed: true}, "killed-but-live"},
		{"killed live split", compose.Row{Kind: compose.LiveSplit, Killed: true}, "killed-but-live"},
		{"killed agent", compose.Row{Kind: compose.Agent, Killed: true}, "killed-but-live"},
		{"killed booting", compose.Row{Kind: compose.Booting, Killed: true}, "killed-but-live"},
		{"backend error", compose.Row{Kind: compose.LiveClaude, BackendError: "herdr down"}, "error"},
		{"lost herdr seat", compose.Row{Kind: compose.ResumeClaude, Backend: "herdr", LostNote: "gone"}, "lost"},
		{"herdr note on a live seat", compose.Row{Kind: compose.LiveClaude, Backend: "herdr", LostNote: "x"}, "idle"},
	}
	for _, testCase := range cases {
		if state := RowState(testCase.row); state != testCase.want {
			t.Errorf("%s: RowState = %q, want %q", testCase.name, state, testCase.want)
		}
	}
}

// TestListedStateInspectsEveryRowTheLiveListingShows pins the state word both ls doors show: a row the CLI's live
// listing shows says what its inspection read ("unknown" when the inspection failed), a booting one "booting"
// unless the inspection says more; a resumable or killed row keeps its liveness word and is never inspected.
func TestListedStateInspectsEveryRowTheLiveListingShows(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		row       compose.Row
		status    headless.Status
		err       error
		want      string
		inspected bool
	}{
		{
			"live working",
			compose.Row{Kind: compose.LiveClaude},
			headless.Status{State: "working"},
			nil,
			"working",
			true,
		},
		{"live idle", compose.Row{Kind: compose.LiveCodex}, headless.Status{State: "idle"}, nil, "idle", true},
		{
			"booting blocked",
			compose.Row{Kind: compose.Booting},
			headless.Status{State: "blocked"},
			nil,
			"blocked",
			true,
		},
		{"booting idle", compose.Row{Kind: compose.Booting}, headless.Status{State: "idle"}, nil, "booting", true},
		{
			"booting inspect failed",
			compose.Row{Kind: compose.Booting},
			headless.Status{},
			errors.New("x"),
			"booting",
			true,
		},
		{"inspect failed", compose.Row{Kind: compose.LiveClaude}, headless.Status{}, errors.New("x"), "unknown", true},
		{
			"lost herdr seat",
			compose.Row{Kind: compose.ResumeClaude, Backend: "herdr", LostNote: "gone"},
			headless.Status{State: "lost"},
			nil, "lost", true,
		},
		{
			"backend error",
			compose.Row{Kind: compose.LiveClaude, BackendError: "herdr down"},
			headless.Status{State: "dead"},
			nil, "dead", true,
		},
		{
			"resumable",
			compose.Row{Kind: compose.ResumeClaude},
			headless.Status{State: "working"},
			nil,
			"resumable",
			false,
		},
		{
			"killed but live",
			compose.Row{Kind: compose.LiveClaude, Killed: true},
			headless.Status{State: "working"},
			nil, "killed-but-live", false,
		},
	}
	for _, testCase := range cases {
		inspected := false
		state := ListedState(testCase.row, func() (headless.Status, error) {
			inspected = true
			return testCase.status, testCase.err
		})
		if state != testCase.want || inspected != testCase.inspected {
			t.Errorf("%s: ListedState = %q (inspected %v), want %q (inspected %v)",
				testCase.name, state, inspected, testCase.want, testCase.inspected)
		}
	}
}
