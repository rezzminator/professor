package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
)

// wireClaudeRegistryDefaults writes every registry value of the launch defaults
// file (claudelaunch.RegistryDefaults), preferences Claude Code keeps in an
// account's .claude.json where no --settings reaches, into the registry of
// every Claude account, once per physical file, under Claude's own lock and
// through a symlinked registry to the file it names. Every other byte of the
// registry is kept; a registry already holding every value is not written, and
// an account without a registry yet gets its values on the next install after
// Claude creates one.
func (installer *engine) wireClaudeRegistryDefaults() error {
	seen := map[string]bool{}
	var failures []error
	for _, account := range installer.options.ClaudeAccounts {
		path := ClaudeRegistryFor(installer.options.Home, account)
		physical := physicalSettingsPath(path)
		if seen[physical] {
			continue
		}
		seen[physical] = true
		raw, info, err := readRegularFile(physical)
		if errors.Is(err, fs.ErrNotExist) {
			installer.ok("claude registry defaults: no registry yet in " + path)
			continue
		}
		if err != nil {
			failures = append(failures, installer.fail(fmt.Errorf(
				"claude registry defaults in %s: registry unreadable, state unknown: %w", path, err,
			)))
			continue
		}
		var document map[string]json.RawMessage
		if err := json.Unmarshal(raw, &document); err != nil {
			failures = append(failures, installer.fail(fmt.Errorf(
				"claude registry defaults in %s: registry unparsable, state unknown: %w", path, err,
			)))
			continue
		}
		wanted := raw
		var changed []string
		for _, preference := range claudelaunch.RegistryDefaults() {
			var current bytes.Buffer
			if encoded, present := document[preference.Key]; present && json.Compact(&current, encoded) == nil &&
				bytes.Equal(current.Bytes(), preference.Value) {
				continue
			}
			if wanted, err = setTopLevelKey(wanted, preference.Key, preference.Value); err != nil {
				break
			}
			changed = append(changed, fmt.Sprintf("%s=%s (%s)", preference.Key, preference.Value, preference.Reason))
		}
		if err != nil {
			failures = append(failures, installer.fail(fmt.Errorf("claude registry defaults in %s: %w", path, err)))
			continue
		}
		if len(changed) == 0 {
			installer.ok("claude registry defaults set in " + path)
			continue
		}
		if err := installer.change(
			fmt.Sprintf("set Claude registry defaults %v in %s", changed, physical),
			func() error {
				locks := installer.claudeMCPLockAliases(path, physical)
				return withClaudeMCPLocks(locks, func(check func() error) error {
					return installer.writeRegistryFileIf(path, raw, wanted, true, info.Mode().Perm(), check)
				})
			},
		); err != nil {
			failures = append(failures, installer.fail(fmt.Errorf(
				"set claude registry defaults in %s: %w", physical, err,
			)))
		}
	}
	return errors.Join(failures...)
}
