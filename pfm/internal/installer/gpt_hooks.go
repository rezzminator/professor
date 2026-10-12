package installer

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

var gptHookAssets = []string{"gpt-return-watch.py", "gpt-agent-stop.py"}

// gptHookDirs includes the default store, an explicit install store, and every
// account. Physical deduplication also handles account-directory aliases.
func gptHookDirs(home, store string, accounts []pfmconfig.Account) []string {
	candidates := []string{filepath.Join(home, ".claude"), store}
	for _, account := range accounts {
		candidates = append(candidates, account.ConfigDir)
	}
	seen := map[string]bool{}
	var dirs []string
	for _, dir := range candidates {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		physical := paths.PhysicalPath(dir)
		if !seen[physical] {
			seen[physical] = true
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

// The exact symlink to the managed copy is the ownership receipt. Never
// replace an operator file or foreign link, even when its bytes match ours.
func (installer *engine) wireGptHookAssets() error {
	targets, err := readGptHookOwnership(installer.managedRoot)
	if err != nil {
		return err
	}
	current, err := installer.gptHookTargets()
	if err != nil {
		return err
	}
	var failures []error
	for _, target := range current {
		source := filepath.Join(installer.managedRoot, filepath.Base(target))
		if err := installer.wireGptHookAsset(source, target); err != nil {
			failures = append(failures, err)
		} else {
			targets = append(targets, target)
		}
	}
	if err := installer.writeGptHookOwnership(targets); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}

func (installer *engine) wireGptHookAsset(source, target string) error {
	owned, absent, err := inspectGptHookLink(source, target)
	if err != nil {
		return err
	}
	if owned {
		installer.ok(target)
		return nil
	}
	if !absent {
		return fmt.Errorf(
			"refuse unowned gpt hook asset %s; move the operator entry aside before pfm install --yes",
			target,
		)
	}
	return installer.change("link "+target+" -> "+source, func() error {
		if _, err := os.Stat(source); err != nil {
			return fmt.Errorf("gpt hook asset source not published %s: %w", source, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("create gpt hook asset directory %s: %w", filepath.Dir(target), err)
		}
		// Symlink is create-only: a raced operator entry must survive.
		if err := os.Symlink(source, target); err != nil {
			return fmt.Errorf("create gpt hook asset link %s: %w", target, err)
		}
		return nil
	})
}

func inspectGptHookLink(source, target string) (owned, absent bool, err error) {
	info, err := os.Lstat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return false, true, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("inspect gpt hook asset %s: %w", target, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		return false, false, nil
	}
	link, err := os.Readlink(target)
	if err != nil {
		return false, false, fmt.Errorf("read gpt hook asset link %s: %w", target, err)
	}
	if !filepath.IsAbs(link) {
		link = filepath.Join(filepath.Dir(target), link)
	}
	return filepath.Clean(link) == filepath.Clean(source), false, nil
}

func (installer *engine) unwireGptHookAssets() error {
	targets, err := readGptHookOwnership(installer.managedRoot)
	if err != nil {
		return err
	}
	current, err := installer.gptHookTargets()
	if err != nil {
		return err
	}
	targets = append(targets, current...)
	for _, target := range targets {
		source := filepath.Join(installer.managedRoot, filepath.Base(target))
		owned, absent, err := inspectGptHookLink(source, target)
		if err != nil {
			return err
		}
		if !owned {
			if !absent {
				installer.skip("preserve unowned gpt hook asset " + target)
			}
			continue
		}
		if err := installer.change("remove "+target, func() error {
			stillOwned, _, err := inspectGptHookLink(source, target)
			if err != nil {
				return err
			}
			if !stillOwned {
				return fmt.Errorf("gpt hook asset %s changed since inspection; kept operator entry", target)
			}
			if err := os.Remove(target); err != nil {
				return fmt.Errorf("remove gpt hook asset %s: %w", target, err)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return installer.removeGptHookOwnership()
}

func probeGptHookAssets(home string, config pfmconfig.Config) []HookProbeResult {
	var results []HookProbeResult
	managedRoot := managedRootForHome(home)
	targets, err := readGptHookOwnership(managedRoot)
	if err != nil {
		results = append(results, HookProbeResult{
			Hook:  ExpectedHook{Target: hookTargetClaude, File: filepath.Join(managedRoot, gptHookOwnershipName)},
			State: stateUnreadable, Error: err.Error(),
		})
	}
	// Doctor has no install Options.ConfigDir. The receipt retains explicit and
	// historical stores; the default store is expected even with no accounts.
	current, err := gptHookTargets(gptHookDirs(home, "", config.Accounts))
	if err != nil {
		results = append(results, HookProbeResult{
			Hook:  ExpectedHook{Target: hookTargetClaude, File: home},
			State: stateUnreadable, Error: err.Error(),
		})
	} else {
		targets = append(targets, current...)
	}
	seen := map[string]bool{}
	for _, target := range targets {
		name := filepath.Base(target)
		// Never resolve the final asset link: distinct stores share its source.
		path := filepath.Join(paths.PhysicalPath(filepath.Dir(target)), name)
		if seen[path] {
			continue
		}
		seen[path] = true
		row := HookProbeResult{
			Hook: ExpectedHook{
				Target: hookTargetClaude,
				File:   path,
				Event:  "asset",
				Name:   fmt.Sprintf("%q", filepath.Dir(path)),
			},
			State: stateOK,
		}
		content, absent, err := readHookFile(path)
		switch {
		case err != nil:
			row.State, row.Error = stateUnreadable, err.Error()
		case absent:
			row.State = stateMissing
		default:
			expected, err := readAsset(name)
			if err != nil {
				row.State, row.Error = stateUnreadable, fmt.Sprintf(
					"read embedded gpt hook asset %s: %v",
					name,
					err,
				)
			} else if !bytes.Equal(content, expected) {
				row.State, row.What, row.Want, row.Got = stateHookDrift, "asset", "embedded content", "different content"
			}
		}
		results = append(results, row)
	}
	return results
}
