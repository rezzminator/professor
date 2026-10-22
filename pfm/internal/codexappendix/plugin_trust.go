package codexappendix

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const rpcCWDsKey = "cwds"

// PluginHookTrustState reads current native trust, never a historical receipt.
// Discovery cannot authorize another plugin, a user hook, or a disabled plugin.
func PluginHookTrustState(ctx context.Context, binary, account, id string) error {
	hooks, err := managedPluginHooks(ctx, binary, account, id)
	if err != nil {
		return err
	}
	for index := range hooks {
		h := &hooks[index]
		if !h.Enabled || h.TrustStatus != trustedState {
			return fmt.Errorf("plugin hook %s disabled or untrusted", h.Key)
		}
	}
	return nil
}

// RegisterPluginHookTrusts authorizes only source=plugin hooks under the exact
// enabled managed id's namespace. OFF never calls this function.
func RegisterPluginHookTrusts(ctx context.Context, binary, account, id string) error {
	hooks, err := managedPluginHooks(ctx, binary, account, id)
	if err != nil {
		return err
	}
	for index := range hooks {
		h := &hooks[index]
		if !h.Enabled || h.TrustStatus == trustedState {
			continue
		}
		key, err := json.Marshal(h.Key)
		if err != nil {
			return fmt.Errorf("quote plugin hook %s: %w", h.Key, err)
		}
		if _, err := rpc(ctx, binary, account, "config/value/write", map[string]any{
			"keyPath": "hooks.state." + string(
				key,
			),
			"value":         map[string]any{"enabled": true, "trusted_hash": h.CurrentHash},
			"mergeStrategy": "replace",
		}); err != nil {
			return fmt.Errorf("trust plugin hook %s in %s: %w", h.Key, account, err)
		}
	}
	return nil
}

func managedPluginHooks(ctx context.Context, binary, account, id string) ([]hook, error) {
	path := filepath.Join(account, "config.toml")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read plugin enablement %s: %w", path, err)
	}
	var config struct {
		Plugins map[string]struct {
			Enabled bool `toml:"enabled"`
		} `toml:"plugins"`
	}
	if _, err := toml.Decode(string(raw), &config); err != nil {
		return nil, fmt.Errorf("decode plugin enablement %s: %w", path, err)
	}
	if !config.Plugins[id].Enabled {
		return nil, fmt.Errorf("plugin %s is not enabled in %s; refusing hook trust", id, account)
	}
	listed, _, err := listNativeHooks(ctx, binary, account)
	if err != nil {
		return nil, fmt.Errorf("discover plugin %s hooks in %s: %w", id, account, err)
	}
	var hooks []hook
	seen := map[string]bool{}
	for index := range listed.hooks {
		h := &listed.hooks[index]
		if h.Source != "plugin" || !strings.HasPrefix(h.Key, id+":") {
			continue
		}
		if h.CurrentHash == "" || seen[h.Key] {
			return nil, fmt.Errorf("plugin hook %s has missing hash or duplicate key", h.Key)
		}
		seen[h.Key] = true
		hooks = append(hooks, *h)
	}
	if len(hooks) == 0 {
		return nil, fmt.Errorf("plugin %s hooks unavailable in %s; trust unknown", id, account)
	}
	return hooks, nil
}
