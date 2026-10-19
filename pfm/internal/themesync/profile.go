package themesync

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
)

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

// VSCodePins pin the contract's terminal colours and window chrome.
type VSCodePins struct {
	Colors map[string]string `json:"colors,omitempty"`
	Chrome map[string]string `json:"chrome,omitempty"`
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
	if err := validatePins(profile.Surfaces); err != nil {
		return Profile{}, fmt.Errorf("profile %s: %w", profile.Name, err)
	}
	return profile, nil
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
