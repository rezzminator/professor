package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// sweepTickInterval is the fast clock the masthead's light rides while it
// crosses the screen: ~30 frames a second, so a sweep that lasts
// mastheadSweepNS moves a few cells a frame instead of leaping a dozen at the
// ambient tick's 8 fps. It runs only for those 1.3 seconds after a key; the
// ambient sky tick keeps its own slower cadence and its own idle backoff.
const sweepTickInterval = 33 * time.Millisecond

type sweepTickMsg struct{ nowNS int64 }

func sweepTickCmd() tea.Cmd {
	return tea.Tick(sweepTickInterval, func(now time.Time) tea.Msg {
		return sweepTickMsg{nowNS: now.UnixNano()}
	})
}

// startSweep arms the fast clock for the sweep a key just started. One chain
// serves every key of a burst: a key that lands while the chain runs only
// restarts the light, which the activity stamp already does. A picker with no
// activity clock (plain, tests) never sweeps, so it never ticks.
func (model *Model) startSweep() tea.Cmd {
	if model.activity == nil || model.deck.sweeping {
		return nil
	}
	model.deck.sweeping = true
	return sweepTickCmd()
}

// advanceSweep takes one fast tick: it moves the clock and either schedules the
// next frame or, once the light has left the screen, ends the chain.
func (model *Model) advanceSweep(nowNS int64) tea.Cmd {
	model.nowNS = max(model.nowNS, nowNS)
	if model.activity == nil || model.nowNS-model.activity.StampNS() >= mastheadSweepNS {
		model.deck.sweeping = false
		return nil
	}
	return sweepTickCmd()
}
