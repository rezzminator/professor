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

// WantsFullscreen reports whether the account whose store is configDir asks
// for Claude's fullscreen renderer: its settings.json carries top-level
// "tui": "fullscreen". An absent settings.json asks for nothing; a file that
// cannot be read or parsed is an error, never "not fullscreen", since its real
// state is unknown. It is the one implementation of the rule the launch knob,
// `pfm config`, pfm doctor and pfm install share.
func WantsFullscreen(configDir string) (bool, error) {
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

// noFlickerValue is the `pfm config` view of the noFlicker knob for the
// account whose store is configDir: the error text when its settings cannot
// be judged, never a "false" that reads as a settled answer.
func noFlickerValue(configDir string) string {
	wants, err := WantsFullscreen(configDir)
	if err != nil {
		return err.Error()
	}
	return strconv.FormatBool(wants)
}
