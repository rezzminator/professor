package mockengine

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const codexPluginAdd = "add"

// codexPluginHooks discovers fixture plugins from the same cache layout as the
// engine, then fingerprints their real hook documents through the native path.
func codexPluginHooks(home string, config map[string]any) ([]codexNativeHook, error) {
	plugins, _ := config["plugins"].(map[string]any)
	ids := make([]string, 0, len(plugins))
	for id := range plugins {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var result []codexNativeHook
	for _, id := range ids {
		plugin, _ := plugins[id].(map[string]any)
		if plugin["enabled"] != true {
			continue
		}
		name, market, ok := strings.Cut(id, "@")
		if !ok {
			return nil, fmt.Errorf("fixture plugin id %s has no marketplace", id)
		}
		cache := filepath.Join(home, "plugins", "cache", market, name)
		versions, err := os.ReadDir(cache)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read fixture plugin cache %s: %w", cache, err)
		}
		for _, version := range versions {
			if !version.IsDir() {
				continue
			}
			root := filepath.Join(cache, version.Name())
			err := filepath.WalkDir(
				filepath.Join(root, "hooks"),
				func(path string, entry os.DirEntry, walkErr error) error {
					if walkErr != nil {
						if errors.Is(walkErr, os.ErrNotExist) {
							return nil
						}
						return walkErr
					}
					if entry.IsDir() || !strings.HasSuffix(path, ".json") {
						return nil
					}
					raw, err := os.ReadFile(path)
					if err != nil {
						return fmt.Errorf("read plugin hook fixture %s: %w", path, err)
					}
					relative, err := filepath.Rel(root, path)
					if err != nil {
						return fmt.Errorf("relative plugin hook path: %w", err)
					}
					hooks, err := codexHooksFromDocument(raw, path, "plugin", id+":"+filepath.ToSlash(relative), config)
					if err != nil {
						return err
					}
					result = append(result, hooks...)
					return nil
				},
			)
			if err != nil {
				return nil, fmt.Errorf("discover fixture plugin %s: %w", id, err)
			}
		}
	}
	return result, nil
}

func answerCodexPluginCLI(proc *process) int {
	home := proc.env("CODEX_HOME")
	if home == "" {
		home = filepath.Join(proc.env("HOME"), ".codex")
	}
	args := proc.args
	var err error
	switch {
	case len(args) == 4 && args[1] == "marketplace" && args[2] == codexPluginAdd:
		name := filepath.Base(args[3])
		path := filepath.Join(home, "config.toml")
		raw, readErr := os.ReadFile(path)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			err = readErr
			break
		}
		var config map[string]any
		if _, decodeErr := toml.Decode(string(raw), &config); decodeErr != nil {
			err = decodeErr
			break
		}
		markets, _ := config["marketplaces"].(map[string]any)
		if _, has := markets[name]; !has {
			var value bytes.Buffer
			fmt.Fprintf(&value, "\n[marketplaces.%q]\n", name)
			err = toml.NewEncoder(&value).Encode(map[string]any{claudeSourceKey: args[3]})
			if err == nil {
				err = os.WriteFile(path, append(raw, value.Bytes()...), 0o600)
			}
		}
	case len(args) == 3 && (args[1] == codexPluginAdd || args[1] == "remove"):
		name, market, ok := strings.Cut(args[2], "@")
		if !ok {
			err = fmt.Errorf("fixture plugin id %s must name marketplace", args[2])
			break
		}
		cache := filepath.Join(home, "plugins", "cache", market, name)
		if args[1] == "remove" {
			err = os.RemoveAll(cache)
			break
		}
		path := filepath.Join(cache, "5.3.2", "hooks", "claude-codex-hooks.json")
		err = os.MkdirAll(filepath.Dir(path), 0o700)
		if err == nil {
			err = os.WriteFile(
				path,
				[]byte(
					`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"printf fixture"}]}],"UserPromptSubmit":[{"hooks":[{"type":"command","command":"printf fixture"}]}],"SubagentStart":[{"hooks":[{"type":"command","command":"printf fixture"}]}]}}`,
				),
				0o600,
			)
		}
	default:
		err = fmt.Errorf("codex plugin command not scripted: %v", args)
	}
	if err != nil {
		fmt.Fprintf(proc.stderr, "mock-engine: codex plugin: %v\n", err)
		return ExitUnpinned
	}
	fmt.Fprintln(proc.stdout, "mock-engine: codex plugin fixture converged")
	return 0
}
