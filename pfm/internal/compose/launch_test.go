package compose

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

func composeLaunches(t *testing.T, launches ...fleetdb.Launch) *fleetdb.Launches {
	t.Helper()
	values := paths.Values{StateDB: filepath.Join(t.TempDir(), "pfm.db")}
	for _, launch := range launches {
		if err := fleetdb.RecordLaunch(context.Background(), values, launch, 1); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := fleetdb.OpenLaunches(context.Background(), values)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	})
	return reader
}

const fixtureClaudeTranscriptPath = "/fixture/one/.claude/projects/S.jsonl"

func TestComposeNonLiveAccountUsesLaunchRecord(t *testing.T) {
	input := Input{
		Transcripts: []store.Transcript{transcript("S", fixtureClaudeTranscriptPath, "/work", "S", 1, 1, 1)},
		Launches:    composeLaunches(t, fleetdb.Launch{SessionID: "S", Engine: pfmengine.Claude, Account: 3}),
		Options:     Options{View: AllView},
	}
	row, found := rowByID(Compose(input).Rows, "S")
	if !found || row.Account != 3 || row.LaunchUnread {
		t.Fatalf("launch account = %#v, want account 3", row)
	}
}

func TestLiveClaudeAccountUsesImplicitSeatUnlessAgentNamesUnknownConfig(t *testing.T) {
	for _, test := range []struct {
		name      string
		agents    []gather.Agent
		processes []gather.ClaudeProcess
		launch    []fleetdb.Launch
		want      int
	}{
		{name: "default process config", want: 1},
		{
			// A failed environ read is no evidence against the launch record.
			name:      "unreadable live process config keeps its launch record",
			processes: []gather.ClaudeProcess{{Socket: "cc-S", PaneID: "%1", ConfigUnreadable: true}},
			launch:    []fleetdb.Launch{{SessionID: "S", Engine: pfmengine.Claude, Account: 2}},
			want:      2,
		},
		{
			name: "two Claude processes in one pane keep the launch record",
			processes: []gather.ClaudeProcess{
				{Socket: "cc-S", PaneID: "%1", ConfigDir: "/home/.claude"},
				{Socket: "cc-S", PaneID: "%1", ConfigDir: "/unknown"},
			},
			launch: []fleetdb.Launch{{SessionID: "S", Engine: pfmengine.Claude, Account: 2}},
			want:   2,
		},
		{name: "unknown explicit config", agents: []gather.Agent{{SessionID: "S", ConfigDir: "/unknown"}}, want: 0},
		{name: "unknown live process config", processes: []gather.ClaudeProcess{{Socket: "cc-S", PaneID: "%1", ConfigDir: "/unknown"}}, want: 0},
		{name: "unreadable live process config", processes: []gather.ClaudeProcess{{Socket: "cc-S", PaneID: "%1", ConfigUnreadable: true}}, want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			const transcriptPath = "/store/S.jsonl"
			input := Input{
				Transcripts: []store.Transcript{transcript("S", transcriptPath, "/work", "S", 1, 1, 1)},
				Snapshot: gather.Snapshot{
					Panes:           []gather.ProbePane{{Socket: "cc-S", PaneID: "%1"}},
					Crumbs:          []gather.Crumb{{Socket: "cc-S", PaneID: "%1", TranscriptPath: transcriptPath}},
					Agents:          test.agents,
					ClaudeProcesses: test.processes,
				},
				ClaudeSeats: []ClaudeSeat{{Account: 1, ConfigDir: "/home/.claude", Implicit: true}},
				Launches:    composeLaunches(t, test.launch...),
				Options:     Options{View: AllView},
			}
			row, found := rowByID(Compose(input).Rows, "S")
			if !found || row.Kind != LiveClaude || row.Account != test.want {
				t.Fatalf("live row = %#v, want account %d", row, test.want)
			}
		})
	}
}

func TestComposeNeverLaunchedHasNoAccountOrMedal(t *testing.T) {
	input := Input{
		Transcripts: []store.Transcript{transcript("S", fixtureClaudeTranscriptPath, "/work", "S", 1, 1, 1)},
		Launches:    composeLaunches(t), Options: Options{View: AllView},
	}
	row, found := rowByID(Compose(input).Rows, "S")
	if !found || row.Account != 0 || row.LaunchUnread {
		t.Fatalf("never-launched row = %#v, want account 0 without warning", row)
	}
}

func TestComposeLaunchReadFailureMarksRow(t *testing.T) {
	input := Input{
		Transcripts: []store.Transcript{transcript("S", fixtureClaudeTranscriptPath, "/work", "S", 1, 1, 1)},
		LaunchError: errors.New("unreadable pfm.db"), Options: Options{View: AllView},
	}
	row, found := rowByID(Compose(input).Rows, "S")
	if !found || !row.LaunchUnread || row.C1H {
		t.Fatalf("read failure row = %#v, want warning without badge", row)
	}
}

func TestComposeLiveBadgeUsesLaunchRecord(t *testing.T) {
	for _, test := range []struct {
		name   string
		launch *fleetdb.Launch
		want   bool
	}{
		{"1h", &fleetdb.Launch{SessionID: "S", Engine: pfmengine.Claude, Account: 2, Cache1H: true}, true},
		{"5m", &fleetdb.Launch{SessionID: "S", Engine: pfmengine.Claude, Account: 2}, false},
		{"none", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var records []fleetdb.Launch
			if test.launch != nil {
				records = append(records, *test.launch)
			}
			path := "/home/.claude/projects/S.jsonl"
			input := Input{
				Transcripts: []store.Transcript{transcript("S", path, "/work", "S", 1, 1, 1)},
				Launches:    composeLaunches(t, records...), Options: Options{View: AllView},
				Snapshot: gather.Snapshot{
					Panes:           []gather.ProbePane{{Socket: "cc-S", PaneID: "%1"}},
					Crumbs:          []gather.Crumb{{Socket: "cc-S", PaneID: "%1", TranscriptPath: path}},
					ClaudeProcesses: []gather.ClaudeProcess{{Socket: "cc-S", PaneID: "%1"}},
				},
			}
			row, found := rowByID(Compose(input).Rows, "S")
			if !found || row.Kind != LiveClaude || row.C1H != test.want {
				t.Fatalf("live row = %#v, want C1H %t", row, test.want)
			}
		})
	}
}
