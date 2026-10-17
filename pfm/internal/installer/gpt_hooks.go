package installer

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

var gptHookAssets = []string{"gpt-agent-stop.py"}

// retiredGptHookAssets are hook scripts an earlier pfm linked beside the
// current ones. Every install and uninstall removes the links it owns, the
// managed copy and the default log directory of each; nothing stages them.
var retiredGptHookAssets = []string{"gpt-return-watch.py"}

func isGptHookName(name string) bool {
	return slices.Contains(gptHookAssets, name) || slices.Contains(retiredGptHookAssets, name)
}

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
	receipt, err := readGptHookOwnership(installer.managedRoot)
	if err != nil {
		return err
	}
	current, err := installer.gptHookTargets()
	if err != nil {
		return err
	}
	targets, failures := installer.retireGptHookAssets(receipt)
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
	retired, err := installer.retiredGptHookTargets()
	if err != nil {
		return err
	}
	targets = append(append(targets, current...), retired...)
	for _, target := range targets {
		if err := installer.retireGptHookLink(target); err != nil {
			return err
		}
	}
	if err := installer.retireGptHookLeftovers(); err != nil {
		return err
	}
	return installer.removeGptHookOwnership()
}

// retireGptHookLink removes one link the managed copy owns and keeps any
// other entry at that path.
func (installer *engine) retireGptHookLink(target string) error {
	name := filepath.Base(target)
	source := filepath.Join(installer.managedRoot, name)
	owned, absent, err := inspectGptHookLink(source, target)
	if err != nil {
		return err
	}
	if !owned {
		if !absent {
			label := "gpt hook asset "
			if slices.Contains(retiredGptHookAssets, name) {
				label = "retired " + label
			}
			installer.skip("preserve unowned " + label + target)
		}
		return nil
	}
	return installer.change("remove "+target, func() error {
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
	})
}

// retireGptHookAssets sweeps the retired hooks from the receipt and from every
// current hooks directory, then returns the receipt's remaining targets: the
// current ones, plus a retired link whose removal failed.
func (installer *engine) retireGptHookAssets(receipt []string) (kept []string, failures []error) {
	retired, err := installer.retiredGptHookTargets()
	if err != nil {
		return nil, []error{err}
	}
	var candidates []string
	for _, target := range receipt {
		if slices.Contains(retiredGptHookAssets, filepath.Base(target)) {
			candidates = append(candidates, target)
		} else {
			kept = append(kept, target)
		}
	}
	candidates = append(candidates, retired...)
	slices.Sort(candidates)
	candidates = slices.Compact(candidates)
	for _, target := range candidates {
		if err := installer.retireGptHookLink(target); err != nil {
			failures = append(failures, err)
			kept = append(kept, target)
		}
	}
	if len(failures) == 0 {
		if err := installer.retireGptHookLeftovers(); err != nil {
			failures = append(failures, err)
		}
	}
	return kept, failures
}

// retireGptHookLeftovers removes each retired hook's managed copy and its
// default log directory, the state a retired hook wrote beside its link.
func (installer *engine) retireGptHookLeftovers() error {
	for _, name := range retiredGptHookAssets {
		if err := installer.retire(filepath.Join(installer.managedRoot, name), "retired gpt hook asset"); err != nil {
			return err
		}
		logDir := filepath.Join(installer.options.Home, ".local", "state", "pfm", strings.TrimSuffix(name, ".py"))
		installer.removeStatePath(logDir, "retired gpt hook log directory", "")
	}
	return nil
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
		if slices.Contains(retiredGptHookAssets, name) {
			continue
		}
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
