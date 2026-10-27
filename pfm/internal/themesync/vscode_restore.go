package themesync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// VSCodeRestore names extension-owned settings that only VS Code can restore.
// Record retains the prior values; Target is Global or the full workspace identity.
type VSCodeRestore struct {
	Record string
	Target string
	Keys   []string
}

// VSCodePendingRestores reads both ownership records, even without state.json or
// a VS Code installation. Missing settings/windows are not proof of restoration.
// It never edits recovery data, including when uninstall deliberately leaves colours.
func VSCodePendingRestores(stateDir string) ([]VSCodeRestore, error) {
	var pending []VSCodeRestore
	for _, name := range []string{"vscode-applied.json", vscodeChromeRecordFile} {
		recordPath := filepath.Join(stateDir, name)
		raw, err := os.ReadFile(recordPath)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read VS Code recovery record %s: %w", recordPath, err)
		}
		records, err := vscodeRecordObject(raw)
		if err != nil {
			return nil, fmt.Errorf("parse VS Code recovery record %s: %w", recordPath, err)
		}
		if name == "vscode-applied.json" {
			records = map[string]json.RawMessage{"Global user settings": raw}
		}
		var targets []string
		for target := range records {
			targets = append(targets, target)
		}
		sort.Strings(targets)
		for _, target := range targets {
			record, err := vscodeRecordObject(records[target])
			if err != nil {
				return nil, fmt.Errorf("parse %s target %s: %w", recordPath, target, err)
			}
			owned, err := vscodeRecordObject(record["keys"])
			if err != nil {
				return nil, fmt.Errorf("parse %s target %s keys: %w", recordPath, target, err)
			}
			keys, err := vscodeOwnedKeys(owned)
			if err != nil {
				return nil, fmt.Errorf("parse %s target %s keys: %w", recordPath, target, err)
			}
			if len(keys) > 0 {
				pending = append(pending, VSCodeRestore{Record: recordPath, Target: target, Keys: keys})
			}
		}
	}
	return pending, nil
}

func vscodeRecordObject(raw []byte) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	if object == nil {
		return nil, errors.New("expected an ownership object, got null")
	}
	return object, nil
}

func vscodeOwnedKeys(record map[string]json.RawMessage) ([]string, error) {
	var keys []string
	for key, raw := range record {
		entry, err := vscodeRecordObject(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		if strings.HasPrefix(key, "[") {
			scoped, err := vscodeRecordObject(entry["keys"])
			if err != nil {
				return nil, fmt.Errorf("%s scoped keys: %w", key, err)
			}
			nested, err := vscodeOwnedKeys(scoped)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			for _, child := range nested {
				keys = append(keys, key+"."+child)
			}
		} else {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}
