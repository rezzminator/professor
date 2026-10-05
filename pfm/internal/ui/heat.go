package ui

import (
	"math"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

// Recency is the picker's first-class dimension: what a fleet operator asks on
// opening it is "what was I just in, and what is still warm". Every surface
// that shows time — the row's colour, the heat ruler, the tempo axis — reads it
// through heatOf, so they cannot disagree about how old a chat is.
const (
	// heatHot is the age at which a chat still reads as active right now.
	heatHot = 30 * time.Second
	// heatCold is the age at which it has cooled all the way down.
	heatCold = 30 * 24 * time.Hour
	// heatRulerCells is the width of the per-row thermometer.
	heatRulerCells = 8
)

var heatSpan = math.Log(float64(heatCold) / float64(heatHot))

// heatOf maps an age onto 1 (active just now) .. 0 (a month or older) on a log
// scale, so the minutes-to-hours range that matters while working takes as much
// of the ramp as the days-to-weeks range that matters when looking back.
func heatOf(ageNS int64) float64 {
	if ageNS <= int64(heatHot) {
		return 1
	}
	if ageNS >= int64(heatCold) {
		return 0
	}
	return 1 - math.Log(float64(ageNS)/float64(heatHot))/heatSpan
}

// rowAgeNS is how long ago the row was last active: the one rule formatAge,
// the heat ruler and the tempo axis share.
func rowAgeNS(row compose.Row, nowNS int64) int64 {
	age := row.AgeNS
	if nowNS > 0 && row.ActivityNS > 0 {
		age = nowNS - row.ActivityNS
	}
	return max(age, 0)
}

// hasRecency reports whether a row is a real chat with an activity time — the
// new-chat and update rows carry none and sit outside the timeline.
func hasRecency(row compose.Row) bool {
	switch row.Kind {
	case compose.NewClaude, compose.NewCodex, compose.NewOpenCode,
		compose.ProfessorUpdate, compose.ProfessorUpdateFailed:
		return false
	default:
		return row.ActivityNS > 0
	}
}

// heatColumn is where a heat lands on an axis n cells wide: stale at the left
// edge, fresh at the right.
func heatColumn(heat float64, cells int) int {
	if cells <= 1 {
		return 0
	}
	return min(cells-1, max(0, int(math.Round(heat*float64(cells-1)))))
}

// heatShade is the colour a row of this heat is drawn in: hot at 1, cold at 0.
func heatShade(hot, cold string, heat float64) string {
	return blendHex(cold, hot, heat)
}

// heatRuler draws recency as a thermometer heatRulerCells wide: a fresh chat
// fills it end to end, ramping from cold to hot; a stale one keeps a cold stub.
// Scanning a column of them reads the fleet's temperature before any number.
// Geometric Shapes on purpose — the banned glyph ranges are guarded.
func heatRuler(heat float64, hot, cold, unlit, bg string) string {
	lit := 0
	if heat > 0 {
		lit = min(heatRulerCells, max(1, int(math.Ceil(heat*heatRulerCells))))
	}
	var ruler strings.Builder
	ruler.Grow(heatRulerCells * 48)
	for index := range heatRulerCells {
		if index < lit {
			step := float64(index+1) / heatRulerCells
			tone{fg: blendHex(cold, hot, step*heat), bg: bg}.renderTo(&ruler, "▰")
			continue
		}
		tone{fg: unlit, bg: bg}.renderTo(&ruler, "▱")
	}
	return ruler.String()
}
