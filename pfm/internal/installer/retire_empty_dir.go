package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// This file holds the one retirement that can refuse: a managed directory
// pfm owns end to end is removed only once it is empty, and anything left in
// it is an operator file this installer must not delete.
//
// The refusal is computed in EVERY mode, dry run included. It used to run
// only under installer.apply, and preflight plans with apply=false — so a
// stray file under the managed chat/ or codex-skills/ tree let preflight
// report a clean plan, and the real pass then staged assets, launchers,
// overlays and migrations before hitting the same refusal and aborting,
// leaving the machine half-converged and naming only the directory. A dry
// pass has removed nothing yet, so it discounts what the pass itself
// retires (removedPaths) before deciding: the question is whether the
// directory would be empty by the time the removal runs, not whether it is
// empty right now.

// markRemoved records a path this pass has removed (apply) or planned to
// remove (dry run) so a later emptiness question can discount it.
func (installer *engine) markRemoved(path string) {
	if installer.removedPaths == nil {
		installer.removedPaths = map[string]bool{}
	}
	installer.removedPaths[filepath.Clean(path)] = true
}

// strayEntries lists what would still be in dir after this pass — its
// entries minus everything the pass has already removed or planned to
// remove. The list is sorted so the refusal reads the same on every run.
func (installer *engine) strayEntries(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("inspect managed directory %s: %w", dir, err)
	}
	stray := make([]string, 0, len(entries))
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		if installer.removedPaths[filepath.Clean(path)] {
			continue
		}
		stray = append(stray, path)
	}
	sort.Strings(stray)
	return stray, nil
}

// retireEmptyDir removes a directory this installer owns completely, and
// refuses when anything the pass is not itself removing is still inside.
// The refusal names the stray files, because "remove this file and rerun" is
// the operator's whole remedy — the directory's name alone leaves them
// guessing what is in it.
//
// In a dry run the refusal is a plan error (the same shape
// reconcileCodexCommands uses for a Codex conflict): planning continues so
// the operator sees the WHOLE plan, and Run joins the plan errors at the
// end, which is what makes preflight fail before the apply pass mutates
// anything.
func (installer *engine) retireEmptyDir(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refuse to retire non-directory %s", path)
	}
	stray, err := installer.strayEntries(path)
	if err != nil {
		return err
	}
	if len(stray) != 0 {
		conflict := fmt.Errorf(
			"refuse to retire non-empty directory %s — move or delete %s, then rerun",
			path,
			strings.Join(stray, ", "),
		)
		if installer.apply {
			return conflict
		}
		installer.say("  conflict %s", conflict.Error())
		installer.planErrors = append(installer.planErrors, conflict)
		return nil
	}
	installer.markRemoved(path)
	return installer.change("remove empty "+path, func() error { return os.Remove(path) })
}
