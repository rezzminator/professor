package statusline

// The main statusline's 256-colour palette: one definition per meaning (model,
// effort, tokens, context window, elapsed time, cache health, label). Every
// segment carries its own 256-colour code; only the │ separators stay recessed.
const (
	cTokens  = "\x1b[1;97m"
	cWindow  = "\x1b[38;5;117m"
	cModel   = "\x1b[38;5;51m"
	cEffort  = "\x1b[38;5;220m"
	cElapsed = "\x1b[38;5;215m"
	cTools   = "\x1b[38;5;87m"
	cLabel   = "\x1b[97m"
	cMuted   = "\x1b[38;5;245m"
	cGood    = "\x1b[38;5;118m"
	cWarn    = "\x1b[38;5;226m"
	cBad     = "\x1b[1;38;5;203m"
)

// effortBarColors is the effort bar's colour per level, cool to hot, every one
// a constant above: low calm blue (cWindow), medium green (cGood), high yellow
// (cWarn), xhigh orange (cElapsed), max red (cBad). The level word keeps the
// effort colour (cEffort), so the bar alone says how hard the model is steered.
var effortBarColors = map[string]string{
	"low": cWindow, "medium": cGood, "high": cWarn, "xhigh": cElapsed, "max": cBad,
}
