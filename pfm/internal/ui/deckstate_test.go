package ui

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

func newArrival() compose.Row {
	return compose.Row{
		Kind: compose.LiveClaude, ID: "ffffffff-0000-4000-8000-ffffffffffff", Socket: "cc-new", Name: "just landed",
		Project: "atlas", ActivityNS: fixtureNowNS - int64(time.Second),
	}
}

func TestNoteArrivalsStampsOnlyChatsThatAppearedWithRecency(t *testing.T) {
	before := deckFleet(120, 30).Rows
	arrival := newArrival()
	notice := compose.Row{Kind: compose.NewCodex, Name: "New Codex chat", Project: "~"}
	after := append(append([]compose.Row{}, before...), arrival, notice)
	var state deckState
	if !state.noteArrivals(before, after, fixtureNowNS) {
		t.Fatal("a new live chat is an arrival")
	}
	if got := state.glow(arrival, fixtureNowNS); got != 1 {
		t.Errorf("a chat flares fully the moment it lands, got %v", got)
	}
	if got := state.glow(notice, fixtureNowNS); got != 0 {
		t.Errorf("an action row never flares, got %v", got)
	}
	if got := state.glow(before[2], fixtureNowNS); got != 0 {
		t.Errorf("a chat that was already there never flares, got %v", got)
	}
	var quiet deckState
	if quiet.noteArrivals(before, before, fixtureNowNS) || len(quiet.arrivals) != 0 {
		t.Error("an unchanged fleet has no arrivals")
	}
}

func TestArrivalGlowFadesToNothing(t *testing.T) {
	var state deckState
	arrival := newArrival()
	state.noteArrivals(nil, []compose.Row{arrival}, fixtureNowNS)
	half := state.glow(arrival, fixtureNowNS+arrivalGlowNS/2)
	if half < 0.49 || half > 0.51 {
		t.Errorf("half the glow time leaves half the glow, got %v", half)
	}
	if got := state.glow(arrival, fixtureNowNS+arrivalGlowNS); got != 0 {
		t.Errorf("the glow is gone after %v, got %v", time.Duration(arrivalGlowNS), got)
	}
	if got := state.glow(arrival, fixtureNowNS-1); got != 0 {
		t.Errorf("a clock that steps back never brightens a row, got %v", got)
	}
}

func TestRefreshFlaresArrivalsAndWakesTheParkedSky(t *testing.T) {
	model := deckModel(120, 30)
	model.nowNS = fixtureNowNS
	model.skyParked = true
	refreshed := deckFleet(120, 30)
	refreshed.Rows = append(refreshed.Rows, newArrival())
	updated, command := model.Update(RefreshMsg{Snapshot: refreshed})
	model = updated.(Model)
	if model.deck.glow(newArrival(), model.nowNS) == 0 {
		t.Error("a chat that arrives in a refresh flares")
	}
	if command == nil || model.skyParked {
		t.Error("an arrival wakes the parked sky so the flare is seen fading")
	}
}

func TestRefreshWithNoSkyKeepsNoArrivalState(t *testing.T) {
	snapshot := deckFleet(120, 30)
	snapshot.NoSky = true
	model := NewModel(snapshot)
	refreshed := deckFleet(120, 30)
	refreshed.NoSky = true
	refreshed.Rows = append(refreshed.Rows, newArrival())
	updated, _ := model.Update(RefreshMsg{Snapshot: refreshed})
	if got := updated.(Model).deck.arrivals; len(got) != 0 {
		t.Errorf("with the sky off nothing animates, so nothing is stamped, got %v", got)
	}
}

func TestReceiptIsRetiredByTheNextKeystroke(t *testing.T) {
	model := selectChat(t, deckModel(140, 30), "P:AUDIT")
	model, _ = applyKey(t, model, controlKey('x'))
	if model.killStatus == "" {
		t.Fatal("⌃X leaves a receipt")
	}
	model, _ = applyKey(t, model, specialKey(tea.KeyDown))
	if model.killStatus != "" {
		t.Errorf("the next keystroke retires the receipt, got %q", model.killStatus)
	}
}

func TestFleetPassesAreMemoisedUntilAMessageCouldChangeThem(t *testing.T) {
	model := deckModel(140, 30)
	model.nowNS = fixtureNowNS
	first := model.projectTallies()
	if again := model.projectTallies(); reflect.ValueOf(again).Pointer() != reflect.ValueOf(first).Pointer() {
		t.Error("an unchanged fleet serves the same census, not a new pass")
	}
	if bins := model.tempoBins(60); len(bins) != 60 || &model.tempoBins(60)[0] != &bins[0] {
		t.Error("an unchanged fleet and clock serve the same bins")
	}
	later := model
	later.nowNS += int64(time.Hour)
	if &later.tempoBins(60)[0] == &model.tempoBins(60)[0] {
		t.Error("a new clock reading re-drops every chat on the axis")
	}

	for _, runeValue := range "quartz" {
		model, _ = applyKey(t, model, printableKey(runeValue))
	}
	if got := model.projectTallies(); reflect.ValueOf(got).Pointer() == reflect.ValueOf(first).Pointer() {
		t.Error("a keystroke can change the filter, so the census is counted again")
	}
	if _, ok := model.projectTallies()["lumen"]; ok {
		t.Error("the filter 'quartz' leaves no lumen chat; a stale census would still list it")
	}
}

func TestFleetPassesFollowARefresh(t *testing.T) {
	model := deckModel(140, 30)
	before := model.fleetVitals().live[pfmengine.Codex]
	refreshed := deckFleet(140, 30)
	refreshed.Rows = append(refreshed.Rows, compose.Row{
		Kind: compose.LiveCodex, ID: "eeeeeeee-0000-4000-8000-eeeeeeeeeeee", Socket: "cx-new", Name: "new codex",
		Project: "fresh", ActivityNS: fixtureNowNS - int64(time.Second),
	})
	updated, _ := model.Update(RefreshMsg{Snapshot: refreshed})
	model = updated.(Model)
	if got := model.fleetVitals().live[pfmengine.Codex]; got != before+1 {
		t.Errorf("a refresh with one more Codex chat moves the vitals %d → %d", before, got)
	}
	if got := model.projectTallies()["fresh"]; got.live != 1 {
		t.Errorf("a refresh that opens a project is counted, got %+v", got)
	}
}

func TestFleetPassesWorkOnAModelWithNoCache(t *testing.T) {
	model := deckModel(140, 30)
	model.deck.agg = nil
	if got := model.projectTallies()["atlas"]; got.live != 3 {
		t.Errorf("an uncached census still counts, got %+v", got)
	}
	if got := model.fleetVitals().live[pfmengine.Claude]; got != 3 {
		t.Errorf("uncached vitals still count, got %d", got)
	}
	if len(model.tempoBins(40)) != 40 {
		t.Error("uncached bins still span the axis")
	}
}

func TestKeyAfterALongIdleMarksTheHeaderRefreshingUntilTheSnapshotLands(t *testing.T) {
	model := deckModel(120, 30)
	model.activity = NewActivityClock(time.Now())
	model.deck.freshNS = time.Now().Add(-2 * RefreshStaleAfter).UnixNano()
	if strings.Contains(ansi.Strip(model.chatsHeaderLine(100)), "refreshing") {
		t.Fatal("an idle picker with no key yet claims to be refreshing")
	}
	model, _ = applyKey(t, model, specialKey(tea.KeyDown))
	if !strings.Contains(ansi.Strip(model.chatsHeaderLine(100)), "refreshing") {
		t.Error("a key on a stale fleet says the wake is under way")
	}
	model.applyRefresh(deckFleet(120, 30))
	if strings.Contains(ansi.Strip(model.chatsHeaderLine(100)), "refreshing") {
		t.Error("the landed snapshot ends the marker")
	}
	fresh := deckModel(120, 30)
	fresh.activity = NewActivityClock(time.Now())
	fresh, _ = applyKey(t, fresh, specialKey(tea.KeyDown))
	if strings.Contains(ansi.Strip(fresh.chatsHeaderLine(100)), "refreshing") {
		t.Error("a key on a fleet refreshed moments ago must not cry refreshing")
	}
}

// TestRefreshFailureMarksTheFleetStaleUntilAPassLands pins the visible half of
// "an error never renders as absence": a refresh pass that failed leaves the
// previous rows on screen, and those rows must read as old — the stale marker
// names the age of the fleet on screen and the error — until a pass lands.
func TestRefreshFailureMarksTheFleetStaleUntilAPassLands(t *testing.T) {
	model := deckModel(120, 30)
	rows := len(model.rows)
	if strings.Contains(ansi.Strip(model.mastheadTitle(160)), "stale") {
		t.Fatal("a fleet that has not failed claims to be stale")
	}
	model.nowNS = model.deck.freshNS + int64(42*time.Second)
	model.applyRefresh(Snapshot{RefreshFailure: &RefreshFailure{
		Stage: "gather", Err: "tmux could not run:\n\x1b[2Jno server", AtNS: model.nowNS,
	}})
	raw := model.mastheadTitle(200)
	title := ansi.Strip(raw)
	for _, want := range []string{"fleet stale 42s", "gather", "tmux could not run"} {
		if !strings.Contains(title, want) {
			t.Errorf("the stale-fleet marker lacks %q: %q", want, title)
		}
	}
	// The error is raw err.Error(), tmux stderr among it: a newline or an
	// escape byte in it must not reach the frame.
	if strings.Contains(raw, "\n") || strings.Contains(raw, "\x1b[2J") {
		t.Errorf("the stale-fleet marker carries the error's control bytes: %q", raw)
	}
	// The fill cuts from the right: on a narrow terminal the marker must still
	// be what shows, ahead of the fleet's counts.
	if narrow := ansi.Strip(model.mastheadTitle(30)); !strings.Contains(narrow, "fleet stale 42s") {
		t.Errorf("a 30-column masthead lost the stale-fleet marker: %q", narrow)
	}
	if len(model.rows) != rows {
		t.Errorf("a failed pass carries no fleet: rows = %d, want the last good %d", len(model.rows), rows)
	}
	model.applyRefresh(deckFleet(120, 30))
	if got := ansi.Strip(model.mastheadTitle(200)); strings.Contains(got, "stale") {
		t.Errorf("the next good pass must clear the stale marker: %q", got)
	}
}

// TestRefreshRebuildsTheCosmosFromTheRowsTheListShows pins the cosmos' last
// door: a snapshot's graph is built from its own rows, but the model filters a
// deactivated socket out of them and lets a typed kill override a row. The sky
// must follow the rows the Chats tab lists, never the snapshot's unfiltered
// ones, or a chat the list just stopped showing keeps shining.
func TestRefreshRebuildsTheCosmosFromTheRowsTheListShows(t *testing.T) {
	nowNS := fixtureNowNS
	alpha := compose.Row{Kind: compose.LiveClaude, ID: "alpha-id", Name: "alpha", Socket: "cc-alpha", PaneID: "%1"}
	beta := compose.Row{Kind: compose.LiveClaude, ID: "beta-id", Name: "beta", Socket: "cc-beta", PaneID: "%2"}
	events := []fleetdb.CommsEvent{
		{
			AtNS:       nowNS - int64(time.Minute),
			Kind:       fleetdb.KindInject,
			SenderUUID: "alpha-id",
			Target:     "beta-id",
			Message:    "hi",
		},
		// No sender identity: BuildCosmos warns once for it.
		{AtNS: nowNS - int64(time.Minute), Kind: fleetdb.KindInject, Target: "alpha-id", Message: "anon"},
	}
	rows := []compose.Row{alpha, beta}
	snapshot := func() Snapshot {
		return Snapshot{
			Rows: rows, NowNS: nowNS, Width: 120, Height: 30,
			Cosmos:       compose.BuildCosmos(rows, events, nowNS, true),
			CosmosEvents: events,
		}
	}
	keys := func(model Model) []string {
		out := make([]string, 0, len(model.cosmos.Nodes))
		for _, node := range model.cosmos.Nodes {
			out = append(out, node.Key)
		}
		return out
	}

	model := NewModel(snapshot())
	model.applyRefresh(snapshot())
	if got := keys(model); len(got) != 2 {
		t.Fatalf("both live chats are stars before anything is filtered, got %v", got)
	}
	model.deactivatedSockets["cc-beta"] = true
	model.applyRefresh(snapshot())
	if got := keys(model); len(got) != 1 || got[0] != "chat:id:alpha-id" {
		t.Errorf("a deactivated chat is out of the list and out of the sky, got %v", got)
	}
	if want := snapshot().Cosmos.Warnings; len(want) != 1 || !slices.Equal(model.cosmos.Warnings, want) {
		t.Errorf("the rebuilt sky's warnings = %q, want the snapshot's %q once each", model.cosmos.Warnings, want)
	}

	killed := NewModel(snapshot())
	killed.killChanges = map[string]KillChange{"beta-id": {ID: "beta-id", Killed: true}}
	killed.applyRefresh(snapshot())
	if got := keys(killed); len(got) != 1 || got[0] != "chat:id:alpha-id" {
		t.Errorf("a chat the picker just killed is not a live star, got %v", got)
	}
}
