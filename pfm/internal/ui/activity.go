package ui

import (
	"sync/atomic"
	"time"
)

// ActivityClock is the picker's presence signal: the moment a human last
// touched the keyboard. The background fleet refresh reads it to choose its
// own cadence, so a picker nobody is driving stops paying for scans nobody
// reads.
//
// It exists because the refresh loop had no way to ask the question at all. A
// full pass costs roughly 2.6 CPU-seconds here — one tmux fork+exec per live
// socket plus a whole store read — and it fired every 4 seconds for the life
// of the process. An abandoned picker in a detached pane therefore held ~50%
// of a core indefinitely while rendering to nobody.
//
// The zero value is not usable; the nil pointer deliberately is. Every
// non-interactive caller (plain, tsv, the one-shot commands, the existing
// stream tests) passes nil and reads as permanently active, which is the
// pre-fix cadence and the safe direction to fail: a missing presence signal
// slows nothing down, it only declines to speed anything up.
type ActivityClock struct {
	lastNS atomic.Int64
}

// NewActivityClock starts a clock already stamped: typing `pfm ls` IS the
// first interaction, so the picker opens at full cadence rather than climbing
// out of a backoff it never earned.
func NewActivityClock(now time.Time) *ActivityClock {
	clock := &ActivityClock{}
	clock.Stamp(now)
	return clock
}

// Stamp records an interaction. Safe on a nil clock.
func (clock *ActivityClock) Stamp(now time.Time) {
	if clock == nil {
		return
	}
	clock.lastNS.Store(now.UnixNano())
}

// StampNS returns the raw stamp of the last interaction, or 0 for a nil or
// never-stamped clock. The refresh stream compares it for CHANGE rather than
// reading an elapsed duration: equality needs no wall clock, so a machine
// whose time steps backwards cannot fake an interaction or kill one.
func (clock *ActivityClock) StampNS() int64 {
	if clock == nil {
		return 0
	}
	return clock.lastNS.Load()
}

// tickCadence is the idle backoff every periodic loop in the picker shares:
// it starts at base, stretches by growth after every pass nobody interrupted
// (capped at max), and snaps back to base the moment the activity clock
// records a new interaction. cmd/pfm's refreshCadence is the identical
// arithmetic for the background fleet scan — that loop lives in package main
// because the scan does, so it carries its own copy, but the shape is
// deliberately the same idle-goes-quiet law.
//
// It was added because the sky/cosmos header widget ticked on a flat 8fps
// timer forever, activity clock or not: an abandoned `pfm ls` picker (VS
// Code's tab-revival storm, 2026-09-03) rendered a full frame eight times a
// second for as long as the pane sat open, independent of how long ago
// anyone last touched it.
type tickCadence struct {
	activity  *ActivityClock
	base      time.Duration
	growth    float64
	max       time.Duration
	lastStamp int64
	interval  time.Duration
}

const defaultTickCadenceGrowth = 1.35

// newTickCadence starts a cadence already at base, matching
// NewActivityClock's "first frame is not a backoff climb" rule.
func newTickCadence(activity *ActivityClock, base, maximum time.Duration) tickCadence {
	cadence := tickCadence{
		activity: activity,
		base:     base,
		growth:   defaultTickCadenceGrowth,
		max:      maximum,
		interval: base,
	}
	if activity != nil {
		cadence.lastStamp = activity.StampNS()
	}
	return cadence
}

// next reports how long to wait before the next tick and advances the
// cadence's internal state. A nil clock never backs off, matching
// refreshCadence.next: an absent presence signal is a claim about US, not
// about the user, and must never be spent as evidence nobody is there.
func (cadence *tickCadence) next() time.Duration {
	if cadence.activity == nil {
		return cadence.base
	}
	if stamp := cadence.activity.StampNS(); stamp != cadence.lastStamp {
		cadence.lastStamp = stamp
		cadence.interval = cadence.base
		return cadence.interval
	}
	grown := time.Duration(float64(cadence.interval) * cadence.growth)
	if grown > cadence.max {
		grown = cadence.max
	}
	cadence.interval = grown
	return cadence.interval
}
