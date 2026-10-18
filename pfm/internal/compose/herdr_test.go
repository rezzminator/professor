package compose

import (
	"errors"
	"slices"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
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

func TestHerdrLiveLossNoteIsNotBackendFailure(t *testing.T) {
	const note = "your last message was not answered — re-send it"
	output := Compose(
		Input{
			HerdrSeats: []gather.HerdrSeat{
				{
					Seat: fleetdb.Seat{
						SeatID:    "s1",
						SessionID: "sid1",
						Engine:    "cc",
						Backend:   "herdr",
						State:     "live",
						LostNote:  note,
					},
				},
			},
		},
	)
	if len(output.Rows) != 1 || output.Rows[0].LostNote != note || output.Rows[0].BackendError != "" ||
		output.Rows[0].Kind != LiveClaude {
		t.Fatalf("live recovery note=%+v", output.Rows)
	}
}

// A nonprimary-account native process also appears in gather's Agent list.
// The durable native address wins over that process-only representation.
func TestHerdrSeatsSuppressOnlyMatchingProcessAgents(t *testing.T) {
	for _, state := range []string{"live", "failed"} {
		t.Run(state, func(t *testing.T) {
			input := Input{
				Options: Options{View: AllView, PrimaryAccount: 1},
				ClaudeSeats: []ClaudeSeat{
					{Account: 1, ConfigDir: "/accounts/primary"},
					{Account: 2, ConfigDir: "/accounts/secondary"},
				},
				Transcripts: []store.Transcript{
					{UUID: "native", Path: "/jail/native.jsonl", PromptCount: 3},
					{UUID: "agent", Path: "/jail/agent.jsonl"},
					{UUID: "tmux", Path: "/jail/tmux.jsonl"},
				},
				Snapshot: gather.Snapshot{
					Agents: []gather.Agent{
						{PID: 101, SessionID: "native", ConfigDir: "/accounts/secondary"},
						{PID: 102, SessionID: "agent", ConfigDir: "/accounts/secondary"},
					},
					Panes: []gather.ProbePane{{Socket: "cc-1-2-3", PaneID: "%0"}},
					Crumbs: []gather.Crumb{
						{Filename: "cc-1-2-3.%0", TranscriptPath: "/jail/tmux.jsonl", Socket: "cc-1-2-3", PaneID: "%0"},
					},
					ClaudeProcesses: []gather.ClaudeProcess{{Socket: "cc-1-2-3"}},
				},
				HerdrSeats: []gather.HerdrSeat{{Seat: fleetdb.Seat{
					SeatID:      "native-seat",
					SessionID:   "native",
					Engine:      string(pfmengine.Claude),
					Backend:     "herdr",
					State:       state,
					HerdrSocket: "/jail/herdr.sock",
					HerdrPane:   "w6:p3",
				}}},
			}
			output := Compose(input)
			if len(output.Rows) != 4 { // One configured new-chat row and three existing chats.
				t.Fatalf("native process must have one addressed row, retaining genuine Agent/tmux: %+v", output.Rows)
			}
			byID := map[string]Row{}
			for _, row := range output.Rows {
				byID[row.ID] = row
			}
			native := byID["native"]
			if native.Backend != "herdr" || native.SeatID != "native-seat" || native.PaneID != "w6:p3" ||
				native.PromptCount != 3 ||
				byID["agent"].Kind != Agent ||
				byID["tmux"].Kind != LiveClaude ||
				byID["tmux"].Socket != "cc-1-2-3" {
				t.Fatalf("canonical native or genuine Agent/tmux lost: %+v", output.Rows)
			}
		})
	}
}

// A row's UserName is the part of its name a user chose: a Claude chat's
// custom title (its /rename), a Codex thread's indexed name, a native seat's
// name (launch --name, pfm chat name) when the engine carries none, so a
// /rename inside a named seat names its row; a chat named by its AI title or
// first prompt has none. Every row read from its indexed source says so.
func TestRowUserNameIsTheNameAUserChose(t *testing.T) {
	renamed := transcript("cc-renamed", "/claude/cc-renamed.jsonl", "/work", "Mine", 100, 1, 1000)
	renamed.AITitle = "AI title"
	titled := transcript("cc-titled", "/claude/cc-titled.jsonl", "/work", "", 100, 1, 1000)
	titled.AITitle, titled.FirstPrompt = "AI title", "first prompt"
	seated := transcript("cc-seated", "/claude/cc-seated.jsonl", "/work", "Custom", 100, 1, 1000)
	named := transcript("cc-named", "/claude/cc-named.jsonl", "/work", "", 100, 1, 1000)
	named.AITitle = "AI title"
	output := Compose(Input{
		Transcripts: []store.Transcript{renamed, titled, seated, named},
		HerdrSeats: []gather.HerdrSeat{
			{Seat: fleetdb.Seat{
				SeatID: "renamed-seat", SessionID: "cc-seated", Engine: "cc", Backend: "herdr", State: "live",
				Name: "Seat name", HerdrSocket: "/jail/herdr.sock", HerdrPane: "w6:p3",
			}},
			{Seat: fleetdb.Seat{
				SeatID: "named-seat", SessionID: "cc-named", Engine: "cc", Backend: "herdr", State: "live",
				Name: "Seat name", HerdrSocket: "/jail/herdr.sock", HerdrPane: "w6:p4",
			}},
		},
		Rollouts: []store.Rollout{
			userNameRollout("cx-named"),
			userNameRollout("cx-prompt"),
		},
		CxNames: map[string]string{"cx-named": "Codex Named"},
		Options: Options{View: AllView},
	})
	for _, test := range []struct{ id, name, user string }{
		{"cc-renamed", "Mine", "Mine"},
		{"cc-titled", "AI title", ""},
		{"cx-named", "Codex Named", "Codex Named"},
		{"cx-prompt", "fp", ""},
	} {
		row, found := rowByID(output.Rows, test.id)
		if !found || row.Name != test.name || row.UserName != test.user || !row.UserNameKnown {
			t.Fatalf(
				"row %s = %#v (found %v), want name %q and user name %q",
				test.id,
				row,
				found,
				test.name,
				test.user,
			)
		}
	}
	for _, test := range []struct{ seat, name string }{
		{"renamed-seat", "Custom"},
		{"named-seat", "Seat name"},
	} {
		native := slices.IndexFunc(output.Rows, func(row Row) bool { return row.SeatID == test.seat })
		if native < 0 {
			t.Fatalf("rows = %#v, want the native seat %s's row", output.Rows, test.seat)
		}
		if row := output.Rows[native]; row.Name != test.name || row.UserName != test.name || !row.UserNameKnown {
			t.Fatalf("native seat %s row name %q user name %q (known %v), want both %q",
				test.seat, row.Name, row.UserName, row.UserNameKnown, test.name)
		}
	}
}

func userNameRollout(id string) store.Rollout {
	return store.Rollout{
		ID:          id,
		SessionID:   id,
		UserThread:  true,
		Path:        "/codex/sessions/rollout-" + id + ".jsonl",
		CWD:         "/work",
		Size:        100,
		MTimeNS:     1000,
		FirstPrompt: "fp",
		PromptCount: 1,
	}
}
