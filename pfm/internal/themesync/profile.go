package themesync

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// claudeTokenSource is Claude Code's theme token ids, one per line, `#` lines
// its provenance; claudeTokens is it parsed. An override key outside it is
// refused: Claude Code would ignore it, so a typo would colour nothing.
//
//go:embed claude_token_ids.txt
var claudeTokenSource string

var (
	claudeTokenID = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*$`)
	claudeTokens  = parseClaudeTokens(claudeTokenSource)
)

// parseClaudeTokens reads the embedded token ids. The source is data compiled
// into the binary, so a malformed or repeated line is a build defect and
// panics at start-up, where profile_test.go catches it.
func parseClaudeTokens(source string) map[string]bool {
	ids := map[string]bool{}
	for _, line := range strings.Split(source, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !claudeTokenID.MatchString(line) || ids[line] {
			panic(fmt.Sprintf("themesync: Claude token line %q is malformed or repeated", line))
		}
		ids[line] = true
	}
	return ids
}

// Profile is one theme profile, templates/themes/{name}.json: Claude Code's
// palette (name, base, overrides; the only keys Claude ever sees) and the
// optional per-surface pins beside it, which win over what derive fills.
type Profile struct {
	Name      string            `json:"name"`
	Base      string            `json:"base,omitempty"`
	Overrides map[string]string `json:"overrides"`
	Surfaces  *SurfacePins      `json:"surfaces,omitempty"`
}

// SurfacePins are a profile's per-surface values, each keyed by the name the
// surface's renderer emits: Tmux by tmux option (tmuxOptions), Herdr by
// custom token (herdrTokens), VSCode by contract key (vscodeContract colors
// and chrome). A pin naming a key its renderer does not emit is an error.
type SurfacePins struct {
	Tmux   map[string]string `json:"tmux,omitempty"`
	Herdr  map[string]string `json:"herdr,omitempty"`
	VSCode *VSCodePins       `json:"vscode,omitempty"`
}

// VSCodePins pin the contract's terminal colours and window chrome. Theme is
// the profile's whole VS Code colour theme (vscode_theme.go): pfm install
// contributes it to VS Code's Color Theme picker, and its colours for the
// contract's keys pin those keys wherever Colors and Chrome do not, so the
// terminal and chrome pfm paints match the theme.
type VSCodePins struct {
	Colors map[string]string `json:"colors,omitempty"`
	Chrome map[string]string `json:"chrome,omitempty"`
	Theme  *VSCodeTheme      `json:"theme,omitempty"`
}

// ProfileDir is where pfm install keeps each bundled profile whole, pins
// included, for the follower to read; the Claude themes dir gets the palette
// only.
func ProfileDir(home string) string {
	return filepath.Join(home, ".local", "share", "pfm", "install", "themes")
}

// ProfilePath is one profile's file in ProfileDir.
func ProfilePath(home, name string) string { return filepath.Join(ProfileDir(home), name+".json") }

// ParseProfile reads a profile strictly: an unknown top-level key, trailing
// data or an invalid pin is an error, so nothing but the palette reaches
// Claude Code.
func ParseProfile(content []byte) (Profile, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var profile Profile
	if err := decoder.Decode(&profile); err != nil {
		return Profile{}, fmt.Errorf("parse profile: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Profile{}, errors.New("parse profile: data after the top-level value")
	}
	if profile.Name == "" {
		return Profile{}, errors.New("parse profile: no name")
	}
	if err := validatePalette(profile); err != nil {
		return Profile{}, fmt.Errorf("profile %s: %w", profile.Name, err)
	}
	if err := validatePins(profile.Surfaces); err != nil {
		return Profile{}, fmt.Errorf("profile %s: %w", profile.Name, err)
	}
	return profile, nil
}

// validatePalette checks Claude Code's half of a profile: base empty or a
// built-in theme, every override key a Claude token, every value a colour
// spelling Claude reads. The first failure in key order is the error.
func validatePalette(profile Profile) error {
	if _, ok := builtinThemes[profile.Base]; profile.Base != "" && !ok {
		return fmt.Errorf("base %q: not a Claude Code built-in theme (%s)", profile.Base,
			strings.Join(slices.Sorted(maps.Keys(builtinThemes)), ", "))
	}
	for _, key := range slices.Sorted(maps.Keys(profile.Overrides)) {
		if !claudeTokens[key] {
			return fmt.Errorf("overrides %q: not a Claude Code theme token (claude_token_ids.txt)", key)
		}
		if _, err := parseColor(profile.Overrides[key]); err != nil {
			return fmt.Errorf("overrides %q: %w", key, err)
		}
	}
	return nil
}

// ClaudeFile is the profile as Claude Code's theme file: content verbatim
// when its top-level keys are name, base and overrides alone, else those
// three re-encoded, two-space indented with a trailing newline.
func (profile Profile) ClaudeFile(content []byte) ([]byte, error) {
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(content, &keys); err != nil {
		return nil, fmt.Errorf("parse profile %s: %w", profile.Name, err)
	}
	if _, pinned := keys["surfaces"]; !pinned {
		return content, nil
	}
	palette := Profile{Name: profile.Name, Base: profile.Base, Overrides: profile.Overrides}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(palette); err != nil {
		return nil, fmt.Errorf("encode profile %s palette: %w", profile.Name, err)
	}
	return out.Bytes(), nil
}
