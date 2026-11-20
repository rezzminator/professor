package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/deps"
)

// OptionalDepsRecord is what the last install found of the host's optional
// dependencies: the names it looked for and the ones present. pfm doctor
// compares a fresh lookup against it, so a dependency installed after pfm
// install (Herdr, uv) is a named row instead of a silently unwired surface.
type OptionalDepsRecord struct {
	Checked []string `json:"checked"`
	Present []string `json:"present"`
}

func optionalDepsPath(home string) string {
	return filepath.Join(managedRootForHome(home), "optional-deps.json")
}

// ProbeOptionalDeps looks up every optional dependency
// (deps.OptionalCommands) through runner. A lookup that failed for any reason but
// not-found is an error: it could not look, so it is neither present nor absent.
func ProbeOptionalDeps(runner deps.Runner) (OptionalDepsRecord, error) {
	record := OptionalDepsRecord{Checked: []string{}, Present: []string{}}
	var lookupErrs []error
	optional := deps.OptionalCommands()
	for index := range optional {
		entry := &optional[index]
		record.Checked = append(record.Checked, entry.Name)
		_, err := runner.LookPath(entry.Command)
		switch {
		case err == nil:
			record.Present = append(record.Present, entry.Name)
		case !errors.Is(err, exec.ErrNotFound):
			lookupErrs = append(lookupErrs, fmt.Errorf("look up %s: %w", entry.Name, err))
		}
	}
	return record, errors.Join(lookupErrs...)
}

// ReadOptionalDepsRecord reads the last install's record; a host no install
// recorded answers an error wrapping fs.ErrNotExist.
func ReadOptionalDepsRecord(home string) (OptionalDepsRecord, error) {
	path := optionalDepsPath(home)
	raw, err := os.ReadFile(path)
	if err != nil {
		return OptionalDepsRecord{}, fmt.Errorf("read %s: %w", path, err)
	}
	var record OptionalDepsRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return OptionalDepsRecord{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return record, nil
}

// OptionalDepsAppeared names each dependency present now that the record
// looked for and did not find. One the record never looked for (a registry
// row newer than the install) is not "appeared": nothing compared it.
func OptionalDepsAppeared(record, now OptionalDepsRecord) []string {
	var appeared []string
	for _, name := range now.Present {
		if slices.Contains(record.Checked, name) && !slices.Contains(record.Present, name) {
			appeared = append(appeared, name)
		}
	}
	return appeared
}

// writeOptionalDepsRecord records what this install found; a lookup that
// could not run leaves the previous record in place and says so.
func (installer *engine) writeOptionalDepsRecord() error {
	record, err := ProbeOptionalDeps(installer.processRunner())
	if err != nil {
		installer.skip("optional-deps record kept: " + err.Error())
		return nil
	}
	content, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode optional-deps record: %w", err)
	}
	content = append(content, '\n')
	path := optionalDepsPath(installer.options.Home)
	if sameFile(path, content, 0o600) {
		installer.ok(path)
		return nil
	}
	return installer.change("write "+path, func() error {
		return atomicfile.Write(path, content, 0o600)
	})
}
