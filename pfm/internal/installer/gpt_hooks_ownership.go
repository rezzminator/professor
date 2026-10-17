package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const gptHookOwnershipName = "gpt-hook-ownership.json"

type gptHookOwnership struct {
	Version int      `json:"version"`
	Targets []string `json:"targets"`
}

// The receipt remembers locations, not permission to replace them: retirement
// must still verify each link against its exact managed source.
func readGptHookOwnership(managedRoot string) ([]string, error) {
	path := filepath.Join(managedRoot, gptHookOwnershipName)
	raw, absent, err := readHookFile(path)
	if err != nil {
		return nil, fmt.Errorf("read gpt hook ownership %s: %w", path, err)
	}
	if absent {
		return nil, nil
	}
	var receipt gptHookOwnership
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return nil, fmt.Errorf("decode gpt hook ownership %s: %w", path, err)
	}
	if receipt.Version != 1 {
		return nil, fmt.Errorf("gpt hook ownership %s: unsupported version %d", path, receipt.Version)
	}
	if receipt.Targets == nil {
		return nil, fmt.Errorf("gpt hook ownership %s: targets must be an array", path)
	}
	for _, target := range receipt.Targets {
		if !filepath.IsAbs(target) || filepath.Clean(target) != target ||
			!isGptHookName(filepath.Base(target)) {
			return nil, fmt.Errorf("gpt hook ownership %s: invalid asset target %q", path, target)
		}
	}
	return receipt.Targets, nil
}

func (installer *engine) gptHookTargets() ([]string, error) {
	return gptHookTargets(
		gptHookDirs(installer.options.Home, installer.options.ConfigDir, installer.options.ClaudeAccounts),
	)
}

func (installer *engine) retiredGptHookTargets() ([]string, error) {
	return gptHookTargetsNamed(
		gptHookDirs(installer.options.Home, installer.options.ConfigDir, installer.options.ClaudeAccounts),
		retiredGptHookAssets,
	)
}

func gptHookTargets(dirs []string) ([]string, error) {
	return gptHookTargetsNamed(dirs, gptHookAssets)
}

func gptHookTargetsNamed(dirs, names []string) ([]string, error) {
	var targets []string
	for _, dir := range dirs {
		parent, err := filepath.Abs(filepath.Join(dir, "hooks"))
		if err != nil {
			return nil, fmt.Errorf("resolve gpt hook directory %s: %w", dir, err)
		}
		for _, name := range names {
			// Canonicalize the parent, not the asset symlink (which resolves to
			// its managed source), so moved account aliases leave no orphan.
			targets = append(targets, filepath.Join(paths.PhysicalPath(parent), name))
		}
	}
	return targets, nil
}

func (installer *engine) writeGptHookOwnership(targets []string) error {
	if len(targets) == 0 {
		return nil
	}
	sort.Strings(targets)
	targets = slices.Compact(targets)
	encoded, err := json.MarshalIndent(gptHookOwnership{Version: 1, Targets: targets}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode gpt hook ownership: %w", err)
	}
	encoded = append(encoded, '\n')
	path := filepath.Join(installer.managedRoot, gptHookOwnershipName)
	if sameFile(path, encoded, 0o600) {
		installer.ok(path)
		return nil
	}
	return installer.change("write "+path, func() error {
		if err := atomicfile.Write(path, encoded, 0o600); err != nil {
			return fmt.Errorf("write gpt hook ownership %s: %w", path, err)
		}
		return nil
	})
}

func (installer *engine) removeGptHookOwnership() error {
	path := filepath.Join(installer.managedRoot, gptHookOwnershipName)
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect gpt hook ownership %s: %w", path, err)
	}
	return installer.change("remove "+path, func() error {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove gpt hook ownership %s: %w", path, err)
		}
		return nil
	})
}
