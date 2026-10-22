package installer

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/rezzminator/professor/pfm/internal/codexappendix"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// ExtensionCheck is one independently named extension drift check.
type ExtensionCheck struct{ Extension, Engine, Target, Aspect, Problem string }

// ConfigureExtensions composes extension switches with the existing Claude
// plugin/env steps. A managed extension id is reserved even when OFF.
func ConfigureExtensions(options *Options, machine pfmconfig.Config) {
	options.Extensions = machine.Extensions
	options.ClaudePlugins = machine.ExtensionClaudePlugins()
	options.ClaudeEnv = machine.ExtensionClaudeEnv()
	options.ClaudePluginsDefault = machine.Source(pfmconfig.KeyClaudePlugins) == pfmconfig.SourceDefault
}

func ExtensionChecks(ctx context.Context, root string, options Options) ([]ExtensionCheck, error) {
	ledger, err := readExtensionOwnership(root)
	if err != nil {
		return nil, err
	}
	var result []ExtensionCheck
	targets := extensionTargets(options, ledger)
	for index := range targets {
		target := &targets[index]
		extension := options.Extensions[target.name]
		active := extension.Active(target.engine)
		record, owned := ledger.Records[target.key()]
		_, document, readErr := readExtensionDocument(target.path, target.engine)
		if readErr == nil && target.engine == extensionOpenCode {
			_, readErr = extensionPackageEntries(document)
		}
		aspects := []string{"installed", extensionEnabled, extensionOptions, configEnvKey}
		if target.engine == extensionCodex {
			aspects = append(aspects, "hook trust")
		}
		for _, aspect := range aspects {
			check := ExtensionCheck{Extension: target.name, Engine: target.engine, Target: target.path, Aspect: aspect}
			if readErr != nil {
				check.Problem = "state unreadable: " + readErr.Error()
				result = append(result, check)
				continue
			}
			switch aspect {
			case "installed":
				if active && target.engine != extensionOpenCode {
					installed, err := extensionInstalled(*target)
					if err != nil {
						check.Problem = "installed state unreadable: " + err.Error()
					} else if !installed {
						check.Problem = "not installed"
					}
				}
				if active && target.engine == extensionOpenCode &&
					!extensionPackagePresent(document, target.fact.Package) {
					check.Problem = "npm plugin not registered"
				}
			case extensionEnabled:
				if !active && !owned && !slices.Contains(extension.Engines, target.engine) {
					break
				}
				if target.engine == extensionOpenCode {
					if extensionPackagePresent(document, target.fact.Package) != active {
						check.Problem = "plugin array membership differs from extension switch"
					}
				} else {
					path := []string{extensionEnabledPlugins, target.fact.ID}
					if target.engine == extensionCodex {
						path = []string{extensionPlugins, target.fact.ID, extensionEnabled}
					}
					value, has, err := extensionLookup(document, path)
					if err != nil {
						check.Problem = err.Error()
					} else if !has || value != active {
						check.Problem = fmt.Sprintf("enabled is %v (present=%t), want %t", value, has, active)
					}
					if active && target.engine == extensionCodex {
						_, has, err := extensionLookup(
							document,
							[]string{extensionMarketplaces, target.fact.Marketplace},
						)
						if err != nil {
							check.Problem = err.Error()
						} else if !has {
							check.Problem = "marketplace configuration missing"
						}
					}
				}
			case extensionOptions:
				if active && target.engine == extensionClaude {
					for _, value := range record.Values {
						if len(value.Path) != 4 || value.Path[0] != extensionPluginConfigs ||
							value.Path[1] != target.fact.ID || value.Path[2] != extensionOptions {
							continue
						}
						if _, wanted := extension.Options[value.Path[3]]; wanted {
							continue
						}
						current, has, err := extensionLookup(document, value.Path)
						if err != nil {
							check.Problem = err.Error()
						} else if has && extensionEqual(current, value.Last) {
							check.Problem = "removed option " + value.Path[3] + " awaits restoration"
						}
					}
					for key, want := range extension.Options {
						value, has, err := extensionLookup(
							document,
							[]string{extensionPluginConfigs, target.fact.ID, extensionOptions, key},
						)
						if err != nil {
							check.Problem = err.Error()
							break
						}
						if !has || !extensionEqual(value, want) {
							check.Problem = "option " + key + " differs from extensions config"
							break
						}
					}
				}
			case configEnvKey:
				want := map[string]string{}
				if active {
					want = extension.Env
				}
				got := record.Env
				if len(got) == 0 {
					got = map[string]string{}
				}
				if len(want) == 0 {
					want = map[string]string{}
				}
				if !extensionEqual(got, want) {
					check.Problem = "environment delivery policy differs from last install"
				}
				if target.engine == extensionClaude && active {
					for key, want := range extension.Env {
						value, has, err := extensionLookup(document, []string{configEnvKey, key})
						if err != nil {
							check.Problem = err.Error()
							break
						}
						if !has || value != want {
							check.Problem = "settings env key " + key + " missing or different"
							break
						}
					}
				}
			case "hook trust":
				if active {
					binary := options.CodexBinary
					if binary == "" {
						binary = extensionCodex
					}
					if err := codexappendix.PluginHookTrustState(ctx, binary, target.home, target.fact.ID); err != nil {
						check.Problem = err.Error()
					}
				}
			}
			result = append(result, check)
		}
	}
	return result, nil
}

func extensionPackageEntries(document map[string]any) ([]any, error) {
	value, has := document["plugin"]
	if !has {
		return []any{}, nil
	}
	entries, ok := value.([]any)
	if !ok {
		return nil, errors.New("OpenCode plugin must be an array")
	}
	for _, entry := range entries {
		if _, ok := entry.(string); !ok {
			return nil, errors.New("OpenCode plugin entries must be strings")
		}
	}
	return entries, nil
}

func extensionPackagePresent(document map[string]any, pkg string) bool {
	entries, _ := extensionPackageEntries(document)
	for _, entry := range entries {
		if entry == pkg {
			return true
		}
	}
	return false
}
