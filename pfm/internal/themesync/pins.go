package themesync

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// pinKind is what a pinned value must be.
type pinKind int

const (
	// pinText is a free string: a tmux style option's value.
	pinText pinKind = iota
	// pinColour is a colour parseColor reads, written back as #rrggbb: a tmux
	// chip or @pfm-c-*/@pfm-state-* option, a Herdr token.
	pinColour
	// pinHex is a VS Code colour: #rgb, #rgba, #rrggbb or #rrggbbaa, kept as written.
	pinHex
)

// vscodeHexColour is a colour VS Code reads: 3, 4, 6 or 8 hex digits.
var vscodeHexColour = regexp.MustCompile(`^#([0-9a-fA-F]{3,4}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

// pinKeys are the keys a profile may pin on each surface, each with the kind
// of value it takes; herdrGlyphs are the tab states, each taking glyph frames
// (canonicalGlyphs).
type pinKeys struct {
	tmux, herdr, vscodeColors, vscodeChrome map[string]pinKind
	herdrGlyphs                             map[string]bool
}

// tmuxBookkeeping are the options tmuxOptions renders for the follower's own
// state, not for the theme: a pin on one would corrupt the read-back.
var tmuxBookkeeping = []string{tmuxThemeKey, tmuxGenerationKey, tmuxChipsKey, tmuxStateColourKey}

// renderedPinKeys derives the pinnable keys from the renderers themselves, by
// rendering a built-in dark theme: what a renderer emits is what can be
// pinned, so a key added to a renderer is pinnable with no second list.
func renderedPinKeys() (pinKeys, error) {
	variant, side, err := builtinVariant("dark")
	if err != nil {
		return pinKeys{}, fmt.Errorf("render the pinnable keys: %w", err)
	}
	sample := Applied{Value: "dark", Title: "dark", Side: side, Primary: variant}
	keys := pinKeys{
		tmux: map[string]pinKind{}, herdr: map[string]pinKind{}, herdrGlyphs: map[string]bool{},
		vscodeColors: map[string]pinKind{}, vscodeChrome: map[string]pinKind{},
	}
	for _, option := range tmuxOptions(sample, side, 1) {
		if slices.Contains(tmuxBookkeeping, option.Name) {
			continue
		}
		keys.tmux[option.Name] = pinText
		if _, err := parseColor(option.Value); err == nil {
			keys.tmux[option.Name] = pinColour
		}
	}
	for _, token := range variant.herdrTokens() {
		keys.herdr[token.name] = pinColour
	}
	for _, key := range herdr.TabGlyphKeys() {
		keys.herdrGlyphs[key] = true
	}
	contract := sample.vscodeContract(side, 1)
	for name := range contract.Colors {
		keys.vscodeColors[name] = pinHex
	}
	for name := range contract.Chrome {
		keys.vscodeChrome[name] = pinHex
	}
	return keys, nil
}

// canonicalPins checks every pin against the keys the renderers emit and
// returns them in the one spelling the follower stores: colours as #rrggbb,
// empty maps dropped, nil when nothing is pinned. Applied is compared against
// its state.json round trip, which drops empty maps, so a non-canonical value
// would read as a changed theme every minute. An unknown key or a bad value is
// an error naming the surface and the key.
func canonicalPins(pins *SurfacePins) (*SurfacePins, error) {
	if pins == nil {
		return nil, nil
	}
	keys, err := renderedPinKeys()
	if err != nil {
		return nil, err
	}
	out := &SurfacePins{}
	if out.Tmux, err = canonicalMap("tmux", pins.Tmux, keys.tmux); err != nil {
		return nil, err
	}
	if pins.Herdr != nil {
		herdrOut := &HerdrPins{}
		if herdrOut.Tokens, err = canonicalMap("herdr", pins.Herdr.Tokens, keys.herdr); err != nil {
			return nil, err
		}
		if herdrOut.TabGlyphs, err = canonicalGlyphs(
			"herdr "+herdrTabGlyphsKey, pins.Herdr.TabGlyphs, keys.herdrGlyphs,
		); err != nil {
			return nil, err
		}
		if len(herdrOut.Tokens) > 0 || len(herdrOut.TabGlyphs) > 0 {
			out.Herdr = herdrOut
		}
	}
	if pins.VSCode != nil {
		code := &VSCodePins{}
		colors, chrome := maps.Clone(pins.VSCode.Colors), maps.Clone(pins.VSCode.Chrome)
		// The theme itself never reaches the result, so state.json stays the
		// pins alone; only its colours for the contract's keys do.
		if theme := pins.VSCode.Theme; theme != nil {
			if err := validateVSCodeTheme(theme); err != nil {
				return nil, err
			}
			colors = foldThemePins(colors, theme.Colors, keys.vscodeColors)
			chrome = foldThemePins(chrome, theme.Colors, keys.vscodeChrome)
		}
		if code.Colors, err = canonicalMap("vscode colors", colors, keys.vscodeColors); err != nil {
			return nil, err
		}
		if code.Chrome, err = canonicalMap("vscode chrome", chrome, keys.vscodeChrome); err != nil {
			return nil, err
		}
		if len(code.Colors) > 0 || len(code.Chrome) > 0 {
			out.VSCode = code
		}
	}
	if len(out.Tmux) == 0 && out.Herdr == nil && out.VSCode == nil {
		return nil, nil
	}
	return out, nil
}

// canonicalMap checks one surface's pins in key order, so the first error is stable.
func canonicalMap(surface string, values map[string]string, allowed map[string]pinKind) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for _, key := range slices.Sorted(maps.Keys(values)) {
		kind, known := allowed[key]
		if !known {
			return nil, fmt.Errorf("%s pin %q: %s renders no such key", surface, key, surface)
		}
		value := values[key]
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%s pin %q: empty value", surface, key)
		}
		switch kind {
		case pinColour:
			colour, err := parseColor(value)
			if err != nil {
				return nil, fmt.Errorf("%s pin %q: %w", surface, key, err)
			}
			value = colour.String()
		case pinHex:
			if !vscodeHexColour.MatchString(value) {
				return nil, fmt.Errorf("%s pin %q: %q is not #rgb, #rgba, #rrggbb or #rrggbbaa", surface, key, value)
			}
		case pinText:
		}
		out[key] = value
	}
	return out, nil
}

// canonicalGlyphs checks the tab glyph pins in key order, each state's
// frames herdr.ValidTabStateFrames, an error naming the key and the frame index.
func canonicalGlyphs(
	surface string,
	values map[string]herdr.TabFrames,
	allowed map[string]bool,
) (map[string]herdr.TabFrames, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]herdr.TabFrames, len(values))
	for _, key := range slices.Sorted(maps.Keys(values)) {
		if !allowed[key] {
			return nil, fmt.Errorf("%s pin %q: %s renders no such key", surface, key, surface)
		}
		if err := herdr.ValidTabStateFrames(key, values[key]); err != nil {
			return nil, fmt.Errorf("%s pin %q: %w", surface, key, err)
		}
		out[key] = slices.Clone(values[key])
	}
	return out, nil
}

// validatePins is canonicalPins' verdict alone: ParseProfile refuses a
// profile whose pins name a key no renderer emits or hold a bad value.
func validatePins(pins *SurfacePins) error {
	_, err := canonicalPins(pins)
	return err
}

// tmuxPins, herdrPins and vscodePins are one surface's pins; nil-safe, so a
// theme with no pins renders as before.
func tmuxPins(pins *SurfacePins) map[string]string {
	if pins == nil {
		return nil
	}
	return pins.Tmux
}

func herdrPins(pins *SurfacePins) map[string]string {
	if pins == nil || pins.Herdr == nil {
		return nil
	}
	return pins.Herdr.Tokens
}

func vscodePins(pins *SurfacePins) VSCodePins {
	if pins == nil || pins.VSCode == nil {
		return VSCodePins{}
	}
	return *pins.VSCode
}

// overlayPins lays pins over the values a renderer derived, key by key; a pin
// for a key the renderer did not emit is ignored.
func overlayPins(values, pins map[string]string) {
	for key, pin := range pins {
		if _, emitted := values[key]; emitted {
			values[key] = pin
		}
	}
}

// pinSource is a custom theme's pins and the profile file they came from; file
// is empty when the theme has no profile file.
type pinSource struct {
	pins *SurfacePins
	file string
	at   time.Time
}

// profilePins reads the pins of the profile a `custom:{name}` value names, at
// ProfilePath. Any other value, or a profile file that does not exist, has no
// pins. A file that exists and cannot be read, parsed or validated is an error
// naming it: pins never silently fall away.
func profilePins(home, value string) (pinSource, error) {
	name, custom := strings.CutPrefix(value, customPrefix)
	if !custom {
		return pinSource{}, nil
	}
	if name == "" || strings.ContainsAny(name, `/\`) {
		return pinSource{}, fmt.Errorf(
			"theme profile %q: the name is empty or holds a path separator (a profile is a file directly in %s)",
			name, ProfileDir(home),
		)
	}
	path := ProfilePath(home, name)
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return pinSource{}, nil
	}
	if err != nil {
		return pinSource{}, fmt.Errorf("theme profile %s: %w", path, err)
	}
	// A FIFO or device in place of the file would block the read while the
	// follower holds the theme lock.
	if !info.Mode().IsRegular() {
		return pinSource{}, fmt.Errorf("theme profile %s: not a regular file (%s)", path, info.Mode().Type())
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return pinSource{}, nil
	}
	if err != nil {
		return pinSource{}, fmt.Errorf("theme profile %s: %w", path, err)
	}
	profile, err := ParseProfile(content)
	if err != nil {
		return pinSource{}, fmt.Errorf("theme profile %s: %w", path, err)
	}
	pins, err := canonicalPins(profile.Surfaces)
	if err != nil {
		return pinSource{}, fmt.Errorf("theme profile %s: %w", path, err)
	}
	return pinSource{pins: pins, file: path, at: info.ModTime()}, nil
}
