package config

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Extension is one machine-wide switch. No registry entry is enabled by default.
type Extension struct {
	Enabled bool              `json:"enabled"`
	Engines []string          `json:"engines"`
	Env     map[string]string `json:"env,omitempty"`
	Options map[string]any    `json:"options,omitempty"`
}

// ExtensionEngine contains install facts, not an extension-specific code path.
type ExtensionEngine struct {
	ID              string   `json:"id"`
	Source          string   `json:"source"`
	Marketplace     string   `json:"marketplace"`
	Package         string   `json:"package"`
	CachePath       []string `json:"cachePath"`
	MarketplaceArgs []string `json:"marketplaceArgs"`
	InstallArgs     []string `json:"installArgs"`
	InstallSuffix   []string `json:"installSuffix"`
	UninstallArgs   []string `json:"uninstallArgs"`
	UninstallSuffix []string `json:"uninstallSuffix"`
}

type ExtensionDefinition struct {
	Version string                     `json:"version"`
	Engines map[string]ExtensionEngine `json:"engines"`
}

//go:embed extensions.json
var extensionRegistryJSON []byte

// ExtensionRegistry returns a fresh copy so callers cannot mutate install policy.
func ExtensionRegistry() map[string]ExtensionDefinition {
	var registry map[string]ExtensionDefinition
	if err := json.Unmarshal(extensionRegistryJSON, &registry); err != nil {
		panic(fmt.Sprintf("embedded extension registry: %v", err))
	}
	return registry
}

func decodeExtensions(raw json.RawMessage) (map[string]Extension, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	entries, err := extensionObject(raw, "extensions")
	if err != nil {
		return nil, err
	}
	result := map[string]Extension{}
	registry := ExtensionRegistry()
	for _, name := range slices.Sorted(maps.Keys(entries)) {
		field := "extensions." + name
		definition, known := registry[name]
		if !known {
			return nil, fmt.Errorf("%s: unknown extension", field)
		}
		row, err := extensionObject(entries[name], field)
		if err != nil {
			return nil, err
		}
		for key := range row {
			if !slices.Contains([]string{"enabled", "engines", "env", "options"}, key) {
				return nil, fmt.Errorf("%s.%s: unknown field", field, key)
			}
		}
		var enabled *bool
		if err := json.Unmarshal(row["enabled"], &enabled); err != nil || enabled == nil {
			return nil, fmt.Errorf("%s.enabled must be a boolean", field)
		}
		var engines []string
		if err := json.Unmarshal(row["engines"], &engines); err != nil || engines == nil {
			return nil, fmt.Errorf("%s.engines must be an array of engine names", field)
		}
		for i, engine := range engines {
			if _, ok := definition.Engines[engine]; !ok {
				return nil, fmt.Errorf("%s.engines[%d]: unknown engine %q", field, i, engine)
			}
		}
		extension := Extension{Enabled: *enabled, Engines: engines}
		if envRaw, has := row["env"]; has {
			env, err := extensionObject(envRaw, field+".env")
			if err != nil {
				return nil, err
			}
			extension.Env = map[string]string{}
			prefix := ExtensionEnvPrefix(name)
			for key, value := range env {
				if !claudeEnvNamePattern.MatchString(key) || !strings.HasPrefix(key, prefix) {
					return nil, fmt.Errorf("%s.env.%s must have upper-snake prefix %s", field, key, prefix)
				}
				var text *string
				if err := json.Unmarshal(value, &text); err != nil || text == nil {
					return nil, fmt.Errorf("%s.env.%s must be a string", field, key)
				}
				extension.Env[key] = *text
			}
		}
		if options, has := row["options"]; has {
			if _, err := extensionObject(options, field+".options"); err != nil {
				return nil, err
			}
			decoder := json.NewDecoder(bytes.NewReader(options))
			decoder.UseNumber()
			if err := decoder.Decode(&extension.Options); err != nil {
				return nil, fmt.Errorf("%s.options: %w", field, err)
			}
		}
		result[name] = extension
	}
	return result, nil
}

func extensionObject(raw []byte, field string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if len(bytes.TrimSpace(raw)) == 0 || json.Unmarshal(raw, &object) != nil || object == nil {
		return nil, fmt.Errorf("%s must be an object", field)
	}
	return object, nil
}

func ExtensionEnvPrefix(name string) string {
	return strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_"
}

func (extension Extension) Active(engine string) bool {
	return extension.Enabled && slices.Contains(extension.Engines, engine)
}

// ExtensionEnv is the process environment overlay for one engine. Values from
// enabled extensions override inherited state; disabled extensions supply none.
func (config Config) ExtensionEnv(engine string) map[string]string {
	result := map[string]string{}
	for _, extension := range config.Extensions {
		if extension.Active(engine) {
			maps.Copy(result, extension.Env)
		}
	}
	return result
}

// ExtensionEnvironment overlays only configured keys of active extensions.
// Inactive engines and unconfigured names retain their inherited values.
func (config Config) ExtensionEnvironment(engine string, inherited []string) []string {
	env := config.ExtensionEnv(engine)
	result := make([]string, 0, len(inherited)+len(env))
	for _, entry := range inherited {
		key, _, _ := strings.Cut(entry, "=")
		if _, overridden := env[key]; !overridden {
			result = append(result, entry)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(env)) {
		result = append(result, key+"="+env[key])
	}
	return result
}

// ExtensionClaudePlugins composes the existing plugin list with extension
// switches. Extension ids are reserved: OFF overrides even an explicit list entry.
func (config Config) ExtensionClaudePlugins() []ClaudePlugin {
	result := slices.Clone(config.Claude.Plugins)
	for name, definition := range ExtensionRegistry() {
		extension, managed := config.Extensions[name]
		if !managed {
			continue
		}
		fact := definition.Engines[pfmengine.MustLookup(pfmengine.Claude).LongName]
		result = slices.DeleteFunc(result, func(p ClaudePlugin) bool { return p.ID == fact.ID })
		if extension.Active(pfmengine.MustLookup(pfmengine.Claude).LongName) {
			result = append(result, ClaudePlugin{ID: fact.ID, Source: fact.Source})
		}
	}
	return result
}

// CallmeterCostLedgerDirEnv is callmeter's own option naming the cost ledger
// it reads; callmeter reads none while it is unset.
const CallmeterCostLedgerDirEnv = "CALLMETER_COST_LEDGER_DIR"

// ExtensionClaudeEnv is claude.env with the extensions' env. While callmeter
// is installed it also hands callmeter pfm's cost ledger (paths.CostLedgerDir
// under home) through CallmeterCostLedgerDirEnv, unless claude.env names one.
func (config Config) ExtensionClaudeEnv(env paths.Env, home string) map[string]string {
	result := maps.Clone(config.Claude.Env)
	if result == nil {
		result = map[string]string{}
	}
	maps.Copy(result, config.ExtensionEnv(pfmengine.MustLookup(pfmengine.Claude).LongName))
	callmeter := slices.ContainsFunc(config.ExtensionClaudePlugins(), func(plugin ClaudePlugin) bool {
		return plugin.Name() == "callmeter"
	})
	if _, named := result[CallmeterCostLedgerDirEnv]; callmeter && !named {
		// An unresolved home would hand callmeter a path relative to its cwd.
		if dir := paths.CostLedgerDir(env, home); filepath.IsAbs(dir) {
			result[CallmeterCostLedgerDirEnv] = dir
		}
	}
	return result
}

// ReportExtensions prints the effective optional block with its config source.
// Values are JSON-quoted and redacted before printing, including env/options.
func (config Config) ReportExtensions(out io.Writer) {
	source := config.Source("extensions")
	if len(config.Extensions) == 0 {
		fmt.Fprintf(out, "config extensions=none (%s)\n", source)
		return
	}
	for _, name := range slices.Sorted(maps.Keys(config.Extensions)) {
		extension := config.Extensions[name]
		prefix := "extensions." + name
		fmt.Fprintf(out, "config %s.enabled=%t (%s)\n", prefix, extension.Enabled, source)
		fmt.Fprintf(out, "config %s.engines=%s (%s)\n", prefix, strings.Join(extension.Engines, ","), source)
		for _, field := range []struct {
			name  string
			value any
		}{{"env", extension.Env}, {"options", extension.Options}} {
			raw, err := json.Marshal(map[string]any{field.name: field.value})
			if err != nil {
				fmt.Fprintf(out, "config %s.%s error=%v\n", prefix, field.name, err)
				continue
			}
			fmt.Fprintf(out, "config %s.%s=%s (%s)\n", prefix, field.name, RedactSecrets(raw), source)
		}
	}
}
