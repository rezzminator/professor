package herdr

import (
	"fmt"
	"maps"
	"slices"
	"unicode"
	"unicode/utf8"
)

// The tab status glyph keys: one per state DecoratedTabLabel draws, and the
// off phase of the two states that blink.
const (
	TabGlyphWorking    = "working"
	TabGlyphIdle       = "idle"
	TabGlyphBlocked    = "blocked"
	TabGlyphBlockedOff = "blockedOff"
	TabGlyphError      = "error"
	TabGlyphErrorOff   = "errorOff"
	TabGlyphDead       = "dead"
	TabGlyphUnknown    = "unknown"
)

// MaxTabGlyphRunes is the prefix budget one glyph may take: room for an emoji
// with a variation selector or a short ZWJ sequence. DecoratedTabLabel
// reserves the glyph's runes and a space before truncating the base.
const MaxTabGlyphRunes = 8

// TabGlyphs are the emoji-mode tab status glyphs; an empty field keeps its
// default (defaultTabGlyphs). The value is comparable, so a tab label record
// holding it still compares with ==.
type TabGlyphs struct {
	Working    string `json:"working,omitempty"`
	Idle       string `json:"idle,omitempty"`
	Blocked    string `json:"blocked,omitempty"`
	BlockedOff string `json:"blockedOff,omitempty"`
	Error      string `json:"error,omitempty"`
	ErrorOff   string `json:"errorOff,omitempty"`
	Dead       string `json:"dead,omitempty"`
	Unknown    string `json:"unknown,omitempty"`
}

// defaultTabGlyphs blink each attention state between its filled shape and
// a hollow or lighter one of the same red: blocked 🔴 ↔ ⭕, and error 🟥 ↔ ❌,
// since Unicode has no hollow red square and ❌ keeps the hue and the error
// reading while staying distinct from blocked's circle.
var defaultTabGlyphs = TabGlyphs{
	Working: "🟡", Idle: "🟢", Blocked: "🔴", BlockedOff: "⭕",
	Error: "🟥", ErrorOff: "❌", Dead: "⬛", Unknown: "❔",
}

func (glyphs *TabGlyphs) fields() map[string]*string {
	return map[string]*string{
		TabGlyphWorking: &glyphs.Working, TabGlyphIdle: &glyphs.Idle,
		TabGlyphBlocked: &glyphs.Blocked, TabGlyphBlockedOff: &glyphs.BlockedOff,
		TabGlyphError: &glyphs.Error, TabGlyphErrorOff: &glyphs.ErrorOff,
		TabGlyphDead: &glyphs.Dead, TabGlyphUnknown: &glyphs.Unknown,
	}
}

// TabGlyphKeys are the keys a theme may set a tab glyph for, sorted.
func TabGlyphKeys() []string {
	return slices.Sorted(maps.Keys((&TabGlyphs{}).fields()))
}

// glyph is key's configured glyph, or its default when none is configured.
func (glyphs TabGlyphs) glyph(key string) string {
	if configured := *glyphs.fields()[key]; configured != "" {
		return configured
	}
	return *defaultTabGlyphs.fields()[key]
}

// ValidTabGlyph refuses a glyph DecoratedTabLabel cannot render as written:
// empty, invalid UTF-8, over MaxTabGlyphRunes, or holding whitespace or a
// control character, which the tab label cleaning (TabLabel) would rewrite,
// so the label Herdr shows would never equal the one pfm rendered.
func ValidTabGlyph(glyph string) error {
	if glyph == "" {
		return fmt.Errorf("empty glyph")
	}
	if !utf8.ValidString(glyph) {
		return fmt.Errorf("glyph %q is not valid UTF-8", glyph)
	}
	if runes := utf8.RuneCountInString(glyph); runes > MaxTabGlyphRunes {
		return fmt.Errorf("glyph %q is %d runes, over the %d-rune tab prefix budget", glyph, runes, MaxTabGlyphRunes)
	}
	for _, r := range glyph {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("glyph %q holds whitespace or a control character", glyph)
		}
	}
	return nil
}

// ParseTabGlyphs reads glyphs keyed by TabGlyphKeys, each one ValidTabGlyph:
// an unknown key or an invalid glyph is an error naming the key. A key
// absent keeps its default.
func ParseTabGlyphs(values map[string]string) (TabGlyphs, error) {
	var glyphs TabGlyphs
	fields := glyphs.fields()
	for _, key := range slices.Sorted(maps.Keys(values)) {
		field, known := fields[key]
		if !known {
			return TabGlyphs{}, fmt.Errorf("tab glyph %q: not a tab state (%v)", key, TabGlyphKeys())
		}
		if err := ValidTabGlyph(values[key]); err != nil {
			return TabGlyphs{}, fmt.Errorf("tab glyph %q: %w", key, err)
		}
		*field = values[key]
	}
	return glyphs, nil
}
