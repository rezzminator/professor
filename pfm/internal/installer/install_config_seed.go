package installer

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// seedNewClaudeKeys are the claude keys added since v0.79.0, which a pfm of
// that release refuses as unknown (config.marshalClaude).
var seedNewClaudeKeys = []string{"remoteControl", "pluginCheckoutRoot", "pluginSource", "plugins", "costLedger"}

// seedWithoutDefaultNewKeys is the seed a first install writes: the example's
// bytes, less each key of seedNewClaudeKeys that config.Marshal would leave out
// at its default, less each scope key (envs, harnessPrompt, claude.settings)
// that changes no launch, less installPing at its default true, and less an unset herdr block. A fresh config then rolls back to the last release as
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
	removed := false
	if _, present := document[pfmconfig.KeyEnvs]; present && len(loaded.Envs) == 0 {
		delete(document, pfmconfig.KeyEnvs)
		removed = true
	}
	if _, present := document[pfmconfig.KeyHarnessPrompt]; present && loaded.HarnessPrompt == nil {
		delete(document, pfmconfig.KeyHarnessPrompt)
		removed = true
	}
	// installPing on is the default, and a pfm before the key refuses it.
	if _, present := document[pfmconfig.KeyInstallPing]; present && loaded.InstallPing {
		delete(document, pfmconfig.KeyInstallPing)
		removed = true
	}
	// An unset herdr.keys is pfm's own keymap, and a pfm before it refuses the herdr block.
	if _, present := document[pfmconfig.KeyHerdr]; present && loaded.Herdr.Keys == nil {
		delete(document, pfmconfig.KeyHerdr)
		removed = true
	}
	raw, found := document[claudeKey]
	if found {
		var claude map[string]json.RawMessage
		if err := json.Unmarshal(raw, &claude); err != nil {
			return nil, fmt.Errorf("read the seed claude block: %w", err)
		}
		claudeRemoved := false
		for _, key := range seedNewClaudeKeys {
			if _, kept := emittedClaude[key]; kept {
				continue
			}
			if _, present := claude[key]; present {
				delete(claude, key)
				claudeRemoved = true
			}
		}
		// claude.settings that layers nothing over the shipped launch defaults changes no launch; the
		// two compare as JSON, since a decoded file and the defaults hold different Go types.
		layered, err := json.Marshal(claudelaunch.EffectiveSettings(loaded.Claude.Settings))
		if err != nil {
			return nil, fmt.Errorf("encode the seed claude.settings over the launch defaults: %w", err)
		}
		defaults, err := json.Marshal(claudelaunch.EffectiveSettings(nil))
		if err != nil {
			return nil, fmt.Errorf("encode the launch defaults: %w", err)
		}
		if _, present := claude["settings"]; present && bytes.Equal(layered, defaults) {
			delete(claude, "settings")
			claudeRemoved = true
		}
		if claudeRemoved {
			if document[claudeKey], err = marshalSeedInOrder(raw, claude); err != nil {
				return nil, fmt.Errorf("encode the seed claude block: %w", err)
			}
			removed = true
		}
	}
	if !removed {
		return loaded.SeedContent, nil
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
