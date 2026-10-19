package themesync

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// vscodeColorIDSource is VS Code's colour registry, one id per line, `#`
// lines its provenance; vscodeColorIDs is it parsed. A theme colour key
// outside it is refused: VS Code would ignore it, so a typo would paint
// nothing and pass unseen.
//
//go:embed vscode_color_ids.txt
var vscodeColorIDSource string

var vscodeColorIDs = parseVSCodeColorIDs(vscodeColorIDSource)

// parseVSCodeColorIDs reads the embedded registry. The source is data
// compiled into the binary, so a malformed line is a build defect and panics
// at start-up, where vscode_theme_test.go catches it.
func parseVSCodeColorIDs(source string) map[string]bool {
	ids := map[string]bool{}
	for _, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !vscodeColorID.MatchString(line) || ids[line] {
			panic(fmt.Sprintf("themesync: VS Code colour registry line %q is malformed or repeated", line))
		}
		ids[line] = true
	}
	return ids
}

var (
	vscodeColorID = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9]*(\.[a-zA-Z][a-zA-Z0-9]*)*$`)
	// vscodeSemanticSelector is a semanticTokenColors key: a token type or
	// `*`, then modifiers, then an optional `:language`.
	vscodeSemanticSelector = regexp.MustCompile(
		`^(\*|[a-zA-Z][a-zA-Z0-9_-]*)(\.[a-zA-Z][a-zA-Z0-9_-]*)*(:[a-zA-Z][a-zA-Z0-9_-]*)?$`,
	)
	// vscodeTextMateScope is one TextMate scope selector: dotted scope names,
	// descendant-separated by spaces, an optional leading `-` excluding one.
	vscodeTextMateScope = regexp.MustCompile(
		`^-?[a-zA-Z0-9_+-]+(\.[a-zA-Z0-9_+-]+)*( -?[a-zA-Z0-9_+-]+(\.[a-zA-Z0-9_+-]+)*)*$`,
	)
)

// vscodeFontStyles are the words a tokenColors fontStyle combines; an empty
// fontStyle resets what a broader scope set.
var vscodeFontStyles = []string{"italic", "bold", "underline", "strikethrough"}

// VSCodeTheme is a VS Code colour theme's colours: the workbench colours, the
// TextMate token colours and the semantic token colours. Every profile's
// theme is derived from its palette (deriveVSCodeTheme); a profile's
// surfaces.vscode.theme is its own crafted layer over that, each part
// optional, each key it names replacing derive's (mergeVSCodeTheme). pfm
// writes the result into the Professor extension at install, adding the
// name and type from the profile, so the slot carries the colours alone. Its
// colours for the keys the contract renders (terminal and window chrome) are
// that profile's VS Code pins too, so the terminal and chrome pfm paints
// over any theme match this one.
type VSCodeTheme struct {
	Colors               map[string]string              `json:"colors,omitempty"`
	TokenColors          []VSCodeTokenColor             `json:"tokenColors,omitempty"`
	SemanticHighlighting *bool                          `json:"semanticHighlighting,omitempty"`
	SemanticTokenColors  map[string]VSCodeSemanticStyle `json:"semanticTokenColors,omitempty"`
}

// VSCodeTokenColor is one tokenColors rule: the TextMate scopes it colours
// and how. Scope is always an array here; VS Code's comma-joined string form
// is refused so every rule reads one way.
type VSCodeTokenColor struct {
	Name     string              `json:"name,omitempty"`
	Scope    []string            `json:"scope"`
	Settings VSCodeTokenSettings `json:"settings"`
}

// VSCodeTokenSettings is a tokenColors rule's style. FontStyle is a pointer
// because an empty one is meaningful: it resets a broader rule's style.
type VSCodeTokenSettings struct {
	Foreground string  `json:"foreground,omitempty"`
	FontStyle  *string `json:"fontStyle,omitempty"`
}

// VSCodeSemanticStyle is one semanticTokenColors value. VS Code takes a bare
// colour or a style object; the bare colour reads into Foreground and every
// value is written back as the object.
type VSCodeSemanticStyle struct {
	Foreground    string `json:"foreground,omitempty"`
	Bold          *bool  `json:"bold,omitempty"`
	Italic        *bool  `json:"italic,omitempty"`
	Underline     *bool  `json:"underline,omitempty"`
	Strikethrough *bool  `json:"strikethrough,omitempty"`
}

// UnmarshalJSON reads a bare colour string or the style object, strictly.
func (style *VSCodeSemanticStyle) UnmarshalJSON(content []byte) error {
	trimmed := bytes.TrimSpace(content)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var colour string
		if err := json.Unmarshal(trimmed, &colour); err != nil {
			return err
		}
		*style = VSCodeSemanticStyle{Foreground: colour}
		return nil
	}
	type plain VSCodeSemanticStyle
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	var value plain
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*style = VSCodeSemanticStyle(value)
	return nil
}

// validateVSCodeTheme checks a theme against what VS Code reads: every
// colour key in its registry with a hex value, every token rule scoped and
// styled, every semantic selector well formed and styled. Every part may be
// empty: a profile's theme sets only what it crafts, derive fills the rest.
// The first failure in key order is the error, naming where it sits.
func validateVSCodeTheme(theme *VSCodeTheme) error {
	for _, key := range slices.Sorted(maps.Keys(theme.Colors)) {
		if !vscodeColorIDs[key] {
			return fmt.Errorf("vscode theme colors %q: not a VS Code colour id (vscode_color_ids.txt)", key)
		}
		if !vscodeHexColour.MatchString(theme.Colors[key]) {
			return fmt.Errorf(
				"vscode theme colors %q: %q is not #rgb, #rgba, #rrggbb or #rrggbbaa",
				key,
				theme.Colors[key],
			)
		}
	}
	for index, rule := range theme.TokenColors {
		where := fmt.Sprintf("vscode theme tokenColors[%d]", index)
		if rule.Name != "" {
			where += " (" + rule.Name + ")"
		}
		if len(rule.Scope) == 0 {
			return fmt.Errorf("%s: no scope", where)
		}
		for _, scope := range rule.Scope {
			if !vscodeTextMateScope.MatchString(scope) {
				return fmt.Errorf("%s: scope %q is not a TextMate scope selector", where, scope)
			}
		}
		settings := rule.Settings
		if settings.Foreground == "" && settings.FontStyle == nil {
			return fmt.Errorf("%s: settings set neither foreground nor fontStyle", where)
		}
		if settings.Foreground != "" && !vscodeHexColour.MatchString(settings.Foreground) {
			return fmt.Errorf("%s: foreground %q is not a hex colour", where, settings.Foreground)
		}
		if settings.FontStyle != nil {
			for _, word := range strings.Fields(*settings.FontStyle) {
				if !slices.Contains(vscodeFontStyles, word) {
					return fmt.Errorf(
						"%s: fontStyle word %q is not one of %s",
						where,
						word,
						strings.Join(vscodeFontStyles, ", "),
					)
				}
			}
		}
	}
	for _, selector := range slices.Sorted(maps.Keys(theme.SemanticTokenColors)) {
		where := fmt.Sprintf("vscode theme semanticTokenColors %q", selector)
		if !vscodeSemanticSelector.MatchString(selector) {
			return fmt.Errorf("%s: not a semantic token selector", where)
		}
		style := theme.SemanticTokenColors[selector]
		if style.Foreground == "" && style.Bold == nil && style.Italic == nil && style.Underline == nil &&
			style.Strikethrough == nil {
			return fmt.Errorf("%s: sets no style", where)
		}
		if style.Foreground != "" && !vscodeHexColour.MatchString(style.Foreground) {
			return fmt.Errorf("%s: foreground %q is not a hex colour", where, style.Foreground)
		}
	}
	return nil
}

// foldThemePins lays a theme's colours for the contract's keys under the
// explicit pins: an explicit pin wins, the theme fills every contract key it
// sets that no pin names. keys is the contract's own key set.
func foldThemePins(pinned, theme map[string]string, keys map[string]pinKind) map[string]string {
	for key, value := range theme {
		if _, rendered := keys[key]; !rendered {
			continue
		}
		if _, explicit := pinned[key]; explicit {
			continue
		}
		if pinned == nil {
			pinned = map[string]string{}
		}
		pinned[key] = value
	}
	return pinned
}

// VSCodeThemeDir is the directory, relative to the Professor extension's
// root, that pfm install writes each Professor colour theme and the index
// into; VSCodeThemeIndexFile is the index's name there, the one map the
// extension reads from a theme id to the `theme` value it selects.
const (
	VSCodeThemeDir       = "themes"
	VSCodeThemeIndexFile = "index.json"
	vscodeThemeIDPrefix  = "professor-"
	vscodeThemeLabelHead = "Professor · "
)

// VSCodeThemeFile is one Professor colour theme as pfm install writes it:
// ID is its contributes.themes id and so its workbench.colorTheme value,
// Label what VS Code's picker lists, UITheme its base, Path its file
// relative to the extension root, Value the `theme` value `pfm theme use`
// takes for it, and Content the theme file.
type VSCodeThemeFile struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	UITheme string `json:"uiTheme"`
	Path    string `json:"path"`
	Value   string `json:"value"`
	Content []byte `json:"-"`
}

// vscodeThemeDocument is the written theme file, its keys in VS Code's own order.
type vscodeThemeDocument struct {
	Schema               string                         `json:"$schema"`
	Name                 string                         `json:"name"`
	Type                 string                         `json:"type"`
	SemanticHighlighting bool                           `json:"semanticHighlighting"`
	Colors               map[string]string              `json:"colors"`
	TokenColors          []VSCodeTokenColor             `json:"tokenColors"`
	SemanticTokenColors  map[string]VSCodeSemanticStyle `json:"semanticTokenColors,omitempty"`
}

// RenderVSCodeTheme turns one installed theme (its whole file, named stem)
// into its Professor colour theme: the theme its palette derives
// (deriveVSCodeTheme), the profile's own surfaces.vscode.theme laid over it
// (mergeVSCodeTheme), then every contract key (terminal, chrome) set to what
// the contract renders for the profile, so the theme and what pfm paints
// over it never disagree. A file that does not parse or derive is an error
// naming it.
func RenderVSCodeTheme(stem string, content []byte) (VSCodeThemeFile, error) {
	if stem == "" || strings.ContainsAny(stem, `/\:`) {
		return VSCodeThemeFile{}, fmt.Errorf("themesync: VS Code theme %q: not a bare profile name", stem)
	}
	profile, err := ParseProfile(content)
	if err != nil {
		return VSCodeThemeFile{}, fmt.Errorf("themesync: VS Code theme %s: %w", stem, err)
	}
	value := CustomValue(stem)
	tokens, side := customPalette(profile.Base, profile.Overrides)
	variant, err := deriveVariant(value, tokens)
	if err != nil {
		return VSCodeThemeFile{}, fmt.Errorf("themesync: VS Code theme %s: %w", stem, err)
	}
	pins, err := canonicalPins(profile.Surfaces)
	if err != nil {
		return VSCodeThemeFile{}, fmt.Errorf("themesync: VS Code theme %s: %w", stem, err)
	}
	theme, err := deriveVSCodeTheme(value, tokens, variant, side)
	if err != nil {
		return VSCodeThemeFile{}, fmt.Errorf("themesync: VS Code theme %s: %w", stem, err)
	}
	if profile.Surfaces != nil && profile.Surfaces.VSCode != nil && profile.Surfaces.VSCode.Theme != nil {
		theme = mergeVSCodeTheme(theme, *profile.Surfaces.VSCode.Theme)
	}
	contract := Applied{
		Value:   value,
		Title:   profile.Name,
		Side:    side,
		Primary: variant,
		Pins:    pins,
	}.vscodeContract(
		side,
		0,
	)
	maps.Copy(theme.Colors, contract.Colors)
	maps.Copy(theme.Colors, contract.Chrome)
	if err := validateVSCodeTheme(&theme); err != nil {
		return VSCodeThemeFile{}, fmt.Errorf("themesync: VS Code theme %s: %w", stem, err)
	}

	name, _, _ := strings.Cut(profile.Name, " · ")
	if strings.TrimSpace(name) == "" {
		name = stem
	}
	file := VSCodeThemeFile{
		ID: vscodeThemeIDPrefix + stem, Label: vscodeThemeLabelHead + strings.TrimSpace(name),
		UITheme: "vs-dark", Value: value,
	}
	file.Path = "./" + VSCodeThemeDir + "/" + file.ID + ".json"
	kind := string(Dark)
	if side == Light {
		file.UITheme, kind = "vs", string(Light)
	}
	file.Content, err = encodeIndented(vscodeThemeDocument{
		Schema:               "vscode://schemas/color-theme",
		Name:                 file.Label,
		Type:                 kind,
		SemanticHighlighting: theme.SemanticHighlighting == nil || *theme.SemanticHighlighting,
		Colors:               theme.Colors,
		TokenColors:          theme.TokenColors,
		SemanticTokenColors:  theme.SemanticTokenColors,
	})
	if err != nil {
		return VSCodeThemeFile{}, fmt.Errorf("themesync: VS Code theme %s: %w", stem, err)
	}
	return file, nil
}

// mergeVSCodeTheme lays a profile's own theme over the derived one: each
// colour and semantic selector it names replaces derive's, and each TextMate
// scope it styles leaves every derived rule (a derived rule left with no
// scope is dropped), its rules following derive's. Nothing derive set that
// the profile does not name is lost.
func mergeVSCodeTheme(derived, own VSCodeTheme) VSCodeTheme {
	merged := VSCodeTheme{
		Colors:               maps.Clone(derived.Colors),
		SemanticHighlighting: derived.SemanticHighlighting,
		SemanticTokenColors:  maps.Clone(derived.SemanticTokenColors),
	}
	if merged.Colors == nil {
		merged.Colors = map[string]string{}
	}
	maps.Copy(merged.Colors, own.Colors)
	if own.SemanticHighlighting != nil {
		merged.SemanticHighlighting = own.SemanticHighlighting
	}
	if len(own.SemanticTokenColors) > 0 && merged.SemanticTokenColors == nil {
		merged.SemanticTokenColors = map[string]VSCodeSemanticStyle{}
	}
	maps.Copy(merged.SemanticTokenColors, own.SemanticTokenColors)
	claimed := map[string]bool{}
	for _, rule := range own.TokenColors {
		for _, scope := range rule.Scope {
			claimed[scope] = true
		}
	}
	for _, rule := range derived.TokenColors {
		var kept []string
		for _, scope := range rule.Scope {
			if !claimed[scope] {
				kept = append(kept, scope)
			}
		}
		if len(kept) > 0 {
			rule.Scope = kept
			merged.TokenColors = append(merged.TokenColors, rule)
		}
	}
	merged.TokenColors = append(merged.TokenColors, own.TokenColors...)
	return merged
}

// VSCodeThemeIndex is the index file the extension reads: every Professor
// theme's id, label and `theme` value, in the order given.
func VSCodeThemeIndex(files []VSCodeThemeFile) ([]byte, error) {
	if files == nil {
		files = []VSCodeThemeFile{}
	}
	return encodeIndented(struct {
		Themes []VSCodeThemeFile `json:"themes"`
	}{files})
}

// encodeIndented is two-space JSON with a trailing newline, HTML left unescaped.
func encodeIndented(value any) ([]byte, error) {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
