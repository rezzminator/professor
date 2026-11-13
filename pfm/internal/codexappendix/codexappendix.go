// Package codexappendix records the Codex trust of the Codex hooks pfm owns
// (resume-unkill, and a plugin's hooks), each under its own receipt, and
// removes exactly the trust its receipt names.
package codexappendix

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// processRunner starts the app-server child (trust.go) when the context
// carries no runner (WithAppServerRunner); nil is the real process table.
var processRunner deps.Runner

type runnerKey struct{}

// WithAppServerRunner returns ctx carrying the runner that starts the app-server child
// of every trust call made with it; the installer hands its process runner
// through here.
func WithAppServerRunner(ctx context.Context, runner deps.Runner) context.Context {
	return context.WithValue(ctx, runnerKey{}, runner)
}

func appServerRunner(ctx context.Context) deps.Runner {
	if runner, ok := ctx.Value(runnerKey{}).(deps.Runner); ok && runner != nil {
		return runner
	}
	if processRunner != nil {
		return processRunner
	}
	return obs.Runner(deps.RealRunner{})
}

func receiptRecorded(receipt string) bool {
	_, err := os.Stat(receipt)
	return !errors.Is(err, os.ErrNotExist)
}

// unregisterTrust removes the hooks.state tables the receipt at receiptFile
// names from the account's config.toml, then the receipt itself.
func unregisterTrust(receiptFile, account string) error {
	receiptRaw, err := os.ReadFile(receiptFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read trust receipt %s: %w", receiptFile, err)
	}
	var receipt map[string]string
	if err = json.Unmarshal(receiptRaw, &receipt); err != nil {
		return fmt.Errorf("parse trust receipt %s: %w", receiptFile, err)
	}
	path := filepath.Join(account, "config.toml")
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return removeReceipt(receiptFile)
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	updated, err := removeRecordedTrust(string(raw), receipt)
	if err != nil {
		return fmt.Errorf("clean %s: %w", path, err)
	}
	if updated != string(raw) {
		physical, err := filepath.EvalSymlinks(path)
		if err != nil {
			return fmt.Errorf("resolve %s: %w", path, err)
		}
		latest, readErr := os.ReadFile(physical)
		if readErr != nil || !bytes.Equal(latest, raw) {
			return fmt.Errorf("%s changed during trust cleanup; retry uninstall", path)
		}
		if err := replaceFile(physical, []byte(updated)); err != nil {
			return fmt.Errorf("write %s: %w", physical, err)
		}
	}
	return removeReceipt(receiptFile)
}

func removeReceipt(receiptFile string) error {
	if err := os.Remove(receiptFile); err != nil {
		return fmt.Errorf("remove trust receipt %s: %w", receiptFile, err)
	}
	return nil
}

func removeRecordedTrust(raw string, receipt map[string]string) (string, error) {
	var document map[string]any
	if _, err := toml.Decode(raw, &document); err != nil {
		return "", fmt.Errorf("parse config cleanup: %w", err)
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
		return "", fmt.Errorf("trust cleanup requires manual removal from edited TOML layout: %w", err)
	}
	for key := range targets {
		delete(states, key)
	}
	// Native table removal can leave empty implicit parents. Preserve explicit
	// parent tables and compare only nonempty content.
	normalizeEmptyTables(document)
	normalizeEmptyTables(checked)
	if !reflect.DeepEqual(document, checked) {
		return "", fmt.Errorf("trust cleanup cannot safely edit this TOML layout")
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
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if err := atomicfile.Write(path, raw, mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
