package claudelaunch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

const (
	knobNoFlicker = "noFlicker"
	envNoFlicker  = "CLAUDE_CODE_NO_FLICKER"
	// settingsTUI is the top-level settings.json key a seat asks for Claude's
	// renderer with; tuiFullscreen is the value that asks for fullscreen.
	settingsTUI   = "tui"
	tuiFullscreen = "fullscreen"
)

// WantsFullscreen reports whether a seat launched with claude, its layered
// claude.settings, from the account whose store is configDir runs Claude's
// fullscreen renderer. A launch's --settings outranks the account's
// settings.json, so the top-level "tui" the launch carries (EffectiveSettings:
// the shipped "fullscreen" unless claude.settings sets another) decides. A tui
// claude.settings sets to null leaves the launch without one, and the account's
// settings.json decides: "tui": "fullscreen" asks for it, an absent file or an
// unknown store (configDir "") asks for nothing, and a file that cannot be read
// or parsed is an error, never "not fullscreen", since its real state is
// unknown. It is the one implementation of the rule the launch knob,
// `pfm config`, pfm doctor and pfm install share.
func WantsFullscreen(configDir string, claude map[string]any) (bool, error) {
	if carried, present := EffectiveSettings(claude)[settingsTUI]; present {
		tui, isString := carried.(string)
		if !isString {
			return false, fmt.Errorf("claude.settings %q is %v, not a string", settingsTUI, carried)
		}
		return tui == tuiFullscreen, nil
	}
	if configDir == "" {
		return false, nil
	}
	path := filepath.Join(configDir, "settings.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	var settings map[string]json.RawMessage
	if err := json.Unmarshal(raw, &settings); err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	encoded, present := settings[settingsTUI]
	if !present {
		return false, nil
	}
	var tui string
	if err := json.Unmarshal(encoded, &tui); err != nil {
		return false, fmt.Errorf("parse %s: %q is not a string: %w", path, settingsTUI, err)
	}
	return tui == tuiFullscreen, nil
}

// noFlickerValue is the `pfm config` view of the noFlicker knob for a seat of
// the account whose store is configDir launched with claude: the error text when its settings cannot
// be judged, never a "false" that reads as a settled answer.
func noFlickerValue(configDir string, claude map[string]any) string {
	wants, err := WantsFullscreen(configDir, claude)
	if err != nil {
		return err.Error()
	}
	return strconv.FormatBool(wants)
}
