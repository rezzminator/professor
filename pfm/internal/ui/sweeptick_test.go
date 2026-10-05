package ui

import (
	"math"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestStartSweepArmsOneChainPerBurstOfKeys(t *testing.T) {
	model := deckModel(120, 30)
	model.activity = NewActivityClock(time.Now())
	if model.startSweep() == nil {
		t.Fatal("the first key of a burst arms the fast clock")
	}
	if model.startSweep() != nil {
		t.Error("a second key while the chain runs must not start a second chain")
	}
	model.activity = nil
	model.deck.sweeping = false
	if model.startSweep() != nil {
		t.Error("a picker with no activity clock never sweeps, so it never ticks")
	}
}

// commandMessages runs a command and flattens a batch into the messages its
// members produce.
func commandMessages(command tea.Cmd) []tea.Msg {
	if command == nil {
		return nil
	}
	message := command()
	batch, ok := message.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{message}
	}
	var messages []tea.Msg
	for _, member := range batch {
		messages = append(messages, commandMessages(member)...)
	}
	return messages
}

func TestAdvanceSweepRunsUntilTheLightLeavesThenStops(t *testing.T) {
	model := deckModel(120, 30)
	model.nowNS = 0
	stamp := time.Now()
	model.activity = NewActivityClock(stamp)
	model.deck.sweeping = true
	if model.advanceSweep(stamp.Add(100*time.Millisecond).UnixNano()) == nil {
		t.Fatal("the light is still crossing 100ms after the key: the next frame is scheduled")
	}
	if !model.deck.sweeping {
		t.Fatal("the chain is still running")
	}
	if model.advanceSweep(stamp.UnixNano()+mastheadSweepNS) != nil {
		t.Error("once the sweep's time is up no further frame is scheduled")
	}
	if model.deck.sweeping {
		t.Error("the chain ended, so the next key may start a new one")
	}
	if model.nowNS < stamp.UnixNano()+mastheadSweepNS {
		t.Error("a tick advances the model clock")
	}
}

func TestSweepGlowIsContinuousBoundedAndOneSided(t *testing.T) {
	previous := 0.0
	for distance := -3 * mastheadSweepRadius; distance <= 2*mastheadSweepRadius; distance += 0.05 {
		glow := sweepGlow(distance)
		if glow < 0 || glow > 1 {
			t.Fatalf("glow(%.2f) = %.3f outside 0..1", distance, glow)
		}
		// A step of a twentieth of a cell may not move the light by more than a
		// few percent, or the sweep would read as blocks, not as a moving glow.
		if math.Abs(glow-previous) > 0.06 {
			t.Fatalf("glow jumps %.3f → %.3f at %.2f", previous, glow, distance)
		}
		previous = glow
	}
	if sweepGlow(0) < sweepGlow(mastheadSweepRadius/2) || sweepGlow(0) < sweepGlow(-mastheadSweepRadius) {
		t.Error("the head is the brightest point")
	}
	if sweepGlow(-mastheadSweepRadius) <= sweepGlow(mastheadSweepRadius/2)*0.1 {
		t.Error("the tail trails behind the head, it is not cut off with it")
	}
	if sweepGlow(mastheadSweepRadius+1) != 0 || sweepGlow(-2*mastheadSweepRadius-1) != 0 {
		t.Error("no light beyond the head's or the tail's reach")
	}
}

func TestSweepCentreCrossesTheWholeLine(t *testing.T) {
	model := deckModel(120, 30)
	model.nowNS = fixtureNowNS
	model.activity = NewActivityClock(time.Unix(0, fixtureNowNS-1))
	start, on := model.sweepCentre(100)
	if !on || start > -mastheadSweepRadius+1 {
		t.Fatalf("the light starts off the left edge, got %.1f (on=%v)", start, on)
	}
	model.activity = NewActivityClock(time.Unix(0, fixtureNowNS-mastheadSweepNS+int64(time.Millisecond)))
	end, on := model.sweepCentre(100)
	if !on || end < 100+2*mastheadSweepRadius {
		t.Fatalf("the light leaves past the right edge with its tail, got %.1f (on=%v)", end, on)
	}
}
