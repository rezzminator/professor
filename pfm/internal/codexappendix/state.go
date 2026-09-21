package codexappendix

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

func receiptPath(account string) string {
	return filepath.Join(account, ".professor-appendix-trust.json")
}

func saveReceipt(account string, h hook) error {
	receipt := map[string]string{}
	raw, err := os.ReadFile(receiptPath(account))
	if err == nil {
		if err = json.Unmarshal(raw, &receipt); err != nil {
			return fmt.Errorf("parse appendix trust receipt: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if receipt == nil {
		return fmt.Errorf("appendix trust receipt must be an object")
	}
	receipt[h.Key] = h.CurrentHash
	raw, err = json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	return replaceFile(receiptPath(account), append(raw, '\n'))
}

// Unregister removes recorded trust without depending on native hook discovery,
// feature enablement, the original hook file, or an installed native executable.
func Unregister(account string) error {
	receiptRaw, err := os.ReadFile(receiptPath(account))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var receipt map[string]string
	if err = json.Unmarshal(receiptRaw, &receipt); err != nil {
		return fmt.Errorf("parse appendix trust receipt: %w", err)
	}
	path := filepath.Join(account, "config.toml")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.Remove(receiptPath(account))
	}
	if err != nil {
		return err
	}
	updated, err := removeRecordedTrust(string(raw), receipt)
	if err != nil {
		return err
	}
	if updated != string(raw) {
		physical, err := filepath.EvalSymlinks(path)
		if err != nil {
			return err
		}
		latest, readErr := os.ReadFile(physical)
		if readErr != nil || !bytes.Equal(latest, raw) {
			return fmt.Errorf("appendix config changed during cleanup; retry uninstall")
		}
		if err := replaceFile(physical, []byte(updated)); err != nil {
			return err
		}
	}
	return os.Remove(receiptPath(account))
}

func removeRecordedTrust(raw string, receipt map[string]string) (string, error) {
	var document map[string]any
	if _, err := toml.Decode(raw, &document); err != nil {
		return "", fmt.Errorf("parse appendix config cleanup: %w", err)
	}
	hooks, _ := document["hooks"].(map[string]any)
	states, _ := hooks["state"].(map[string]any)
	targets := map[string]bool{}
	for key, hash := range receipt {
		state, _ := states[key].(map[string]any)
		if state["trusted_hash"] == hash && len(state) <= 2 {
			targets[key] = true
		}
	}
	// Decode original prefixes to distinguish table declarations from bracket-like
	// text inside multiline values. Only whole owned declarations are removed.
	var result strings.Builder
	offset, start, previous := 0, 0, 0
	for _, line := range strings.SplitAfter(raw, "\n") {
		offset += len(line)
		var prefix map[string]any
		metadata, err := toml.Decode(raw[:offset], &prefix)
		if err != nil {
			continue
		}
		keys := metadata.Keys()
		owned := false
		if len(keys) > previous {
			key := keys[previous]
			owned = len(key) >= 3 && key[0] == "hooks" && key[1] == "state" && targets[key[2]]
		}
		if !owned {
			result.WriteString(raw[start:offset])
		}
		previous = len(keys)
		start = offset
	}
	result.WriteString(raw[start:])
	var checked map[string]any
	if _, err := toml.Decode(result.String(), &checked); err != nil {
		return "", fmt.Errorf("appendix trust cleanup requires manual removal from edited TOML layout: %w", err)
	}
	for key := range targets {
		delete(states, key)
	}
	// Native table removal can leave empty implicit parents. Preserve explicit
	// parent tables and compare only nonempty content.
	normalizeEmptyTables(document)
	normalizeEmptyTables(checked)
	if !reflect.DeepEqual(document, checked) {
		return "", fmt.Errorf("appendix trust cleanup cannot safely edit this TOML layout")
	}
	return result.String(), nil
}

func normalizeEmptyTables(value map[string]any) {
	for key, v := range value {
		if table, ok := v.(map[string]any); ok {
			normalizeEmptyTables(table)
			if len(table) == 0 {
				delete(value, key)
			}
		}
	}
}

func replaceFile(path string, raw []byte) error {
	mode := os.FileMode(0o600)
	existing, err := os.Stat(path)
	if err == nil {
		mode = existing.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return atomicfile.Write(path, raw, mode)
}
