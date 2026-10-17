package compose

import (
	"errors"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/store"
)

func TestHerdrSeatsUseCanonicalEligibility(t *testing.T) {
	for _, tc := range []struct {
		name       string
		view       View
		label      string
		bg         bool
		kill       bool
		baseline   *int64
		prompts    int64
		wantRows   int
		wantKilled bool
	}{
		{name: "live default", label: "REVIEW", wantRows: 1},
		{name: "live killed", view: KilledView, label: "REVIEW"},
		{name: "label hidden", label: "_KILL_REVIEW"},
		{name: "label killed", view: KilledView, label: "_KILL_REVIEW", wantRows: 1, wantKilled: true},
		{name: "label all", view: AllView, label: "_KILL_REVIEW", wantRows: 1, wantKilled: true},
		{name: "stored hidden", label: "REVIEW", kill: true},
		{name: "stored killed", view: KilledView, label: "REVIEW", kill: true, wantRows: 1, wantKilled: true},
		{name: "baseline lifts", label: "REVIEW", kill: true, baseline: new(int64(1)), prompts: 2, wantRows: 1},
		{name: "baseline holds", view: KilledView, label: "REVIEW", kill: true, baseline: new(int64(1)), prompts: 1, wantRows: 1, wantKilled: true},
		{name: "background", label: "REVIEW", bg: true},
		{name: "background all", view: AllView, label: "REVIEW", bg: true, wantRows: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := Input{
				Options: Options{View: tc.view},
				Transcripts: []store.Transcript{
					{
						UUID:        "sid1",
						Path:        "/jail/session.jsonl",
						CustomTitle: "INDEX",
						IsBG:        tc.bg,
						PromptCount: tc.prompts,
					},
				},
				HerdrSeats: []gather.HerdrSeat{{Seat: fleetdb.Seat{
					SeatID: "seat1", SessionID: "sid1", Engine: "cc", Backend: "herdr", State: "live",
					Name: tc.label, HerdrSocket: "/jail/herdr.sock", HerdrPane: "w6:p3",
				}}},
			}
			if tc.kill {
				input.Killed = []store.Killed{{ID: "sid1", Engine: "cc", BaselinePrompts: tc.baseline}}
			}
			output := Compose(input)
			if len(output.Rows) != tc.wantRows {
				t.Fatalf("native rows=%+v, want %d", output.Rows, tc.wantRows)
			}
			if len(output.Rows) != 0 {
				row := output.Rows[0]
				if row.SeatID != "seat1" || row.Path != input.Transcripts[0].Path || row.Killed != tc.wantKilled ||
					row.BG != tc.bg {
					t.Fatalf("canonical native metadata=%+v", row)
				}
			}
		})
	}
}

func TestHerdrSeatsKeepDistinctAddressesAndTranscriptMetadata(t *testing.T) {
	input := Input{
		Options:     Options{View: AllView},
		Transcripts: []store.Transcript{{UUID: "shared", Path: "/jail/shared.jsonl", PromptCount: 3, Size: 42}},
		Snapshot: gather.Snapshot{
			Panes: []gather.ProbePane{{Socket: "cc-1-2-3", PaneID: "%0"}},
			Crumbs: []gather.Crumb{
				{Filename: "cc-1-2-3.%0", TranscriptPath: "/jail/shared.jsonl", Socket: "cc-1-2-3", PaneID: "%0"},
			},
			ClaudeProcesses: []gather.ClaudeProcess{{Socket: "cc-1-2-3"}},
		},
		HerdrSeats: []gather.HerdrSeat{
			{
				Seat: fleetdb.Seat{
					SeatID:      "native1",
					SessionID:   "shared",
					Engine:      "cc",
					Backend:     "herdr",
					State:       "live",
					HerdrSocket: "/jail/herdr.sock",
					HerdrPane:   "w6:p3",
				},
				Err: errors.New("HERDR UNREACHABLE first socket"),
			},
			{
				Seat: fleetdb.Seat{
					SeatID:      "native2",
					SessionID:   "shared",
					Engine:      "cc",
					Backend:     "herdr",
					State:       "live",
					HerdrSocket: "/jail/herdr.sock",
					HerdrPane:   "w6:p4",
				},
			},
		},
	}
	output := Compose(input)
	if len(output.Rows) != 3 {
		t.Fatalf("rows=%+v; want original tmux seat and both native seats", output.Rows)
	}
	bySeat := map[string]Row{}
	for _, row := range output.Rows {
		bySeat[row.SeatID] = row
		if row.ID != "shared" || row.Path != "/jail/shared.jsonl" || row.PromptCount != 3 || row.Size != 42 {
			t.Fatalf("session metadata lost: %+v", row)
		}
	}
	if bySeat[""].Socket != "cc-1-2-3" || bySeat["native1"].PaneID != "w6:p3" || bySeat["native2"].PaneID != "w6:p4" ||
		bySeat["native1"].BackendError == "" ||
		bySeat["native2"].BackendError != "" {
		t.Fatalf("seat address or error lost: %+v", bySeat)
	}
}
