// Package theme contains pfm's embedded visual palettes.
package theme

import (
	"fmt"
	"os"

	pfmengine "hostops/pfm/internal/engine"
)

const (
	orangeColor          = "#ff9e64"
	defaultOpencodeColor = "#5eead4"
	tokyoOpencodeColor   = "#73daca"
	tokyoAccentColor     = "#7dcfff"
	tokyoWarningColor    = "#e0af68"
)

// Palette is the color vocabulary shared by the picker and status panels.
//
// EngineRow/StatsEngine carry engine IDENTITY rather than a free slot in the
// ramp: Claude is orange, Codex is light blue, OpenCode is green, in every
// palette.
// A theme restyles the shade, never the hue — an operator scanning a mixed
// fleet reads the engine off the colour before reading the row.
// The extra fields preserve the non-row literals that were already part of
// the default renderer, so a palette change cannot leave a stray hardcoded
// color behind.
type Palette struct {
	EngineRow   map[pfmengine.ID]string
	AgentRow    string
	StatsEngine map[pfmengine.ID]string
	StatsCPU    string
	StatsRAM    string
	// StatsName colours the NAME cell of every stats table — a chat's name and
	// a Docker container's alike. It is its own token because a name is not an
	// engine: both cells used to borrow the Claude row colour, which was
	// harmless while those colours were only a ramp and wrong the moment they
	// became an identity. A Codex chat whose NAME renders in Claude's orange
	// beside an engine cell that renders white is a row disagreeing with
	// itself; the engine cell carries the identity, the name does not.
	StatsName     string
	Accent        string
	Muted         string
	Warn          string
	CosmosLineage string
	CosmosStar    string
	CosmosBright  string
	// CosmosSun colours the project stars — the anchors every chat of one
	// project orbits. Warm on purpose: a sun, not another chat.
	CosmosSun string
	// CosmosSunHot is the blue-white end of a star's temperature: the cosmos
	// blends CosmosSun toward it as a project's hourly traffic rises, so a
	// busy system burns like an O-class star and a sleeping one stays a warm
	// dwarf.
	CosmosSunHot string

	Header      string
	HeaderBg    string
	GroupA      string
	GroupB      string
	Border      string
	Selected    string
	Dim         string
	StatsHeader string
	StatsMemory string
	StatsToken  string
	StatsGear   string
	StatsImage  string
	LimitGreen  string
	LimitAmber  string
	LimitRed    string
	Label       string
}

var defaultPalette = Palette{
	EngineRow: map[pfmengine.ID]string{
		pfmengine.Claude: orangeColor, pfmengine.Codex: "#38bdf8", pfmengine.Opencode: defaultOpencodeColor,
	},
	AgentRow: "#fb923c",
	StatsEngine: map[pfmengine.ID]string{
		pfmengine.Claude: orangeColor, pfmengine.Codex: "#38bdf8", pfmengine.Opencode: defaultOpencodeColor,
	},
	StatsCPU:      "#4ade80",
	StatsRAM:      "#60a5fa",
	StatsName:     "#67e8f9",
	Accent:        "#22d3ee",
	Muted:         "#94a3b8",
	Warn:          "#fde047",
	CosmosLineage: "#828CA5",
	CosmosStar:    "#52586E",
	CosmosBright:  "#E1E6F0",
	CosmosSun:     "#FFD27D",
	CosmosSunHot:  "#BFD7FF",
	Header:        "#ffffff",
	HeaderBg:      "#5f3dc4",
	GroupA:        defaultOpencodeColor,
	GroupB:        "#7dd3fc",
	Border:        "#64748b",
	Selected:      "#334155",
	Dim:           "#94a3b8",
	StatsHeader:   "#22d3ee",
	StatsMemory:   "#60a5fa",
	StatsToken:    "#facc15",
	StatsGear:     "#fb923c",
	StatsImage:    "#c084fc",
	LimitGreen:    "#4ade80",
	LimitAmber:    "#facc15",
	LimitRed:      "#fb7185",
	Label:         "#67e8f9",
}

var tokyoNightPalette = Palette{
	EngineRow: map[pfmengine.ID]string{
		pfmengine.Claude: "#ffb38a", pfmengine.Codex: "#89ddff", pfmengine.Opencode: tokyoOpencodeColor,
	},
	AgentRow: "#bb9af7",
	StatsEngine: map[pfmengine.ID]string{
		pfmengine.Claude: "#ffb38a", pfmengine.Codex: "#89ddff", pfmengine.Opencode: tokyoOpencodeColor,
	},
	StatsCPU:      "#9ece6a",
	StatsRAM:      "#7aa2f7",
	StatsName:     "#2ac3de",
	Accent:        tokyoAccentColor,
	Muted:         "#565f89",
	Warn:          tokyoWarningColor,
	CosmosLineage: "#828CA5",
	CosmosStar:    "#52586E",
	CosmosBright:  "#E1E6F0",
	CosmosSun:     "#E0AF68",
	CosmosSunHot:  "#B4C8FF",
	Header:        "#c0caf5",
	HeaderBg:      "#414868",
	GroupA:        tokyoOpencodeColor,
	GroupB:        tokyoAccentColor,
	Border:        "#3b4261",
	Selected:      "#292e42",
	Dim:           "#565f89",
	StatsHeader:   tokyoAccentColor,
	StatsMemory:   "#7aa2f7",
	StatsToken:    tokyoWarningColor,
	StatsGear:     orangeColor,
	StatsImage:    "#bb9af7",
	LimitGreen:    "#9ece6a",
	LimitAmber:    tokyoWarningColor,
	LimitRed:      "#f7768e",
	Label:         "#2ac3de",
}

// Load returns an embedded palette. Unknown names intentionally degrade to
// the default while making the repair visible to the operator.
func Load(name string) Palette {
	var palette Palette
	switch name {
	case "", "default":
		palette = defaultPalette
	case "tokyo-night":
		palette = tokyoNightPalette
	default:
		fmt.Fprintf(os.Stderr, "pfm: unknown theme %q; using default\n", name)
		palette = defaultPalette
	}
	return withEngineFallbacks(palette)
}

func withEngineFallbacks(palette Palette) Palette {
	engineRows := make(map[pfmengine.ID]string, len(palette.EngineRow)+len(pfmengine.All()))
	for id, color := range palette.EngineRow {
		engineRows[id] = color
	}
	statsRows := make(map[pfmengine.ID]string, len(palette.StatsEngine)+len(pfmengine.All()))
	for id, color := range palette.StatsEngine {
		statsRows[id] = color
	}
	for _, id := range pfmengine.All() {
		if engineRows[id] == "" {
			engineRows[id] = palette.Accent
		}
		if statsRows[id] == "" {
			statsRows[id] = palette.Accent
		}
	}
	palette.EngineRow = engineRows
	palette.StatsEngine = statsRows
	return palette
}
