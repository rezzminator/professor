package themesync

import (
	"fmt"
	"math"
)

// deriveTokens are the Claude theme tokens the derivation reads (D11); a
// theme missing one is an error naming it.
var deriveTokens = []string{
	"claude", "inactive", "inactiveShimmer", "inverseText", "permission", "selectionBg", "subtle", "success",
	"text", "userMessageBackground", "red_FOR_SUBAGENTS_ONLY", "green_FOR_SUBAGENTS_ONLY",
	"yellow_FOR_SUBAGENTS_ONLY", "blue_FOR_SUBAGENTS_ONLY", "purple_FOR_SUBAGENTS_ONLY",
	"cyan_FOR_SUBAGENTS_ONLY", "orange_FOR_SUBAGENTS_ONLY",
}

// The contrast guard (P13) the lift enforces: each state hue against the
// terminal background, the muted overlay against the raised surface.
const (
	stateContrast = 3.0
	mutedContrast = 2.5
)

// deriveVariant maps one resolved token set onto a Variant (D11): terminal
// background and foreground from inverseText and text, ANSI 1–6 from the
// sub-agent hues, 8 and 15 from inactive and text; the four agent states from
// claude (working), permission (blocked), success (done) and inactiveShimmer
// (idle). A state hue under the guard against the background, or the muted
// overlay under its guard against the surface, is lifted (liftColor) so every
// surface draws the one lifted hue. A missing or unparseable token is an
// error naming it; name is the theme the error names.
func deriveVariant(name string, tokens map[string]string) (Variant, error) {
	colours := map[string]string{}
	for _, name := range deriveTokens {
		raw, ok := tokens[name]
		if !ok {
			return Variant{}, fmt.Errorf("themesync: theme token %q is missing", name)
		}
		colour, err := parseColor(raw)
		if err != nil {
			return Variant{}, fmt.Errorf("themesync: theme token %q: %w", name, err)
		}
		colours[name] = colour.String()
	}
	pick := func(name string) string { return colours[name] }
	variant := Variant{
		Background:          pick("inverseText"),
		Foreground:          pick("text"),
		Cursor:              pick("text"),
		SelectionBackground: pick("selectionBg"),
		SelectionForeground: pick("text"),
		Surface:             pick("userMessageBackground"),
		Overlay:             pick("subtle"),
		Muted:               pick("inactive"),
		Accent:              pick("claude"),
		Orange:              pick("orange_FOR_SUBAGENTS_ONLY"),
		State: StateColors{
			Working: pick("claude"), Blocked: pick("permission"), Done: pick("success"), Idle: pick("inactiveShimmer"),
		},
	}
	background, surface := variant.Background, variant.Surface
	for _, lifted := range []struct {
		token, against string
		colour         *string
		guard          float64
	}{
		{"working", background, &variant.State.Working, stateContrast},
		{"blocked", background, &variant.State.Blocked, stateContrast},
		{"done", background, &variant.State.Done, stateContrast},
		{"idle", background, &variant.State.Idle, stateContrast},
		{"muted", surface, &variant.Muted, mutedContrast},
	} {
		colour, err := liftColor(name, lifted.token, *lifted.colour, lifted.against, lifted.guard)
		if err != nil {
			return Variant{}, err
		}
		*lifted.colour = colour
	}
	hues := []string{
		pick("red_FOR_SUBAGENTS_ONLY"), pick("green_FOR_SUBAGENTS_ONLY"), pick("yellow_FOR_SUBAGENTS_ONLY"),
		pick("blue_FOR_SUBAGENTS_ONLY"), pick("purple_FOR_SUBAGENTS_ONLY"), pick("cyan_FOR_SUBAGENTS_ONLY"),
	}
	variant.ANSI[0] = pick("inverseText")
	copy(variant.ANSI[1:7], hues)
	variant.ANSI[7] = pick("text")
	variant.ANSI[8] = pick("inactive")
	copy(variant.ANSI[9:15], hues)
	variant.ANSI[15] = pick("text")
	return variant, nil
}

// liftColor returns colour unchanged when it measures at least guard:1 against
// background, else the first colour that does: the same hue and saturation with
// HSL lightness moved in 1% steps away from the background (darker on a
// background light enough that black out-contrasts white, else lighter). When
// even lightness 0% or 100% misses, the error names the theme, the token and
// the ratio reached.
func liftColor(theme, token, colour, background string, guard float64) (string, error) {
	fg, err := parseColor(colour)
	if err != nil {
		return "", fmt.Errorf("themesync: theme %q token %q: %w", theme, token, err)
	}
	bg, err := parseColor(background)
	if err != nil {
		return "", fmt.Errorf("themesync: theme %q token %q background: %w", theme, token, err)
	}
	if contrastRatio(fg, bg) >= guard {
		return colour, nil
	}
	hue, saturation, lightness := fg.hsl()
	direction := 1.0
	if relativeLuminance(bg) > blackWhiteCrossover {
		direction = -1
	}
	best := fg
	for step := 1; step <= 100; step++ {
		moved := math.Min(1, math.Max(0, lightness+direction*float64(step)/100))
		best = hslColor(hue, saturation, moved)
		if contrastRatio(best, bg) >= guard {
			return best.String(), nil
		}
		if moved == 0 || moved == 1 {
			break
		}
	}
	return "", fmt.Errorf(
		"themesync: theme %q token %q: contrast ratio %.2f against %s stays under %.1f at every lightness",
		theme,
		token,
		contrastRatio(best, bg),
		background,
		guard,
	)
}

// blackWhiteCrossover is the background luminance where black and white
// contrast equally: sqrt(1.05 x 0.05) - 0.05.
const blackWhiteCrossover = 0.1791

// relativeLuminance is the WCAG 2.x relative luminance of one colour.
func relativeLuminance(c rgb) float64 {
	channel := func(value uint8) float64 {
		v := float64(value) / 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*channel(c.red) + 0.7152*channel(c.green) + 0.0722*channel(c.blue)
}

// contrastRatio is the WCAG contrast of two colours, 1 to 21.
func contrastRatio(a, b rgb) float64 {
	high, low := relativeLuminance(a), relativeLuminance(b)
	if high < low {
		high, low = low, high
	}
	return (high + 0.05) / (low + 0.05)
}

// hsl is the colour's hue (0 to 6, sextants), saturation and lightness (0 to 1).
func (c rgb) hsl() (hue, saturation, lightness float64) {
	r, g, b := float64(c.red)/255, float64(c.green)/255, float64(c.blue)/255
	high, low := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	lightness = (high + low) / 2
	if high == low {
		return 0, 0, lightness
	}
	delta := high - low
	if lightness > 0.5 {
		saturation = delta / (2 - high - low)
	} else {
		saturation = delta / (high + low)
	}
	switch high {
	case r:
		hue = (g - b) / delta
		if g < b {
			hue += 6
		}
	case g:
		hue = (b-r)/delta + 2
	default:
		hue = (r-g)/delta + 4
	}
	return hue, saturation, lightness
}

// hslColor is the colour of a hue (0 to 6), saturation and lightness, each
// channel rounded to the nearest of 255, a half rounding up (the epsilon keeps
// a channel that is exactly .5 in exact arithmetic from landing under it in
// floating point).
func hslColor(hue, saturation, lightness float64) rgb {
	chroma := (1 - math.Abs(2*lightness-1)) * saturation
	x := chroma * (1 - math.Abs(math.Mod(hue, 2)-1))
	var r, g, b float64
	switch int(hue) {
	case 0:
		r, g, b = chroma, x, 0
	case 1:
		r, g, b = x, chroma, 0
	case 2:
		r, g, b = 0, chroma, x
	case 3:
		r, g, b = 0, x, chroma
	case 4:
		r, g, b = x, 0, chroma
	default:
		r, g, b = chroma, 0, x
	}
	offset := lightness - chroma/2
	toByte := func(v float64) uint8 { return uint8(math.Round((v+offset)*255 + 1e-9)) }
	return rgb{toByte(r), toByte(g), toByte(b)}
}
