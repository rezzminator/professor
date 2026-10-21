package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// InstallConfig loads the target config or plans a first install from the clone's example.
// It returns the seed path without writing the target.
func InstallConfig(
	runtime pfmconfig.Runtime,
	_ paths.Env,
	installClone string,
) (config pfmconfig.Config, seeded string, err error) {
	values, target := runtime.Paths, runtime.Config.Path
	if target == "" {
		return runtime.Config, "", nil
	}
	load := func() (pfmconfig.Config, error) {
		loaded, loadErr := pfmconfig.Load(
			target,
			values.Home,
			values.Roots[pfmengine.Claude],
			values.FirstRoot(pfmengine.Codex),
		)
		if loadErr != nil {
			return pfmconfig.Config{}, fmt.Errorf("load install config %s: %w", target, loadErr)
		}
		return loaded, nil
	}
	if _, statErr := os.Stat(target); statErr == nil {
		config, err = load()
		return config, "", err
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return config, "", fmt.Errorf("inspect install config %s: %w", target, statErr)
	}
	if runtime.ConfigExplicit {
		return runtime.Config, "", nil
	}
	clone, markerErr := paths.ReadSourceRepoMarker(values.Home)
	if markerErr != nil && !errors.Is(markerErr, paths.ErrNoSourceRepoMarker) {
		return config, "", fmt.Errorf("read source repo marker: %w", markerErr)
	}
	if errors.Is(markerErr, paths.ErrNoSourceRepoMarker) && installClone != "" {
		absolute, absErr := filepath.Abs(installClone)
		if absErr != nil {
			return config, "", fmt.Errorf("resolve install clone %q: %w", installClone, absErr)
		}
		info, statErr := os.Stat(absolute)
		if statErr != nil {
			return config, "", fmt.Errorf("resolve install clone %q: %w", installClone, statErr)
		}
		if !info.IsDir() {
			return config, "", fmt.Errorf("resolve install clone %q: %w", installClone, errors.New("not a directory"))
		}
		clone = paths.PhysicalPath(absolute)
	}
	if clone == "" {
		return runtime.Config, "", nil
	}
	example := filepath.Join(clone, "example.pfm.config.json")
	_, readErr := os.ReadFile(example)
	if errors.Is(readErr, fs.ErrNotExist) {
		return runtime.Config, "", nil
	}
	if readErr != nil {
		return config, "", fmt.Errorf("read install example %s: %w", example, readErr)
	}
	config, err = pfmconfig.LoadSeed(
		example,
		target,
		values.Home,
		values.Roots[pfmengine.Claude],
		values.FirstRoot(pfmengine.Codex),
	)
	if err != nil {
		return config, example, err
	}
	config.SeedContent, err = seedWithoutDefaultNewKeys(config)
	return config, example, err
}

// seedNewClaudeKeys are the claude keys added since v0.79.0, which a pfm of
// that release refuses as unknown (config.marshalClaude).
var seedNewClaudeKeys = []string{"remoteControl", "helperModel", "pluginCheckoutRoot", "plugins", "costLedger"}

// seedWithoutDefaultNewKeys is the seed a first install writes: the example's
// bytes, less each key of seedNewClaudeKeys that config.Marshal would leave out
// at its default. A fresh config then rolls back to the last release as
// `pfm config init`'s does; a key the example sets away from its default stays.
// The probe marshals the loaded config as if the file had not set those keys,
// so the omission rule stays marshalClaude's alone.
func seedWithoutDefaultNewKeys(loaded pfmconfig.Config) ([]byte, error) {
	probe := loaded
	probe.Sources = make(map[string]pfmconfig.Source, len(loaded.Sources))
	for key, source := range loaded.Sources {
		probe.Sources[key] = source
	}
	for _, key := range seedNewClaudeKeys {
		delete(probe.Sources, "claude."+key)
	}
	marshaled, err := pfmconfig.Marshal(probe, false)
	if err != nil {
		return nil, fmt.Errorf("marshal the seed config for its default claude keys: %w", err)
	}
	claudeKey := pfmengine.MustLookup(pfmengine.Claude).LongName
	var emitted map[string]json.RawMessage
	if err := json.Unmarshal(marshaled, &emitted); err != nil {
		return nil, fmt.Errorf("read the marshaled seed config: %w", err)
	}
	var emittedClaude map[string]json.RawMessage
	if err := json.Unmarshal(emitted[claudeKey], &emittedClaude); err != nil {
		return nil, fmt.Errorf("read the marshaled seed claude block: %w", err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(loaded.SeedContent, &document); err != nil {
		return nil, fmt.Errorf("read the seed config bytes: %w", err)
	}
	var claude map[string]json.RawMessage
	raw, found := document[claudeKey]
	if !found {
		return loaded.SeedContent, nil
	}
	if err := json.Unmarshal(raw, &claude); err != nil {
		return nil, fmt.Errorf("read the seed claude block: %w", err)
	}
	removed := false
	for _, key := range seedNewClaudeKeys {
		if _, kept := emittedClaude[key]; kept {
			continue
		}
		if _, present := claude[key]; present {
			delete(claude, key)
			removed = true
		}
	}
	if !removed {
		return loaded.SeedContent, nil
	}
	if document[claudeKey], err = marshalSeedInOrder(raw, claude); err != nil {
		return nil, fmt.Errorf("encode the seed claude block: %w", err)
	}
	seed, err := marshalSeedInOrder(loaded.SeedContent, document)
	if err != nil {
		return nil, fmt.Errorf("encode the seed config: %w", err)
	}
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, seed, "", "  "); err != nil {
		return nil, fmt.Errorf("format the seed config: %w", err)
	}
	return append(formatted.Bytes(), '\n'), nil
}

func (installer *engine) seedConfig() error {
	if installer.options.ConfigSeed == "" {
		return nil
	}
	example, target := installer.options.ConfigSeed, installer.options.MCPConfigPath
	return installer.change("seed "+target+" from "+example, func() error {
		content := installer.options.ConfigSeedContent
		if content == nil {
			return fmt.Errorf("seed install config %s: validated example bytes are missing", target)
		}
		if err := atomicfile.Create(target, content, 0o600); err != nil {
			return fmt.Errorf("write install config %s: %w", target, err)
		}
		return nil
	})
}

// marshalSeedInOrder emits the retained fields in the validated seed's order.
func marshalSeedInOrder(raw []byte, fields map[string]json.RawMessage) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("read seed object: %w", err)
	}
	var output bytes.Buffer
	output.WriteByte('{')
	first := true
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("read seed key: %w", err)
		}
		key, ok := token.(string)
		if !ok {
			return nil, fmt.Errorf("seed object key is not a string: %v", token)
		}
		var original json.RawMessage
		if err := decoder.Decode(&original); err != nil {
			return nil, fmt.Errorf("read seed value %s: %w", key, err)
		}
		value, keep := fields[key]
		if !keep {
			continue
		}
		encoded, err := json.Marshal(key)
		if err != nil {
			return nil, fmt.Errorf("encode seed key %s: %w", key, err)
		}
		if !first {
			output.WriteByte(',')
		}
		first = false
		output.Write(encoded)
		output.WriteByte(':')
		output.Write(value)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("close seed object: %w", err)
	}
	output.WriteByte('}')
	return output.Bytes(), nil
}
