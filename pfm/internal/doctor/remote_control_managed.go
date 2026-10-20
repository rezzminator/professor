package doctor

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Managed policy outranks account settings. Matching values across managed
// sources are unambiguous; conflicting values are reported as unknown rather
// than guessing an undocumented ordering between the monolith and drop-ins.
type remoteControlManagedPolicy struct {
	value         *bool
	sources       []string
	conflict      bool
	env           map[string]string
	helper        *string
	layerConflict bool
}

func readManagedRemoteControlPolicy(dir string) (remoteControlManagedPolicy, error) {
	var policy remoteControlManagedPolicy
	if !filepath.IsAbs(dir) {
		return policy, fmt.Errorf("managed policy %q: expected absolute settings directory", dir)
	}
	candidates := []string{filepath.Join(filepath.Dir(dir), "managed-settings.json")}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		// A dangling directory symlink is unreadable policy, not its absence.
		if _, statErr := os.Lstat(dir); statErr == nil {
			return policy, fmt.Errorf("managed policy enumerate %s: %w", dir, err)
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return policy, fmt.Errorf("managed policy inspect %s: %w", dir, statErr)
		}
	} else if err != nil {
		return policy, fmt.Errorf("managed policy enumerate %s: %w", dir, err)
	}
	// Match the visible regular-file/symlink enumeration Claude uses, also
	// followed by installer.inspectPrependPlugins. Never follow hidden entries.
	for _, entry := range entries {
		name := entry.Name()
		listed := entry.Type().IsRegular() || entry.Type()&fs.ModeSymlink != 0
		if listed && !strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".json") {
			candidates = append(candidates, filepath.Join(dir, name))
		}
	}
	for _, path := range candidates {
		settings, err := readRemoteControlSettings(path)
		if err != nil {
			return policy, fmt.Errorf("managed policy %w", err)
		}
		if settings == nil {
			continue
		}
		if policy.env == nil {
			policy.env = make(map[string]string)
		}
		for _, name := range remoteControlEnvNames {
			if value, set := settings.Env[name]; set {
				if previous, exists := policy.env[name]; exists && previous != value {
					policy.layerConflict = true
				}
				policy.env[name] = value
			}
		}
		if settings.APIKeyHelper != nil {
			if policy.helper != nil && *policy.helper != *settings.APIKeyHelper {
				policy.layerConflict = true
			}
			policy.helper = settings.APIKeyHelper
		}
		if settings.DisableRemoteControl == nil {
			continue
		}
		if policy.value != nil && *policy.value != *settings.DisableRemoteControl {
			policy.conflict = true
		}
		policy.value = settings.DisableRemoteControl
		policy.sources = append(policy.sources, path)
	}
	return policy, nil
}

func (policy remoteControlManagedPolicy) blockers() []string {
	source := strings.Join(policy.sources, ", ")
	if policy.layerConflict {
		return []string{"managed settings have conflicting Remote Control environment or helper values; " +
			"precedence not verified"}
	}
	if policy.conflict {
		return []string{"managed settings disableRemoteControl has conflicting values in " + source +
			"; effective managed policy precedence not verified"}
	}
	if policy.value != nil && *policy.value {
		return []string{"managed settings " + source + " disableRemoteControl=true"}
	}
	return nil
}
