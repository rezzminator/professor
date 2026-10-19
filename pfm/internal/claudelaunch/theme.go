package claudelaunch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const themeSyncOwnsIt = "theme sync owns it"

// RefreshThemePin reapplies sync ownership to a saved Render recipe. Render
// appends its own --settings last; preceding caller settings stay untouched.
// A settings filename is caller-owned and is never edited.
func RefreshThemePin(args []string) ([]string, error) {
	if _, note := themeForLaunch(""); note != themeSyncOwnsIt {
		return args, nil
	}
	for index := len(args) - 1; index >= 0; index-- {
		flag, payload, inline := splitValueFlag(args[index])
		if flag != flagSettings {
			continue
		}
		if !inline {
			if index+1 >= len(args) {
				return nil, fmt.Errorf("refresh theme pin: %s requires a value", flagSettings)
			}
			payload = args[index+1]
		}
		if !strings.HasPrefix(strings.TrimSpace(payload), "{") {
			return args, nil
		}
		var settings map[string]json.RawMessage
		if err := json.Unmarshal([]byte(payload), &settings); err != nil {
			return nil, fmt.Errorf("refresh theme pin: parse settings: %w", err)
		}
		if _, pinned := settings[knobTheme]; !pinned {
			return args, nil
		}
		delete(settings, knobTheme)
		encoded, err := json.Marshal(settings)
		if err != nil {
			return nil, fmt.Errorf("refresh theme pin: encode settings: %w", err)
		}
		refreshed := append([]string(nil), args...)
		if inline {
			refreshed[index] = flagSettings + "=" + string(encoded)
		} else {
			refreshed[index+1] = string(encoded)
		}
		return refreshed, nil
	}
	return args, nil
}

// themeForLaunch leaves the account settings.json in charge while theme sync
// is enabled. A failed state read keeps the configured pin and reports why.
func themeForLaunch(configured string) (value, note string) {
	enabled, err := readThemeSyncEnabled()
	if err != nil {
		obs.Logger(context.Background()).Warn(
			"claudelaunch: theme sync state unreadable; keeping configured theme",
			obs.FieldErr, err.Error(),
		)
		return configured, "theme sync state unreadable; carrying pin"
	}
	if enabled {
		return "", themeSyncOwnsIt
	}
	return configured, ""
}

// Read only the enabled bit here: pulling the surface synchronizer into the
// launch registry would couple launches to its engine and follower machinery.
// Home is the operator/jail home, not Request.Home (the pfm install home).
func readThemeSyncEnabled() (bool, error) {
	home, err := paths.Home()
	if err != nil {
		return false, fmt.Errorf("resolve theme sync home: %w", err)
	}
	path := filepath.Join(home, ".local", "state", "pfm", "theme", "state.json")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read theme sync state %s: %w", path, err)
	}
	var state struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return false, fmt.Errorf("parse theme sync state %s: %w", path, err)
	}
	return state.Enabled, nil
}
