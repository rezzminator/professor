package themesync

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// The colour ids the readability check measures text against.
const (
	vscodeEditorBackground  = "editor.background"
	vscodeLineHighlight     = "editor.lineHighlightBackground"
	vscodeSideBarBackground = "sideBar.background"
	// vscodeSemanticComment is the semantic token type comments are, held
	// to the comment floor.
	vscodeSemanticComment = "comment"
)

// vscodeReadablePair is one workbench place a theme draws text on a fill:
// the text colour (text, or "foreground" when the theme leaves it unset, as
// VS Code falls back), the fill, and the ground a translucent fill is drawn
// over.
type vscodeReadablePair struct {
	text, fill, ground string
}

// vscodeReadablePairs are the workbench text a reader reads in every session,
// each held at the token floor: inputs, menus, tabs, lists, bars, widgets and
// the terminal.
var vscodeReadablePairs = []vscodeReadablePair{
	{"input.foreground", "input.background", vscodeEditorBackground},
	{"dropdown.foreground", "dropdown.background", vscodeEditorBackground},
	{"sideBar.foreground", vscodeSideBarBackground, vscodeEditorBackground},
	{"tab.activeForeground", "tab.activeBackground", vscodeEditorBackground},
	{"list.activeSelectionForeground", "list.activeSelectionBackground", vscodeSideBarBackground},
	{"statusBar.foreground", "statusBar.background", vscodeEditorBackground},
	{"titleBar.activeForeground", "titleBar.activeBackground", vscodeEditorBackground},
	{"button.foreground", "button.background", vscodeEditorBackground},
	{"editorSuggestWidget.foreground", "editorSuggestWidget.background", vscodeEditorBackground},
	{"editorHoverWidget.foreground", "editorHoverWidget.background", vscodeEditorBackground},
	{"terminal.foreground", "terminal.background", vscodeEditorBackground},
}

// readabilityDocument is the part of a rendered theme file the check reads.
type readabilityDocument struct {
	Colors              map[string]string              `json:"colors"`
	TokenColors         []VSCodeTokenColor             `json:"tokenColors"`
	SemanticTokenColors map[string]VSCodeSemanticStyle `json:"semanticTokenColors"`
}

// CheckVSCodeReadability measures a rendered theme file (RenderVSCodeTheme's
// Content) against the floors derive holds: editor.foreground at 7:1, and
// every TextMate and semantic foreground at 4.5:1 (comments and other text
// that sits back, 3.5:1), on the editor and on the current line; and the
// workbench text of vscodeReadablePairs at 4.5:1 on its fill. A translucent
// colour is measured as VS Code draws it, over its ground. The selection is
// not measured: derive lifts its own syntax off it where a lightness can,
// and a profile's own rules answer for themselves there. A pair whose fill
// the theme leaves to VS Code is not measured; a theme with no
// editor.background cannot be measured and is an error. The error lists
// every place under its floor.
func CheckVSCodeReadability(content []byte) error {
	var document readabilityDocument
	if err := json.Unmarshal(content, &document); err != nil {
		return fmt.Errorf("readability: rendered VS Code theme does not read back: %w", err)
	}
	colours := document.Colors
	editor, err := opaqueColour(colours[vscodeEditorBackground], "")
	if err != nil {
		return fmt.Errorf("readability: colors %q: %w: nothing to measure text against", vscodeEditorBackground, err)
	}
	var problems []error
	ground := func(key string) string {
		value, err := opaqueColour(colours[key], editor)
		if err != nil {
			problems = append(problems, fmt.Errorf("colors %q: %w", key, err))
			return ""
		}
		return value
	}
	line := editor
	if colours[vscodeLineHighlight] != "" {
		line = ground(vscodeLineHighlight)
	}
	measure := func(where, text string, floor float64, fills map[string]string) {
		for _, name := range slices.Sorted(maps.Keys(fills)) {
			fill := fills[name]
			if fill == "" {
				continue
			}
			ink, err := opaqueColour(text, fill)
			if err != nil {
				problems = append(problems, fmt.Errorf("%s: %w", where, err))
				return
			}
			left, errLeft := parseColor(ink)
			right, errRight := parseColor(fill)
			if errLeft != nil || errRight != nil {
				problems = append(problems, fmt.Errorf("%s on %s: %w", where, name, errors.Join(errLeft, errRight)))
				continue
			}
			// A hair under the floor is rounding: derive lifts to the
			// floor in 1% lightness steps measured on the same colours.
			if got := contrastRatio(left, right); got < floor-0.005 {
				problems = append(problems, fmt.Errorf("%s %s on %s %s: contrast %.2f < %.1f",
					where, text, name, fill, got, floor))
			}
		}
	}
	editorFills := map[string]string{vscodeEditorBackground: editor, vscodeLineHighlight: line}
	measure(`colors "editor.foreground"`, colours["editor.foreground"], vscodeTextContrast, editorFills)
	for index, rule := range document.TokenColors {
		if rule.Settings.Foreground == "" {
			continue
		}
		floor := vscodeTokenContrast
		if sitsBack(rule.Scope) {
			floor = vscodeCommentContrast
		}
		measure(fmt.Sprintf("tokenColors[%d] %q", index, rule.Name), rule.Settings.Foreground, floor, editorFills)
	}
	for _, selector := range slices.Sorted(maps.Keys(document.SemanticTokenColors)) {
		style := document.SemanticTokenColors[selector]
		if style.Foreground == "" {
			continue
		}
		floor := vscodeTokenContrast
		if strings.HasPrefix(selector, vscodeSemanticComment) {
			floor = vscodeCommentContrast
		}
		measure(fmt.Sprintf("semanticTokenColors %q", selector), style.Foreground, floor, editorFills)
	}
	for _, pair := range vscodeReadablePairs {
		if colours[pair.fill] == "" {
			continue
		}
		under := editor
		if pair.ground != vscodeEditorBackground && colours[pair.ground] != "" {
			under = ground(pair.ground)
		}
		fill, err := opaqueColour(colours[pair.fill], under)
		if err != nil {
			problems = append(problems, fmt.Errorf("colors %q: %w", pair.fill, err))
			continue
		}
		text, key := colours[pair.text], pair.text
		if text == "" {
			text, key = colours["foreground"], "foreground"
		}
		if text == "" {
			continue
		}
		measure(fmt.Sprintf("colors %q", key), text, vscodeTokenContrast, map[string]string{pair.fill: fill})
	}
	if len(problems) > 0 {
		return fmt.Errorf("readability: %d places under their contrast floor: %w",
			len(problems), errors.Join(problems...))
	}
	return nil
}

// opaqueColour is a VS Code colour (#rgb, #rgba, #rrggbb, #rrggbbaa) as
// drawn: a translucent one laid over ground (#rrggbb), an opaque one as it
// is. A translucent colour with no ground to lie on is an error.
func opaqueColour(value, ground string) (string, error) {
	if digits, ok := strings.CutPrefix(value, "#"); ok && len(digits) == 4 {
		value = "#" + string([]byte{
			digits[0], digits[0], digits[1], digits[1], digits[2], digits[2], digits[3], digits[3],
		})
	}
	switch {
	case len(value) == 9 && value[0] == '#':
		colour, err := parseColor(value[:7])
		if err != nil {
			return "", err
		}
		var alpha uint8
		if _, err := fmt.Sscanf(value[7:], "%02x", &alpha); err != nil {
			return "", fmt.Errorf("alpha of %q: %w", value, err)
		}
		if ground == "" {
			return "", fmt.Errorf("%q is translucent with nothing under it", value)
		}
		return overColour(ground, colour.String(), alpha), nil
	default:
		colour, err := parseColor(value)
		if err != nil {
			return "", err
		}
		return colour.String(), nil
	}
}

// sitsBack reports whether a TextMate rule colours only text that sits back
// on purpose — comments, quotes, struck-through text — and so is held to the
// comment floor, whatever its author named the rule.
func sitsBack(scopes []string) bool {
	for _, scope := range scopes {
		if !strings.HasPrefix(scope, "comment") && !strings.HasPrefix(scope, "punctuation.definition.comment") &&
			!strings.HasPrefix(scope, "string.comment") && scope != "markup.quote" && scope != "markup.strikethrough" {
			return false
		}
	}
	return len(scopes) > 0
}
