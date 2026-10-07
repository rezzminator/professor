package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func applyMessage(t *testing.T, model Model, message tea.Msg) Model {
	t.Helper()
	updated, _ := model.Update(message)
	result, ok := updated.(Model)
	if !ok {
		t.Fatalf("Update returned %T", updated)
	}
	return result
}

// The body is served from memory only for a sweep tick inside the clock grain
// it was drawn at. The sentinel plants a body no render could produce, so a
// frame that still shows it was not laid out again; one that does not was.
func TestChatsBodyIsRememberedOnlyAcrossSweepTicksInOneClockBucket(t *testing.T) {
	const sentinel = "REMEMBERED-BODY"
	model := deckModel(160, 40)
	model.nowNS = fixtureNowNS
	_ = model.render()
	model.deck.agg.bodyText = sentinel

	within := applyMessage(t, model, sweepTickMsg{nowNS: fixtureNowNS + int64(time.Millisecond)})
	if !strings.Contains(within.render(), sentinel) {
		t.Fatal("a sweep tick inside the clock bucket drew the body again")
	}

	later := applyMessage(t, model, sweepTickMsg{nowNS: fixtureNowNS + 2*bodyFrameNS})
	if strings.Contains(later.render(), sentinel) {
		t.Fatal("a sweep tick in the next clock bucket served the old body")
	}

	model.deck.agg.bodyText = sentinel
	for name, message := range map[string]tea.Msg{
		"key":    specialKey(tea.KeyDown),
		"sky":    skyTickMsg{nowNS: fixtureNowNS + int64(time.Millisecond)},
		"resize": tea.WindowSizeMsg{Width: 120, Height: 40},
	} {
		_ = model.render()
		model.deck.agg.bodyText = sentinel
		if strings.Contains(applyMessage(t, model, message).render(), sentinel) {
			t.Fatalf("a %s message was served the remembered body", name)
		}
	}
}

// A remembered body is never different from the one a model without the memo
// would draw, whatever sequence of messages got it there.
func TestRememberedBodyMatchesAFreshLayoutAcrossAMessageSequence(t *testing.T) {
	model := deckModel(160, 40)
	model.nowNS = fixtureNowNS
	messages := []tea.Msg{
		sweepTickMsg{nowNS: fixtureNowNS + int64(33*time.Millisecond)},
		specialKey(tea.KeyDown),
		sweepTickMsg{nowNS: fixtureNowNS + int64(66*time.Millisecond)},
		sweepTickMsg{nowNS: fixtureNowNS + int64(99*time.Millisecond)},
		specialKey(tea.KeyDown),
		skyTickMsg{nowNS: fixtureNowNS + int64(250*time.Millisecond)},
		sweepTickMsg{nowNS: fixtureNowNS + int64(280*time.Millisecond)},
		tea.WindowSizeMsg{Width: 120, Height: 36},
		sweepTickMsg{nowNS: fixtureNowNS + int64(310*time.Millisecond)},
		printableKey('b'),
		sweepTickMsg{nowNS: fixtureNowNS + int64(340*time.Millisecond)},
	}
	for step, message := range messages {
		model = applyMessage(t, model, message)
		fresh := model
		fresh.deck.agg = nil
		if got, want := model.render(), fresh.render(); got != want {
			t.Fatalf("step %d (%T): the remembered frame differs from a fresh layout", step, message)
		}
	}
}
