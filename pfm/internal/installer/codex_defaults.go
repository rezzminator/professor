package installer

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

const (
	codexPolicyBegin                  = "<!-- BEGIN Professor subagent coordination -->"
	codexPolicyEnd                    = "<!-- END Professor subagent coordination -->"
	codexMinWaitTimeout               = "min_wait_timeout_ms"
	codexDefaultWaitTimeout           = "default_wait_timeout_ms"
	codexMaxWaitTimeout               = "max_wait_timeout_ms"
	codexCodeMode                     = "code_mode"
	codexDefaultExecYieldTime         = "default_exec_yield_time_ms"
	codexBackgroundTerminalMaxTimeout = "background_terminal_max_timeout"
	// codexLongYieldMs is the hour Codex holds a command wait open for. Without
	// it codex-cli clamps every wait at ~31s, so an agent asking for 600s polls
	// instead of waiting once.
	codexLongYieldMs = int64(3600000)
)

// wireCodexDefaults keeps mutable trust/model/MCP configuration local. Only
// missing settings come from the template; the retired global prompt block is
// removed, and the composed Codex fleet prompt is written into pfm's own
// developer_instructions fence — the channel Codex rebuilds verbatim after
// every compaction and hands to every default-role sub-agent.
func (installer *engine) wireCodexDefaults() error {
	defaults, err := installer.codexDefaultsSource()
	if err != nil {
		return err
	}
	prompt, err := codexHarnessPrompt()
	if err != nil {
		return err
	}
	for _, home := range installer.codexHomes() {
		path := filepath.Join(home, "config.toml")
		raw, existed, err := readCodexConfig(path)
		if err != nil {
			return err
		}
		wanted := string(raw)
		if defaults != "" {
			if wanted, err = mergeCodexDefaults(wanted, defaults); err != nil {
				return fmt.Errorf("merge Codex defaults into %s: %w", path, err)
			}
		}
		wanted, foreign, err := mergeCodexDeveloperInstructions(wanted, string(prompt))
		if err != nil {
			return fmt.Errorf("write the fleet prompt into %s: %w", path, err)
		}
		if foreign != "" {
			installer.skip(path + ": " + foreign)
		}
		if err := installer.writeCodexConfig(path, raw, existed, wanted, codexConfigEdit{
			change:  "merge Professor defaults into " + path,
			settled: path + " defaults",
		}); err != nil {
			return err
		}
	}
	return nil
}

// removeCodexDeveloperInstructions takes pfm's fleet-prompt block back out on
// uninstall. Nothing else in config.toml is touched: a foreign
// developer_instructions was never ours to remove.
func (installer *engine) removeCodexDeveloperInstructions() error {
	for _, home := range installer.codexHomes() {
		path := filepath.Join(home, "config.toml")
		raw, existed, err := readCodexConfig(path)
		if err != nil {
			return err
		}
		if !existed {
			continue
		}
		wanted := stripCodexInstructionsFence(string(raw))
		if err := installer.writeCodexConfig(path, raw, existed, wanted, codexConfigEdit{
			change:  "remove the fleet prompt from " + path,
			settled: path + " fleet prompt removed",
		}); err != nil {
			return err
		}
	}
	return nil
}

// codexDefaultsSource reads the blueprint clone's Codex defaults. An absent
// clone is named and returns an empty document, never an error: the defaults
// are an optional source, while everything else this file writes comes from
// the binary itself.
func (installer *engine) codexDefaultsSource() (string, error) {
	sourceRepo, err := installer.globalSourceRepoRoot()
	if err != nil {
		return "", err
	}
	source := filepath.Join(
		sourceRepo,
		"templates",
		"global",
		pfmengine.MustLookup(pfmengine.Codex).LongName,
		"config.toml",
	)
	defaults, err := os.ReadFile(source)
	if errors.Is(err, fs.ErrNotExist) {
		installer.skip("Codex defaults source absent at " + source)
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read Codex defaults %s: %w", source, err)
	}
	return string(defaults), nil
}

// readCodexConfig reads one account's config.toml. A missing file reads as
// absent; a dangling symlink and an unreadable file are errors, because
// neither is "this account has no config".
func readCodexConfig(path string) ([]byte, bool, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		return raw, true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, false, fmt.Errorf("config for Codex is a dangling symlink: %s", path)
		} else if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
			return nil, false, fmt.Errorf("inspect Codex config %s: %w", path, statErr)
		}
		return nil, false, nil
	}
	return nil, false, fmt.Errorf("read Codex config %s: %w", path, err)
}

// codexConfigEdit names one edit of a Codex config both ways: what the change
// line says when the file is rewritten, and what the ok line says when it was
// already as wanted. A settled edit that named the wrong operation would read
// as a step that ran.
type codexConfigEdit struct {
	change  string
	settled string
}

// writeCodexConfig replaces one account's config.toml with the planned text,
// refusing if the file moved between planning and applying, and backing up
// what it replaces.
func (installer *engine) writeCodexConfig(
	path string,
	raw []byte,
	existed bool,
	wanted string,
	edit codexConfigEdit,
) error {
	if wanted == string(raw) {
		installer.ok(edit.settled)
		return nil
	}
	return installer.change(edit.change, func() error {
		latest, readErr := os.ReadFile(path)
		if (existed && (readErr != nil || !bytes.Equal(latest, raw))) ||
			(!existed && !errors.Is(readErr, fs.ErrNotExist)) {
			return fmt.Errorf("config for Codex changed while planning install: %s", path)
		}
		if existed {
			// Atomic rename must not replace a user's config symlink.
			target, err := filepath.EvalSymlinks(path)
			if err != nil {
				return fmt.Errorf("resolve Codex config %s: %w", path, err)
			}
			path = target
			if err := copyBackup(path, availableBackup(path, installer.stamp)); err != nil {
				return err
			}
		}
		return atomicfile.Write(path, []byte(wanted), 0o600)
	})
}

// mergeCodexDefaults preserves comments and installer ownership fences. The
// TOML parser locates complete declarations, including multiline strings;
// the retired Professor developer block is removed and absent feature keys are inserted.
func mergeCodexDefaults(raw, defaults string) (string, error) {
	var config, source map[string]any
	if _, err := toml.Decode(raw, &config); err != nil {
		return "", fmt.Errorf("parse existing config: %w", err)
	}
	if _, err := toml.Decode(defaults, &source); err != nil {
		return "", fmt.Errorf("parse defaults: %w", err)
	}
	current, ok := config["developer_instructions"].(string)
	if !ok && config["developer_instructions"] != nil {
		return "", fmt.Errorf("developer_instructions must be a string")
	}
	if strings.Contains(current, codexPolicyBegin) || strings.Contains(current, codexPolicyEnd) {
		if strings.Count(current, codexPolicyBegin) != 1 || strings.Count(current, codexPolicyEnd) != 1 {
			return "", fmt.Errorf("ambiguous Professor instruction markers")
		}
		start, end := strings.Index(current, codexPolicyBegin), strings.Index(current, codexPolicyEnd)
		if end < start {
			return "", fmt.Errorf("reversed Professor instruction markers")
		}
		current = current[:start] + current[end+len(codexPolicyEnd):]
	}
	updated := raw
	if config["developer_instructions"] != nil && config["developer_instructions"] != current {
		value, err := encodeCodexValues(map[string]any{"developer_instructions": current})
		if err != nil {
			return "", err
		}
		if current == "" {
			value = ""
		}
		start, end, found := codexDeclaration(raw, []string{"developer_instructions"}, false)
		if found {
			// Preserve an inline comment only after the old value parses fully;
			// hashes inside quoted or multiline strings remain string content.
			for index := start; index < end; index++ {
				if raw[index] != '#' {
					continue
				}
				var prefix map[string]any
				if _, err := toml.Decode(raw[:index], &prefix); err == nil {
					value = strings.TrimSuffix(value, "\n") + " " + raw[index:end]
					break
				}
			}
			updated = raw[:start] + value + raw[end:]
		} else {
			updated = value + raw
		}
	}
	sourceFeatures, ok := source["features"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("defaults need a features table")
	}
	wanted, ok := sourceFeatures["multi_agent_v2"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("defaults need features.multi_agent_v2")
	}
	existing, err := codexFeatureTable(config, "multi_agent_v2")
	if err != nil {
		return "", err
	}
	// Existing preferences win, but a partial override must not produce a
	// configuration Codex rejects at startup.
	limits := map[string]int64{}
	for _, key := range []string{codexMinWaitTimeout, codexDefaultWaitTimeout, codexMaxWaitTimeout} {
		value, present := existing[key]
		if !present {
			value, present = wanted[key]
		}
		if !present {
			continue
		}
		number, ok := value.(int64)
		if !ok || number < 0 || number > 3600000 {
			return "", fmt.Errorf("%s must be an integer from 0 to 3600000", key)
		}
		limits[key] = number
	}
	for _, pair := range [][2]string{{codexMinWaitTimeout, codexDefaultWaitTimeout}, {codexDefaultWaitTimeout, codexMaxWaitTimeout}, {codexMinWaitTimeout, codexMaxWaitTimeout}} {
		lower, hasLower := limits[pair[0]]
		upper, hasUpper := limits[pair[1]]
		if hasLower && hasUpper && lower > upper {
			return "", fmt.Errorf(
				"wait defaults for Codex conflict with existing settings: %s exceeds %s; set a consistent minimum/default/maximum",
				pair[0],
				pair[1],
			)
		}
	}
	updated, err = insertMissingCodexFeature(updated, "multi_agent_v2", wanted, existing)
	if err != nil {
		return "", err
	}
	// Both long-yield keys are needed together: codex-cli clamps a command wait
	// at ~31s without them, so an agent asking for 600s polls instead of waiting.
	codeMode, err := codexFeatureTable(config, codexCodeMode)
	if err != nil {
		return "", err
	}
	updated, err = insertMissingCodexFeature(
		updated,
		codexCodeMode,
		map[string]any{codexDefaultExecYieldTime: codexLongYieldMs},
		codeMode,
	)
	if err != nil {
		return "", err
	}
	if _, present := config[codexBackgroundTerminalMaxTimeout]; !present {
		// A bare key appended after a table would land inside it, so it leads.
		value, err := encodeCodexValues(map[string]any{codexBackgroundTerminalMaxTimeout: codexLongYieldMs})
		if err != nil {
			return "", err
		}
		updated = value + updated
	}
	var checked map[string]any
	if _, err := toml.Decode(updated, &checked); err != nil {
		return "", fmt.Errorf(
			"defaults cannot be merged into this TOML layout without rewriting existing tables: %w",
			err,
		)
	}
	return updated, nil
}

// codexFeatureTable reads one feature sub-table from a parsed config. An absent
// table reads as empty; a feature declared as anything but a table is an error,
// never a silent empty, because the merge would then overwrite a real setting.
func codexFeatureTable(config map[string]any, name string) (map[string]any, error) {
	features, present := config["features"]
	if !present {
		return map[string]any{}, nil
	}
	featureTable, ok := features.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("features must be a table")
	}
	value, present := featureTable[name]
	if !present {
		return map[string]any{}, nil
	}
	table, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("features.%s must be a table", name)
	}
	return table, nil
}

// insertMissingCodexFeature adds only the keys the user has not set, into the
// existing [features.<name>] declaration when there is one and a fresh table at
// the end of the document otherwise.
func insertMissingCodexFeature(updated, name string, wanted, existing map[string]any) (string, error) {
	missing := map[string]any{}
	for key, value := range wanted {
		if _, exists := existing[key]; !exists {
			missing[key] = value
		}
	}
	if len(missing) == 0 {
		return updated, nil
	}
	values, err := encodeCodexValues(missing)
	if err != nil {
		return "", err
	}
	if _, end, found := codexDeclaration(updated, []string{"features", name}, true); found {
		return updated[:end] + "\n" + values + updated[end:], nil
	}
	return strings.TrimRight(updated, "\n") + "\n\n[features." + name + "]\n" + values, nil
}

func encodeCodexValues(values map[string]any) (string, error) {
	var buffer bytes.Buffer
	if err := toml.NewEncoder(&buffer).Encode(values); err != nil {
		return "", fmt.Errorf("encode Codex defaults: %w", err)
	}
	return buffer.String(), nil
}

// codexDeclaration uses successfully parsed prefixes as declaration boundaries.
// A bracket-looking line inside a multiline string is therefore never a table.
func codexDeclaration(raw string, key []string, table bool) (int, int, bool) {
	offset, start, previousKeys := 0, 0, 0
	for _, line := range strings.SplitAfter(raw, "\n") {
		offset += len(line)
		var document map[string]any
		metadata, err := toml.Decode(raw[:offset], &document)
		if err != nil {
			continue
		} // The whole document was validated by the caller.
		keys := metadata.Keys()
		declaration := strings.TrimSpace(raw[start:offset])
		if len(keys) > previousKeys {
			isTable := strings.HasPrefix(declaration, "[")
			if reflect.DeepEqual([]string(keys[previousKeys]), key) && isTable == table {
				return start, offset, true
			}
		}
		previousKeys = len(keys)
		start = offset
	}
	return 0, 0, false
}
