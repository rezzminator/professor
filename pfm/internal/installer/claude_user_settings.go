package installer

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
)

// wireClaudeUserSettings writes every user setting of the launch defaults
// (claudelaunch.UserSettingDefaults over claude.settings), values Claude Code
// honours only from an account's own settings.json on some path, into the
// settings.json of every Claude account, once per physical file and through a
// symlinked settings.json to the file it names. A key the file already holds
// keeps its value, whatever it is: the step adds, never overrides, splicing
// each added key after the last member so every other byte stays as it was;
// an absent or empty file is created 0600.
func (installer *engine) wireClaudeUserSettings() error {
	wanted := claudelaunch.UserSettingDefaults(installer.options.ClaudeSettings)
	if len(wanted) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var failures []error
	for _, account := range installer.options.ClaudeAccounts {
		path := filepath.Join(account.ConfigDir, claudeSettingsName)
		physical := physicalSettingsPath(path)
		if seen[physical] {
			continue
		}
		seen[physical] = true
		raw, _, err := readSettingsForEdit(path)
		if err != nil {
			failures = append(failures, installer.fail(fmt.Errorf(
				"claude user settings in %s: settings unreadable, state unknown: %w", path, err,
			)))
			continue
		}
		content, added, err := addAbsentUserSettings(raw, wanted)
		if err != nil {
			failures = append(failures, installer.fail(fmt.Errorf("claude user settings in %s: %w", path, err)))
			continue
		}
		if len(added) == 0 {
			installer.ok("claude user settings set in " + path)
			continue
		}
		edit := func() error {
			resolved, mode, err := settingsEditTarget(path)
			if err != nil {
				return err
			}
			return writeWhileUnchanged(path, resolved, mode, raw, content, os.ReadFile,
				func(current []byte) ([]byte, bool, error) {
					next, added, err := addAbsentUserSettings(current, wanted)
					return next, len(added) != 0, err
				})
		}
		message := fmt.Sprintf("set Claude user settings [%s] in %s", strings.Join(added, " "), physical)
		if err := installer.change(message, edit); err != nil {
			failures = append(failures, installer.fail(fmt.Errorf("set claude user settings in %s: %w", physical, err)))
		}
	}
	return errors.Join(failures...)
}

// addAbsentUserSettings is the settings.json raw with each wanted key it lacks
// spliced in after its last member, every other byte kept, and each added one
// as key=value (reason); a file without a member (absent, blank, {} or null)
// becomes the added keys alone.
func addAbsentUserSettings(raw []byte, wanted []claudelaunch.UserSettingDefault) ([]byte, []string, error) {
	var document map[string]any
	if len(bytes.TrimSpace(raw)) != 0 {
		if err := unmarshalKeepingNumbers(raw, &document); err != nil {
			return nil, nil, fmt.Errorf("parse: %w", err)
		}
	}
	var added []string
	fresh := map[string]any{}
	for _, setting := range wanted {
		if _, present := document[setting.Key]; present {
			continue
		}
		if len(document) == 0 {
			var value any
			if err := unmarshalKeepingNumbers(setting.Value, &value); err != nil {
				return nil, nil, fmt.Errorf("user setting %s: %w", setting.Key, err)
			}
			fresh[setting.Key] = value
		} else {
			next, err := setTopLevelKey(raw, setting.Key, setting.Value)
			if err != nil {
				return nil, nil, fmt.Errorf("user setting %s: %w", setting.Key, err)
			}
			raw = next
		}
		added = append(added, fmt.Sprintf("%s=%s (%s)", setting.Key, setting.Value, setting.Reason))
	}
	if len(document) == 0 && len(added) != 0 {
		content, err := encodeSettingsDocument(fresh)
		return content, added, err
	}
	return raw, added, nil
}
