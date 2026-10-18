package installer

import (
	"encoding/json"
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

// retiredGptHookAssets are the hook scripts an earlier pfm staged in its
// managed root and linked into hooks/ of the default Claude store, an explicit
// install store and every account: the gpt agent Stop hook and the return
// watch before it. Nothing stages them now; install and uninstall remove each
// link that resolves exactly to its managed copy, then the managed copy, the
// default log directory the hook wrote and the ownership receipt.
var retiredGptHookAssets = []string{"gpt-agent-stop.py", "gpt-return-watch.py"}

// retiredGptHookReceiptName is the receipt the gpt hook wiring kept in the
// managed root: every link location it made, accounts since dropped from the
// config included.
const retiredGptHookReceiptName = "gpt-hook-ownership.json"

// retiredStateDirs are pfm-owned directories under ~/.local/state/pfm whose
// writers are gone: the call-cap hook's per-transcript tallies and the
// tracetree cut gate's stamp records. Install removes each whole.
var retiredStateDirs = []struct{ name, what string }{
	{"call-cap", "retired call-cap tally directory"},
	{"cutgate", "retired tracetree cut-gate stamp directory"},
}

// retireStateDirs removes the retired state directories; a dry run names
// what it would free, a failed removal is a warn line naming the path.
//
// Since v0.83.0. Sunset: delete this step and retiredStateDirs at v0.93.0, ten
// minor versions past Since (docs/dev/pfm-architecture.md Open ruling 6).
func (installer *engine) retireStateDirs() {
	for _, dir := range retiredStateDirs {
		path := filepath.Join(installer.options.Home, ".local", "state", "pfm", dir.name)
		installer.removeStatePath(path, dir.what, "")
	}
}

// retireGptHooks sweeps the retired gpt hook assets. The exact symlink to the
// managed copy is the ownership proof: an operator file or a foreign link at
// the same path is kept and named. The managed copies, their log directories
// and the receipt go only when every link step succeeded and the receipt read,
// so a link only the receipt knew of never dangles unnamed. An unreadable
// receipt is a warn line naming it, never a failed install: the preflight
// runs this step too, and a leftover of a retired feature must not block
// every other step.
//
// Since v0.83.0 for the Stop hook. Sunset: delete this file's gpt hook steps
// with their callers at v0.93.0 (docs/dev/pfm-architecture.md Open ruling 6).
func (installer *engine) retireGptHooks() error {
	var failures []error
	receipt, receiptErr := readRetiredGptHookReceipt(installer.managedRoot)
	if receiptErr != nil {
		installer.say("  warn    %v", receiptErr)
		installer.record("warn", "retired gpt hook receipt unreadable", receiptErr)
	}
	current, err := retiredGptHookTargets(retiredGptHookDirs(
		installer.options.Home, installer.options.ConfigDir, installer.options.ClaudeAccounts,
	))
	if err != nil {
		failures = append(failures, err)
	}
	targets := slices.Concat(receipt, current)
	slices.Sort(targets)
	for _, target := range slices.Compact(targets) {
		if err := installer.retireGptHookLink(target); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) != 0 {
		return errors.Join(failures...)
	}
	if receiptErr != nil {
		return nil
	}
	for _, name := range retiredGptHookAssets {
		if err := installer.retire(filepath.Join(installer.managedRoot, name), "retired gpt hook asset"); err != nil {
			return fmt.Errorf("retire gpt hook asset %s: %w", name, err)
		}
		logDir := filepath.Join(installer.options.Home, ".local", "state", "pfm", strings.TrimSuffix(name, ".py"))
		installer.removeStatePath(logDir, "retired gpt hook log directory", "")
	}
	return installer.removeRetiredGptHookReceipt()
}

// retiredGptHookDirs are the default store, an explicit install store and
// every account; physical deduplication also handles account-directory aliases.
func retiredGptHookDirs(home, store string, accounts []pfmconfig.Account) []string {
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

// retiredGptHookTargets canonicalizes each hooks/ parent, never the asset
// link (which resolves to its managed source), so a moved account alias
// leaves no orphan.
func retiredGptHookTargets(dirs []string) ([]string, error) {
	var targets []string
	for _, dir := range dirs {
		parent, err := filepath.Abs(filepath.Join(dir, "hooks"))
		if err != nil {
			return nil, fmt.Errorf("resolve gpt hook directory %s: %w", dir, err)
		}
		for _, name := range retiredGptHookAssets {
			targets = append(targets, filepath.Join(paths.PhysicalPath(parent), name))
		}
	}
	return targets, nil
}

type retiredGptHookReceipt struct {
	Version int      `json:"version"`
	Targets []string `json:"targets"`
}

// readRetiredGptHookReceipt returns the link locations the receipt names. It
// remembers locations, not permission to remove them: each link is still
// checked against its exact managed source.
func readRetiredGptHookReceipt(managedRoot string) ([]string, error) {
	path := filepath.Join(managedRoot, retiredGptHookReceiptName)
	raw, absent, err := readHookFile(path)
	if err != nil {
		return nil, fmt.Errorf("read gpt hook ownership %s: %w", path, err)
	}
	if absent {
		return nil, nil
	}
	var receipt retiredGptHookReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return nil, fmt.Errorf("decode gpt hook ownership %s: %w; delete it to finish the retirement", path, err)
	}
	if receipt.Version != 1 || receipt.Targets == nil {
		return nil, fmt.Errorf(
			"gpt hook ownership %s: want version 1 with a targets array; delete it to finish the retirement", path,
		)
	}
	for _, target := range receipt.Targets {
		if !filepath.IsAbs(target) || filepath.Clean(target) != target ||
			!slices.Contains(retiredGptHookAssets, filepath.Base(target)) {
			return nil, fmt.Errorf("gpt hook ownership %s: invalid asset target %q", path, target)
		}
	}
	return receipt.Targets, nil
}

// inspectRetiredGptHookLink reports whether target is a symlink resolving
// exactly to source, and whether nothing is there at all.
func inspectRetiredGptHookLink(source, target string) (owned, absent bool, err error) {
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

// retireGptHookLink removes one link the managed copy owns and keeps, naming
// it, any other entry at that path.
func (installer *engine) retireGptHookLink(target string) error {
	source := filepath.Join(installer.managedRoot, filepath.Base(target))
	owned, absent, err := inspectRetiredGptHookLink(source, target)
	if err != nil {
		return err
	}
	if !owned {
		if !absent {
			installer.skip("preserve unowned retired gpt hook asset " + target)
		}
		return nil
	}
	return installer.change("remove "+target, func() error {
		stillOwned, _, err := inspectRetiredGptHookLink(source, target)
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

func (installer *engine) removeRetiredGptHookReceipt() error {
	path := filepath.Join(installer.managedRoot, retiredGptHookReceiptName)
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
