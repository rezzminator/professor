package sky

import (
	lipgloss "charm.land/lipgloss/v2"

	pfmengine "hostops/pfm/internal/engine"
)

// styleClass enumerates every color role a cell can take. Each engine gets
// four brightness levels; the depth cue drops the far body one level.
type styleClass uint8

const (
	clsNone   styleClass = iota
	clsBgStar            // fixed faint background stars

	clsClaude0 // far-dim steel blue (faint)
	clsClaude1 // dim blue
	clsClaude2 // bright blue
	clsClaude3 // white-hot core

	clsCodex0 // far-dim amber (faint)
	clsCodex1 // dim amber
	clsCodex2 // bright gold
	clsCodex3 // bold gold core

	clsOpenCode0 // far-dim magenta (faint)
	clsOpenCode1 // dim magenta
	clsOpenCode2 // bright magenta
	clsOpenCode3 // bold magenta core

	clsFallback0 // far-dim fallback for a newly registered engine
	clsFallback1 // dim fallback
	clsFallback2 // bright fallback
	clsFallback3 // bold fallback core

	clsCometHead
	clsCometTail
	clsCometFade

	clsEmberHot
	clsEmberMid
	clsEmberDark

	numClasses
)

const starFallback = clsFallback0

var starBase = map[pfmengine.ID]styleClass{
	pfmengine.Claude:   clsClaude0,
	pfmengine.Codex:    clsCodex0,
	pfmengine.OpenCode: clsOpenCode0,
}

// bodyCls maps engine x brightness level (0..3, clamped) to a styleClass.
func bodyCls(id pfmengine.ID, lvl int) styleClass {
	if lvl < 0 {
		lvl = 0
	}
	if lvl > 3 {
		lvl = 3
	}
	base, ok := starBase[id]
	if !ok {
		base = starFallback
	}
	return base + styleClass(lvl)
}

// StarClass renders one cell with an engine's star style. Unknown engines use
// the explicit fallback class rather than inheriting a built-in's identity.
func StarClass(id pfmengine.ID) string {
	return styleTab[bodyCls(id, 1)].Render("·")
}

// styleTab pre-renders one lipgloss style per class. 16-color ANSI only,
// same VGA-homage discipline as the sword prototype.
var styleTab = func() [numClasses]lipgloss.Style {
	fg := func(col string) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(col))
	}
	var t [numClasses]lipgloss.Style
	t[clsNone] = lipgloss.NewStyle()
	t[clsBgStar] = fg("8").Faint(true)

	t[clsClaude0] = fg("4").Faint(true)
	t[clsClaude1] = fg("4")
	t[clsClaude2] = fg("12")
	t[clsClaude3] = fg("15").Bold(true)

	t[clsCodex0] = fg("3").Faint(true)
	t[clsCodex1] = fg("3")
	t[clsCodex2] = fg("11")
	t[clsCodex3] = fg("11").Bold(true)

	// The theme's third accent is magenta, deliberately distinct from the
	// blue and amber built-ins.
	t[clsOpenCode0] = fg("5").Faint(true)
	t[clsOpenCode1] = fg("5")
	t[clsOpenCode2] = fg("13")
	t[clsOpenCode3] = fg("13").Bold(true)

	// A fourth engine is visible without a palette edit. The neutral cyan
	// fallback is deliberately not clsBgStar: a live chat must never render as
	// the same faint dot as an empty fleet.
	t[clsFallback0] = fg("6").Faint(true)
	t[clsFallback1] = fg("6")
	t[clsFallback2] = fg("14")
	t[clsFallback3] = fg("14").Bold(true)

	t[clsCometHead] = fg("15").Bold(true)
	t[clsCometTail] = fg("7")
	t[clsCometFade] = fg("8").Faint(true)

	t[clsEmberHot] = fg("9").Bold(true)
	t[clsEmberMid] = fg("1")
	t[clsEmberDark] = fg("1").Faint(true)
	return t
}()
