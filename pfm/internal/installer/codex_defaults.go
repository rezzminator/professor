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

	"hostops/pfm/internal/atomicfile"
	pfmengine "hostops/pfm/internal/engine"
)

const (
	codexPolicyBegin = "<!-- BEGIN Professor subagent coordination -->"
	codexPolicyEnd   = "<!-- END Professor subagent coordination -->"
)

// wireCodexDefaults keeps mutable trust/model/MCP configuration local. Only
// missing settings come from the template; the retired global prompt block is removed.
func (installer *engine) wireCodexDefaults() error {
	sourceRepo, err := installer.globalSourceRepoRoot()
	if err != nil {
		return err
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
		return nil
	}
	if err != nil {
		return fmt.Errorf("read Codex defaults %s: %w", source, err)
	}
	for _, home := range installer.codexHomes() {
		path := filepath.Join(home, "config.toml")
		raw, err := os.ReadFile(path)
		existed := err == nil
		if errors.Is(err, fs.ErrNotExist) {
			if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("Codex config is a dangling symlink: %s", path)
			} else if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
				return fmt.Errorf("inspect Codex config %s: %w", path, statErr)
			}
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("read Codex config %s: %w", path, err)
		}
		wanted, err := mergeCodexDefaults(string(raw), string(defaults))
		if err != nil {
			return fmt.Errorf("merge Codex defaults into %s: %w", path, err)
		}
		if wanted == string(raw) {
			installer.ok(path + " defaults")
			continue
		}
		if err := installer.change("merge Professor defaults into "+path, func() error {
			latest, readErr := os.ReadFile(path)
			if (existed && (readErr != nil || !bytes.Equal(latest, raw))) ||
				(!existed && !errors.Is(readErr, fs.ErrNotExist)) {
				return fmt.Errorf("Codex config changed while planning install: %s", path)
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
		}); err != nil {
			return err
		}
	}
	return nil
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
	existing := map[string]any{}
	if features, present := config["features"]; present {
		featureTable, ok := features.(map[string]any)
		if !ok {
			return "", fmt.Errorf("features must be a table")
		}
		if value, present := featureTable["multi_agent_v2"]; present {
			existing, ok = value.(map[string]any)
			if !ok {
				return "", fmt.Errorf("features.multi_agent_v2 must be a table")
			}
		}
	}
	// Existing preferences win, but a partial override must not produce a
	// configuration Codex rejects at startup.
	limits := map[string]int64{}
	for _, key := range []string{"min_wait_timeout_ms", "default_wait_timeout_ms", "max_wait_timeout_ms"} {
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
	for _, pair := range [][2]string{{"min_wait_timeout_ms", "default_wait_timeout_ms"}, {"default_wait_timeout_ms", "max_wait_timeout_ms"}, {"min_wait_timeout_ms", "max_wait_timeout_ms"}} {
		lower, hasLower := limits[pair[0]]
		upper, hasUpper := limits[pair[1]]
		if hasLower && hasUpper && lower > upper {
			return "", fmt.Errorf(
				"Codex wait defaults conflict with existing settings: %s exceeds %s; set a consistent minimum/default/maximum",
				pair[0],
				pair[1],
			)
		}
	}
	missing := map[string]any{}
	for key, value := range wanted {
		if _, exists := existing[key]; !exists {
			missing[key] = value
		}
	}
	if len(missing) > 0 {
		values, err := encodeCodexValues(missing)
		if err != nil {
			return "", err
		}
		_, end, found := codexDeclaration(updated, []string{"features", "multi_agent_v2"}, true)
		if found {
			updated = updated[:end] + "\n" + values + updated[end:]
		} else {
			updated = strings.TrimRight(updated, "\n") + "\n\n[features.multi_agent_v2]\n" + values
		}
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
