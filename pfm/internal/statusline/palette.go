package statusline

// The 256-colour palette shared by the sub-agent rows and the main statusline:
// one definition, so a part that means the same thing on both (model, effort,
// tokens, context window, elapsed time, cache health, label) wears the same
// colour on both.
//
// Claude Code draws the row body in its muted theme colour at faint weight,
// so every segment carries its own bright 256-colour code and the row opens by
// cancelling the faint (SGR 22); only the │ separators stay recessed.
const (
	rowOpen     = "\x1b[22m"
	cTokens     = "\x1b[1;97m"
	cWindow     = "\x1b[38;5;117m"
	cName       = "\x1b[1;97m"
	cRole       = "\x1b[38;5;213m"
	cModel      = "\x1b[38;5;51m"
	cEffort     = "\x1b[38;5;220m"
	cRunning    = "\x1b[38;5;118m"
	cCompleted  = "\x1b[38;5;117m"
	cFailed     = "\x1b[1;38;5;203m"
	cElapsed    = "\x1b[38;5;215m"
	cTools      = "\x1b[38;5;87m"
	cCompaction = "\x1b[38;5;177m"
	cDelegating = "\x1b[38;5;171m"
	cCwd        = "\x1b[38;5;156m"
	cLabel      = "\x1b[97m"
	cMuted      = "\x1b[38;5;245m"
	cGood       = "\x1b[38;5;118m"
	cWarn       = "\x1b[38;5;226m"
	cBad        = "\x1b[1;38;5;203m"
)
