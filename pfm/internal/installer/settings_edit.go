package installer

import (
	"bytes"
	"errors"
	"fmt"
	"os"
)

// EditSettings applies apply to the existing settings.json at path and
// publishes the result by atomic rename through its symlink, keeping the
// file's mode and every number's spelling; a write another writer made
// between the read and the rename is kept, apply re-running on the fresh
// document (writeWhileUnchanged). An absent file is an error naming it,
// never a file created, also when it is removed between the read and the
// rename; a file that keeps changing under every retry is an error asking to
// run the edit again.
func EditSettings(path string, apply func(document map[string]any) error) error {
	return editSettingsRereading(path, apply, os.ReadFile)
}

// editSettingsRereading is EditSettings; reread reads the file again before
// each rename.
func editSettingsRereading(
	path string,
	apply func(document map[string]any) error,
	reread func(string) ([]byte, error),
) error {
	raw, document, err := readSettingsForEdit(path)
	if err != nil {
		return err
	}
	if raw == nil {
		return fmt.Errorf("settings file %s does not exist", path)
	}
	if err := apply(document); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	resolved, mode, err := settingsEditTarget(path)
	if err != nil {
		return err
	}
	content, err := encodeSettingsDocument(document)
	if err != nil {
		return err
	}
	err = writeWhileUnchanged(path, resolved, mode, raw, content, reread, func(current []byte) ([]byte, bool, error) {
		if current == nil {
			return nil, false, fmt.Errorf("settings file %s was removed during the edit; nothing written", path)
		}
		var fresh map[string]any
		if len(bytes.TrimSpace(current)) > 0 {
			if err := unmarshalKeepingNumbers(current, &fresh); err != nil {
				return nil, false, fmt.Errorf("parse %s: %w", path, err)
			}
		}
		if fresh == nil {
			fresh = map[string]any{}
		}
		if err := apply(fresh); err != nil {
			return nil, false, fmt.Errorf("%s: %w", path, err)
		}
		next, err := encodeSettingsDocument(fresh)
		return next, true, err
	})
	var busy *settingsKeptChangingError
	if errors.As(err, &busy) {
		return fmt.Errorf(
			"%s changed under the edit %d times in a row; nothing written, run it again",
			path,
			busy.edits,
		)
	}
	return err
}
