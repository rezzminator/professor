package ui

import (
	"reflect"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
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
