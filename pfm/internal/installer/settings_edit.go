package installer

import (
	"fmt"
)

// EditSettings applies apply to the existing settings.json at path and
// publishes the result by atomic rename through its symlink, keeping the
// file's mode and every number's spelling; a write another writer made
// between the read and the rename is kept, apply re-running on the fresh
// document (writeSettingsDocument). An absent file is an error naming it,
// never a file created.
func EditSettings(path string, apply func(document map[string]any) error) error {
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
	return writeSettingsDocument(path, raw, document, apply)()
}
