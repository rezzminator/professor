package statusline

import (
	"slices"

	"github.com/rezzminator/professor/pfm/internal/modelglyph"
)

// AgentsLook is how the main status line draws what an agent row shows, so
// the professor plugin's row wears pfm's look without a copy of its own:
// Palette maps each meaning to the SGR sequence the main line draws it in,
// EffortColors each effort level to its bar's colour, Gauge the context
// gauge's cells, Pulse the pulse's scale, Context the context block's width
// table (contextForms) and Tones where an amount turns from one colour to the
// next.
type AgentsLook struct {
	Palette      map[string]string `json:"palette"`
	EffortColors map[string]string `json:"effortColors"`
	Gauge        AgentsGauge       `json:"gauge"`
	Pulse        AgentsPulse       `json:"pulse"`
	Context      []ContextForm     `json:"context"`
	Tones        AgentsTones       `json:"tones"`
}

// AgentsGauge is the context gauge (pulse.go): On a used cell, Off a free
// one, Cells its width.
type AgentsGauge struct {
	On    string `json:"on"`
	Off   string `json:"off"`
	Cells int    `json:"cells"`
}

// AgentsPulse is the pulse's scale (pulse.go): Glyphs the cell per growth
// level, none to most; Steps the fewest tokens one step must add to reach each
// level above the first; Drop the cell of a step that shed at least DropFrom
// tokens; StepMs the wall time one main-line cell covers; Cells its width.
type AgentsPulse struct {
	Glyphs   []string `json:"glyphs"`
	Drop     string   `json:"drop"`
	Steps    []int64  `json:"steps"`
	DropFrom int64    `json:"dropFrom"`
	StepMs   int64    `json:"stepMs"`
	Cells    int      `json:"cells"`
}

// AgentsTones is where the main line's amounts change colour: USD a dollar
// amount (usdTone), Share a percent of the context window (shareTone),
// CacheHit the share of a call's prompt read from the cache (cacheHitText).
type AgentsTones struct {
	USD      AgentsTone    `json:"usd"`
	Share    AgentsTone    `json:"share"`
	CacheHit AgentsHitTone `json:"cacheHit"`
}

// AgentsHitTone is a share that is better high: bad below Warn, the warning
// colour from Warn, good from Good, each inclusive.
type AgentsHitTone struct {
	Warn float64 `json:"warn"`
	Good float64 `json:"good"`
}

// AgentsTone is one amount's two turns: the warning colour from Warn, the bad
// one from Bad, each inclusive.
type AgentsTone struct {
	Warn float64 `json:"warn"`
	Bad  float64 `json:"bad"`
}

// AgentText is one agent's facts in the main line's units and symbols:
// Glyph the model family's one-cell symbol, Label its name as the main line
// writes it (modelLabel: Opus 5.5), EffortBar
// the effort's filled bar ("" for a level outside the five), Context, In and
// Out token counts as the main line writes them (12.5K), USD two decimals.
type AgentText struct {
	Glyph     string `json:"glyph"`
	Label     string `json:"label"`
	EffortBar string `json:"effortBar"`
	Context   string `json:"context"`
	In        string `json:"in"`
	Out       string `json:"out"`
	USD       string `json:"usd"`
}

// lookModel is the palette key of the model colour, named apart from the
// effort record's "model" mode it only spells alike.
const lookModel = "model"

// agentsLook is the palette as the main line holds it right now.
func agentsLook() AgentsLook {
	return AgentsLook{
		Palette: map[string]string{
			"tokens": cTokens, "window": cWindow, lookModel: cModel, "effort": cEffort, "elapsed": cElapsed,
			"tools": cTools, "label": cLabel, "muted": cMuted, "good": cGood, "warn": cWarn, "bad": cBad,
			"dim": dim, "reset": reset,
		},
		EffortColors: effortBarColors,
		Gauge:        AgentsGauge{On: gaugeOn, Off: gaugeOff, Cells: gaugeCells},
		Pulse: AgentsPulse{
			Glyphs: slices.Clone(pulseGlyphs[:]), Drop: pulseDrop, Steps: slices.Clone(pulseSteps[:]),
			DropFrom: pulseDropFrom, StepMs: pulseStep.Milliseconds(), Cells: pulseCells,
		},
		Context: slices.Clone(contextForms[:]),
		Tones: AgentsTones{
			USD:      AgentsTone{Warn: usdWarnFrom, Bad: usdBadFrom},
			Share:    AgentsTone{Warn: shareWarnFrom, Bad: shareBadFrom},
			CacheHit: AgentsHitTone{Warn: cacheHitWarnFrom, Good: cacheHitGoodFrom},
		},
	}
}

// newAgentText writes one agent's facts in the main line's units.
func newAgentText(facts *AgentFacts) AgentText {
	_, bar := modelglyph.EffortBar(facts.Effort)
	text := AgentText{EffortBar: bar, Context: formatContextTokens(facts.Context)}
	if facts.Model != "" {
		text.Glyph, text.Label = modelglyph.Symbol(facts.Model), modelLabel(facts.Model)
	}
	if facts.Spend != nil {
		text.In, text.Out = formatContextTokens(facts.Spend.In), formatContextTokens(facts.Spend.Out)
		if facts.Spend.USDKnown {
			text.USD = usdText(facts.Spend.USD)
		}
	}
	return text
}
