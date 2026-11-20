package installer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/themesync"
)

// UnownedShippedThemes names each theme the source clone ships whose Claude
// theme file or profile copy exists without an ownership record: install
// skipped it as the user's work, so no profile copy is recorded for it and no
// VS Code colour theme is rendered from it. A clone with no theme listing
// returns an error wrapping fs.ErrNotExist: nothing was compared.
func UnownedShippedThemes(home string) ([]string, error) {
	repo, err := paths.GlobalSourceRepo(home)
	if err != nil {
		return nil, fmt.Errorf("resolve source clone: %w", err)
	}
	names, _, err := localProfileDirs(filepath.Join(repo, themeProfilesRelative))
	if err != nil {
		return nil, err
	}
	ledgerPath := filepath.Join(ManagedRoot(home), themeOwnershipName)
	ownership, err := readThemeOwnership(ledgerPath)
	if err != nil {
		return nil, fmt.Errorf("read ownership %s: %w", ledgerPath, err)
	}
	slices.Sort(names)
	var unowned []string
	for _, name := range names {
		files := []themeFile{
			{key: name, path: filepath.Join(home, ".claude", "themes", name+".json")},
			{key: profileLedgerKey(name), path: themesync.ProfilePath(home, name)},
		}
		for index := range files {
			file := &files[index]
			_, exists, err := readOptionalRegularFile(file.path)
			if err != nil {
				return nil, fmt.Errorf("inspect theme %s: %w", name, err)
			}
			if _, owned := ownership[file.key]; exists && !owned {
				unowned = append(unowned, name)
				break
			}
		}
	}
	return unowned, nil
}

// settle decides an inspected file's action, or returns the skip line for an
// unowned file that differs from what install would write.
func (file *themeFile) settle(name string) string {
	switch {
	case !file.exists:
		file.action = themeWrite
	case bytes.Equal(file.existing, file.content) && file.owned:
		file.action = themeUnchanged
	case bytes.Equal(file.existing, file.content):
		file.action = themeAdopt
	case !file.owned && themeStaleCopy(file.existing, file.shipped):
		file.action, file.stale = themeWrite, true
	case !file.owned:
		return "theme " + name + file.label + " already exists but is not installer-owned; left in place at " + file.path
	default:
		file.action = themeWrite
	}
	return ""
}

// writeLine is the change line for a file install writes, naming a stale
// copy it adopts so the overwrite never reads as an ordinary update.
func (file *themeFile) writeLine(name string) string {
	line := "write theme " + name + file.label + " -> " + file.path
	if file.stale {
		line += " (adopted a stale copy: every key it held matches the shipped profile)"
	}
	return line
}

// themeStaleCopy reports whether existing is a stale copy of the shipped
// profile rather than the user's work: a JSON object every key of which,
// nested objects included, holds the shipped profile's value. A key the
// profile has since added is no edit; a changed value or an extra key is.
func themeStaleCopy(existing, shipped []byte) bool {
	var held, want any
	if json.Unmarshal(existing, &held) != nil || json.Unmarshal(shipped, &want) != nil {
		return false
	}
	if _, object := held.(map[string]any); !object {
		return false
	}
	return themeJSONSubset(held, want)
}

func themeJSONSubset(held, want any) bool {
	heldObject, heldIsObject := held.(map[string]any)
	wantObject, wantIsObject := want.(map[string]any)
	if !heldIsObject || !wantIsObject {
		return reflect.DeepEqual(held, want)
	}
	for key, value := range heldObject {
		shipped, present := wantObject[key]
		if !present || !themeJSONSubset(value, shipped) {
			return false
		}
	}
	return true
}
