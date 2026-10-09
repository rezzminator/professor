package costledger

import (
	"testing"
	"time"
)

func costRow(id, ts string, cost int64) Row {
	return Row{RequestID: id, TS: ts, SessionID: sessionA, CostUSDMicros: micros(cost), Join: JoinLogSpan}
}

func at(text string) time.Time {
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		panic(err)
	}
	return parsed
}

// The before-ledger amount is read from the transcript's cost-state lines, never guessed from a
// live total: a cost-state is Claude Code's exact running total at one exit, so the ledger rows of
// the launches up to that exit (every row before the next launch's first line), taken from it,
// leave what the ledger never saw.
func TestBeforeLedgerIsWhatTheCostStatesHoldBeyondTheRowsUpToTheirExit(t *testing.T) {
	cases := []struct {
		name   string
		rows   []Row
		states []CostState
		want   int64
	}{
		{
			name: "no cost-state: nothing is known to precede the ledger",
			rows: []Row{costRow("a", "2026-10-09T18:00:00.000Z", 5_000_000)},
		},
		{
			name: "a session first seen by the ledger after its exit",
			rows: []Row{costRow("a", "2026-10-09T20:51:17.218Z", 1_440_000)},
			states: []CostState{
				{USD: 75.2011, At: at("2026-10-09T20:51:03.408Z"), Until: at("2026-10-09T20:51:10.000Z")},
			},
			want: 75_201_100,
		},
		{
			name: "a healthy resume: the launch before was fully ledgered",
			rows: []Row{
				costRow("a", "2026-10-09T18:00:00.000Z", 17_580),
				costRow("b", "2026-10-09T18:05:00.000Z", 18_190),
			},
			states: []CostState{
				{USD: 0.0175803, At: at("2026-10-09T18:00:30.000Z"), Until: at("2026-10-09T18:04:00.000Z")},
			},
			want: 0,
		},
		{
			name: "a launch that ran with no sink, between two that logged",
			rows: []Row{
				costRow(
					"a",
					"2026-10-09T17:00:00.000Z",
					10_000_000,
				),
				costRow("b", "2026-10-09T19:00:00.000Z", 3_000_000),
			},
			states: []CostState{
				{USD: 10, At: at("2026-10-09T17:30:00.000Z"), Until: at("2026-10-09T18:00:00.000Z")},
				{USD: 15, At: at("2026-10-09T18:30:00.000Z"), Until: at("2026-10-09T18:59:00.000Z")},
			},
			want: 5_000_000,
		},
		{
			name: "a reload: the last call's row trails its line, the next launch began within the grace",
			rows: []Row{costRow("a", "2026-10-09T18:00:00.250Z", 2_000_000)},
			states: []CostState{
				{USD: 2, At: at("2026-10-09T18:00:00.000Z"), Until: at("2026-10-09T18:00:01.000Z")},
			},
			want: 0,
		},
		{
			name: "a row of the next launch, later than the grace, is not part of the earlier exit",
			rows: []Row{costRow("a", "2026-10-09T18:00:05.000Z", 2_000_000)},
			states: []CostState{
				{USD: 2, At: at("2026-10-09T18:00:00.000Z"), Until: at("2026-10-09T18:00:03.000Z")},
			},
			want: 2_000_000,
		},
		{
			name: "a background agent's rows after the main thread's last line belong to the launch they ran in",
			rows: []Row{
				costRow("a", "2026-10-09T18:00:00.000Z", 1_000_000),
				costRow("b", "2026-10-09T18:04:00.000Z", 1_000_000),
			},
			states: []CostState{
				{USD: 2, At: at("2026-10-09T18:00:00.100Z"), Until: at("2026-10-09T18:10:00.000Z")},
			},
			want: 0,
		},
		{
			name:   "a closed chat: every row precedes its last exit",
			rows:   []Row{costRow("a", "2026-10-09T18:04:00.000Z", 2_000_000)},
			states: []CostState{{USD: 2, At: at("2026-10-09T18:00:00.000Z")}},
			want:   0,
		},
		{
			name: "a crash after the ledger began, then a resume to the earlier total: the earlier cost-state still names it",
			rows: []Row{
				costRow("crashed", "2026-10-09T11:30:00.000Z", 5_000_000),
				costRow("resumed", "2026-10-09T12:10:00.000Z", 3_000_000),
			},
			states: []CostState{
				{USD: 75, At: at("2026-10-09T10:00:00.000Z"), Until: at("2026-10-09T11:00:00.000Z")},
				{USD: 78, At: at("2026-10-09T12:30:00.000Z")},
			},
			want: 75_000_000,
		},
		{
			name:   "a ledger above the cost-state leaves no negative before",
			rows:   []Row{costRow("a", "2026-10-09T18:00:00.000Z", 9_000_000)},
			states: []CostState{{USD: 5, At: at("2026-10-09T18:30:00.000Z")}},
			want:   0,
		},
		{
			name:   "a cost-state with no exit time is bounded by the next launch's first line",
			rows:   []Row{costRow("a", "2026-10-09T18:00:00.000Z", 1_000_000)},
			states: []CostState{{USD: 4, Until: at("2026-10-09T17:00:00.000Z")}},
			want:   4_000_000,
		},
		{
			name: "a row whose cost never arrived counts nothing, a row with no time counts as logged",
			rows: []Row{
				{RequestID: "a", TS: "2026-10-09T18:00:00.000Z", SessionID: sessionA, Join: JoinSpan},
				costRow("b", "", 1_000_000),
			},
			states: []CostState{{USD: 3, At: at("2026-10-09T18:30:00.000Z")}},
			want:   2_000_000,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BeforeLedger(tc.rows, tc.states); got != tc.want {
				t.Fatalf("BeforeLedger = %d micro-dollars, want %d", got, tc.want)
			}
		})
	}
}

// A reader hands the collector the lines of a transcript in file order; a cost-state is placed at
// the last stamp before it and bounded by the first stamp after it, and the transcript is closed
// only while no stamp follows its last one.
func TestAnchorsPlaceEachCostStateAtTheLastStampBeforeIt(t *testing.T) {
	var anchors Anchors
	if anchors.Closed() || len(anchors.States()) != 0 {
		t.Fatalf("a collector that saw nothing holds %v closed=%v", anchors.States(), anchors.Closed())
	}
	anchors.Stamp("2026-10-09T18:00:00.000Z")
	anchors.Stamp("2026-10-09T18:22:32.744Z")
	anchors.State(11.8434, 0, 0)
	anchors.State(11.8434, 0, 0)
	if !anchors.Closed() {
		t.Fatal("no stamp follows the last cost-state, want closed")
	}
	anchors.Stamp("2026-10-09T20:50:58.724Z")
	anchors.State(75.2011, 1791567826254, 11232465)
	anchors.Stamp("2026-10-09T21:00:00.000Z")
	if anchors.Closed() {
		t.Fatal("a stamp follows the last cost-state, want open")
	}
	want := []CostState{
		{USD: 11.8434, At: at("2026-10-09T18:22:32.744Z"), Until: at("2026-10-09T20:50:58.724Z")},
		{USD: 11.8434, At: at("2026-10-09T18:22:32.744Z"), Until: at("2026-10-09T20:50:58.724Z")},
		{USD: 75.2011, At: at("2026-10-09T20:50:58.724Z"), Until: at("2026-10-09T21:00:00.000Z")},
	}
	got := anchors.States()
	if len(got) != len(want) {
		t.Fatalf("States = %v, want %v", got, want)
	}
	for index := range want {
		if got[index].USD != want[index].USD || !got[index].At.Equal(want[index].At) ||
			!got[index].Until.Equal(want[index].Until) {
			t.Fatalf("state %d = %+v, want %+v", index, got[index], want[index])
		}
	}
}

func TestAnchorsPlaceAStateWithNoStampBeforeItAtTheZeroTime(t *testing.T) {
	var anchors Anchors
	anchors.State(1, 0, 0)
	anchors.Stamp("not a time")
	anchors.State(2, 0, 0)
	if len(anchors.States()) != 2 {
		t.Fatalf("States = %v, want both cost-states", anchors.States())
	}
	for index, state := range anchors.States() {
		if !state.At.IsZero() {
			t.Fatalf("state %d At = %v, want the zero time", index, state.At)
		}
	}
}

// The exit is the later of the last stamp before the cost-state and startTime + totalDuration: a
// main thread that went quiet while background agents worked stamps nothing, and the clock fields
// still say when the launch ended.
func TestAnchorsTakeTheExitFromTheClockFieldsWhenTheStampsAreOlder(t *testing.T) {
	var anchors Anchors
	anchors.Stamp("2026-10-09T18:22:30.047Z")
	anchors.State(11.8434, 1791567826254, 2326475) // 18:22:32.729, 2.7 s past the last stamp
	anchors.Stamp("2026-10-09T19:00:00.000Z")
	anchors.State(12, 1791567826254, 1000) // 17:43:47.254: older than the stamp, the stamp stands
	got := anchors.States()
	if len(got) != 2 || !got[0].At.Equal(at("2026-10-09T18:22:32.729Z")) ||
		!got[1].At.Equal(at("2026-10-09T19:00:00.000Z")) {
		t.Fatalf("States = %v, want exits 18:22:32.729Z and 19:00:00.000Z", got)
	}
}
