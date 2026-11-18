package statusline

import (
	"strconv"
)

// spendForm is how much of a spend the line's width leaves, widest first; the
// professor plugin's agent row gives up its own spend in the same forms.
type spendForm int

const (
	// spendWords is "Σ $4.68 · 6.5M in · 31.0K out · 46 tools".
	spendWords spendForm = iota
	// spendSlash is "Σ $4.68/6.5M/31.0K/46".
	spendSlash
	// spendUSD is "Σ $4.68".
	spendUSD
)

// spendText is a spend as the main line writes it, in parts, so a narrow
// terminal takes a shorter form of the same facts: one dollar amount, a floor
// carrying "+?" on the dollars, prompt tokens, output tokens and tool calls,
// each "?" when it could not be read; then the one flag (spendMarker) and the
// gateway's API-equivalent note, which every form keeps.
type spendText struct {
	usd         float64
	usdKnown    bool
	floor       bool
	in, out     int64
	countsKnown bool
	tools       int
	toolsKnown  bool
	marker      string
	gateway     gatewaySpend
}

// usdText is a dollar amount as the line writes it, "$4.68".
func usdText(usd float64) string {
	return "$" + strconv.FormatFloat(usd, 'f', 2, 64)
}

// usdWarnFrom and usdBadFrom are the dollar amounts usdTone turns at; the
// agents door hands them to the professor plugin's row (agentsLook).
const (
	usdWarnFrom = 2
	usdBadFrom  = 10
)

// usdTone is the colour of a dollar amount by its size: plain below
// usdWarnFrom, a warning from it, bad from usdBadFrom.
func usdTone(usd float64) string {
	switch {
	case usd >= usdBadFrom:
		return cBad
	case usd >= usdWarnFrom:
		return cWarn
	}
	return cLabel
}

// text is the spend in one form.
func (spend spendText) text(form spendForm) string {
	out := cMuted + "Σ" + reset + " " + cWarn + "$?" + reset
	if spend.usdKnown {
		out = cMuted + "Σ" + reset + " " + usdTone(spend.usd) + usdText(spend.usd) + reset
	}
	if spend.floor {
		out += cWarn + "+?" + reset
	}
	unknown := cWarn + "?" + reset
	in, produced, tools := unknown, unknown, unknown
	if spend.countsKnown {
		in = cTokens + formatContextTokens(spend.in) + reset
		produced = cTokens + formatContextTokens(spend.out) + reset
	}
	word := "tools"
	if spend.toolsKnown {
		tools = cTools + strconv.Itoa(spend.tools) + reset
		if spend.tools == 1 {
			word = "tool"
		}
	}
	switch form {
	case spendWords:
		dot := " " + cMuted + "·" + reset + " "
		out += dot + in + " " + cMuted + "in" + reset + dot + produced + " " + cMuted + "out" + reset +
			dot + tools + " " + cMuted + word + reset
	case spendSlash:
		slash := cMuted + "/" + reset
		out += slash + in + slash + produced + slash + tools
	case spendUSD:
	}
	if spend.marker != "" {
		out += cWarn + " " + spend.marker + reset
	}
	return out + spend.gateway.note()
}
