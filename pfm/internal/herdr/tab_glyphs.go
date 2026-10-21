package herdr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"unicode"
	"unicode/utf8"
)

// The tab status glyph keys: one per state DecoratedTabLabel draws.
const (
	TabGlyphWorking = "working"
	TabGlyphIdle    = "idle"
	TabGlyphBlocked = "blocked"
	TabGlyphDone    = "done"
	TabGlyphError   = "error"
	TabGlyphDead    = "dead"
	TabGlyphUnknown = "unknown"
)

// MaxTabGlyphRunes is the prefix budget one frame may take: room for an emoji
// with a variation selector or a short ZWJ sequence. DecoratedTabLabel
// reserves the state's widest frame and a space before truncating the base.
const MaxTabGlyphRunes = 8

// MaxTabFrames caps the frames one state may play.
const MaxTabFrames = 12

// TabFrames are one tab state's glyphs, played in order one per half-second
// tick: a single frame is a still glyph, several an animation (a pulse, a
// spinner). JSON takes a string (one frame) or an array of strings; a single
// frame writes back as a string, so either spelling round-trips equal.
type TabFrames []string

// UnmarshalJSON reads a string as one frame or an array as its frames; any
// other shape is an error. The frames are checked by ValidTabFrames.
func (frames *TabFrames) UnmarshalJSON(content []byte) error {
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var frame string
		if err := json.Unmarshal(trimmed, &frame); err != nil {
			return fmt.Errorf("a glyph string or an array of glyph frames: %w", err)
		}
		*frames = TabFrames{frame}
		return nil
	}
	var list []string
	if err := json.Unmarshal(trimmed, &list); err != nil {
		return fmt.Errorf("a glyph string or an array of glyph frames: %w", err)
	}
	if list == nil {
		list = []string{}
	}
	*frames = list
	return nil
}

// MarshalJSON writes one frame as a string, several as an array.
func (frames TabFrames) MarshalJSON() ([]byte, error) {
	var value any = []string(frames)
	if len(frames) == 1 {
		value = frames[0]
	}
	content, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode tab glyph frames: %w", err)
	}
	return content, nil
}

// TabGlyphs are the emoji-mode tab status glyphs; a nil field keeps its
// default (defaultTabGlyphs).
type TabGlyphs struct {
	Working TabFrames `json:"working,omitempty"`
	Idle    TabFrames `json:"idle,omitempty"`
	Blocked TabFrames `json:"blocked,omitempty"`
	Done    TabFrames `json:"done,omitempty"`
	Error   TabFrames `json:"error,omitempty"`
	Dead    TabFrames `json:"dead,omitempty"`
	Unknown TabFrames `json:"unknown,omitempty"`
}

// defaultTabGlyphs: colour carries status, only needs-you moves. Blocked and
// error pulse in red; completion stays yellow. All other states stay still,
// including in themes: only blocked and error may animate.
var defaultTabGlyphs = TabGlyphs{
	Working: TabFrames{"🔵"},
	Idle:    TabFrames{"⚪"},
	Blocked: TabFrames{"🔴", "⭕"},
	Done:    TabFrames{"🟡"},
	Error:   TabFrames{"🟥", "❌"},
	Dead:    TabFrames{"⬛"},
	Unknown: TabFrames{"❔"},
}

func (glyphs *TabGlyphs) fields() map[string]*TabFrames {
	return map[string]*TabFrames{
		TabGlyphWorking: &glyphs.Working, TabGlyphIdle: &glyphs.Idle,
		TabGlyphBlocked: &glyphs.Blocked, TabGlyphDone: &glyphs.Done,
		TabGlyphError: &glyphs.Error, TabGlyphDead: &glyphs.Dead,
		TabGlyphUnknown: &glyphs.Unknown,
	}
}

// TabGlyphKeys are the keys a theme may set tab glyphs for, sorted.
func TabGlyphKeys() []string {
	return slices.Sorted(maps.Keys((&TabGlyphs{}).fields()))
}

// frames is key's configured frames, or its default when none are configured.
func (glyphs TabGlyphs) frames(key string) TabFrames {
	if configured := *glyphs.fields()[key]; len(configured) > 0 {
		return configured
	}
	return *defaultTabGlyphs.fields()[key]
}

// ValidTabGlyph refuses a frame DecoratedTabLabel cannot render as written:
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

// ValidTabFrames refuses frames DecoratedTabLabel cannot play: none, over
// MaxTabFrames, or a frame ValidTabGlyph refuses, named by its index.
func ValidTabFrames(frames TabFrames) error {
	if len(frames) == 0 {
		return fmt.Errorf("no frames: a glyph string or an array of 1 to %d glyph frames", MaxTabFrames)
	}
	if len(frames) > MaxTabFrames {
		return fmt.Errorf("%d frames, over the cap of %d", len(frames), MaxTabFrames)
	}
	for i, frame := range frames {
		if err := ValidTabGlyph(frame); err != nil {
			return fmt.Errorf("frame %d: %w", i, err)
		}
	}
	return nil
}

// ValidTabStateFrames checks frame content and requires one still frame for
// every state except blocked and error, the only states allowed to animate.
func ValidTabStateFrames(state string, frames TabFrames) error {
	if err := ValidTabFrames(frames); err != nil {
		return err
	}
	if state != TabGlyphBlocked && state != TabGlyphError && len(frames) != 1 {
		return fmt.Errorf("tab state %q requires exactly one frame: only blocked and error may animate", state)
	}
	return nil
}

// ParseTabGlyphs reads frames keyed by TabGlyphKeys, each ValidTabStateFrames:
// an unknown key or invalid frames are an error naming the key. A key absent
// keeps its default.
func ParseTabGlyphs(values map[string]TabFrames) (TabGlyphs, error) {
	var glyphs TabGlyphs
	fields := glyphs.fields()
	for _, key := range slices.Sorted(maps.Keys(values)) {
		field, known := fields[key]
		if !known {
			return TabGlyphs{}, fmt.Errorf("tab glyph %q: not a tab state (%v)", key, TabGlyphKeys())
		}
		if err := ValidTabStateFrames(key, values[key]); err != nil {
			return TabGlyphs{}, fmt.Errorf("tab glyph %q: %w", key, err)
		}
		*field = slices.Clone(values[key])
	}
	return glyphs, nil
}

// TabTick is the wall clock's half-second count: every painter reads the
// same tick at the same moment, so concurrent painters draw the same frame.
func TabTick(unixMillis int64) int64 {
	return unixMillis / blinkHalfPeriod.Milliseconds()
}
