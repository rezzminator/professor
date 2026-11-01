package usagehook

import (
	"fmt"
	"strings"
	"time"
)

// planUtilization is a main window's current reading, 0 once its reset has
// passed, and -1 when the answer did not report the window at all.
func planUtilization(window usageWindow, now time.Time) int {
	if window.Utilization == nil {
		return -1
	}
	return currentUtilization(window, now, 0)
}

// criticalGuidance names the window that actually crossed the critical threshold, and tells
// the reader only what is true of THAT window.
//
// The severity is max() across every window, which is right — any one of them hitting its cap
// stops work. The old copy was not: it asserted "This window is nearly exhausted: finish the
// in-flight step, then /reload" no matter which window tripped. A model-scoped 7-day cap
// (opus, fable) at 95% therefore printed a session-wide stop order while the live 5-hour
// window sat at 1% used — an alarm that is false about the thing it is read as describing, on
// every single prompt. Read literally, it truncates work that had ~99% of its budget left; read
// often enough, it trains the reader to ignore the banner that will one day be true.
//
// So: say which window, and say what is spendable. A model-scoped cap names the model and the
// still-open 5-hour headroom; the 5-hour window keeps the original stop-and-reload advice.
func criticalGuidance(five, seven, opus, fable, critical int) string {
	if five >= critical {
		return "The 5-hour window is nearly exhausted: finish the in-flight step, then /reload to another account (or pause until the reset)."
	}
	if seven >= critical && five < 0 {
		return "The 7-day account cap is nearly reached: /reload to another account."
	}
	if seven >= critical {
		return fmt.Sprintf(
			"The 7-day account cap is nearly reached (the 5-hour window is only %d%% used, but the weekly cap binds first): /reload to another account.",
			five,
		)
	}
	scoped := make([]string, 0, 2)
	if opus >= critical {
		scoped = append(scoped, "opus")
	}
	if fable >= critical {
		scoped = append(scoped, "fable")
	}
	if len(scoped) > 0 && five < 0 {
		return fmt.Sprintf(
			"Only the 7-day %s cap is nearly reached — every other model on this account is unaffected. "+
				"Keep working; route %s-tier spawns elsewhere or /reload for those.",
			strings.Join(scoped, " and "),
			strings.Join(scoped, "/"),
		)
	}
	if len(scoped) > 0 {
		return fmt.Sprintf(
			"Only the 7-day %s cap is nearly reached — the 5-hour window is %d%% used and every other model on this account is unaffected. Keep working; route %s-tier spawns elsewhere or /reload for those.",
			strings.Join(scoped, " and "),
			five,
			strings.Join(scoped, "/"),
		)
	}
	return "One window is nearly exhausted: finish the in-flight step, then /reload to another account (or pause until the reset)."
}
