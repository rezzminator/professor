package doctor

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/hostcheck"
)

const doctorStaleUsage = "       pfm doctor --stale [--purge]" +
	" list the retired paths pfm once wrote; --purge moves them into one backup dir"

// staleRename is the purge's one move; a test swaps it to fail a move.
var staleRename = os.Rename

// staleFlags are doctor's --stale mode: the retired-path registry
// (hostcheck.RetiredPaths) alone, never the health pass.
type staleFlags struct{ enabled, purge *bool }

func bindStaleFlags(flags *flag.FlagSet) staleFlags {
	return staleFlags{
		enabled: flags.Bool("stale", false, "list every retired path pfm once wrote that still exists, then exit"),
		purge:   flags.Bool("purge", false, "with --stale: move every stale path into one timestamped backup dir"),
	}
}

// set reports whether either stale flag is on, for the other modes' usage door.
func (s staleFlags) set() bool { return *s.enabled || *s.purge }

// dispatch returns handled=true with the exit code when the call is the stale
// mode or misuses its flags (usage, exit 2): --purge without --stale, --stale
// with a health-pass flag.
func (s staleFlags) dispatch(
	flags *flag.FlagSet,
	healthFlagSet bool,
	runtime config.Runtime,
	now time.Time,
	stdout io.Writer,
) (code int, handled bool) {
	switch {
	case *s.enabled && healthFlagSet, *s.purge && !*s.enabled:
		flags.Usage()
		return 2, true
	case *s.enabled:
		if runtime.ConfigError != nil {
			fmt.Fprintf(stdout, "stale: config error=%v — the accounts are unknown, so no path is judged\n",
				runtime.ConfigError)
			return 3, true
		}
		return runStalePaths(hostcheck.EnvFor(runtime, now), *s.purge, now, stdout), true
	}
	return 0, false
}

// runStalePaths prints one row per retired path; with purge it moves every WARN
// path into one backup dir. Exit 0 nothing stale (or all moved), 1 stale paths
// listed, 3 a held or unreadable path or a failed move.
func runStalePaths(env hostcheck.Env, purge bool, now time.Time, stdout io.Writer) int {
	rows := hostcheck.RetiredPaths(env)
	if len(rows) == 0 {
		fmt.Fprintf(stdout, "stale: ok (%d kinds walked)\n", len(hostcheck.RetiredPathDetectors()))
		return 0
	}
	for _, row := range rows {
		fmt.Fprint(stdout, row.Render("stale: "))
	}
	warnings, failures := hostcheck.Count(rows, hostcheck.Warn), hostcheck.Count(rows, hostcheck.Block)
	if !purge {
		fmt.Fprintf(stdout, "stale: %d stale, %d held or unreadable\n", warnings, failures)
		if failures > 0 {
			return 3
		}
		return 1
	}
	if warnings == 0 {
		fmt.Fprintf(stdout, "stale: nothing to purge, %d held or unreadable\n", failures)
		return 3
	}
	backup, err := createStaleBackupDir(env.Home, now)
	if err != nil {
		fmt.Fprintf(stdout, "stale: FAILED backup dir — %v; nothing moved\n", err)
		return 3
	}
	moved, failed := 0, 0
	for _, path := range purgeRoots(rows) {
		target := staleBackupTarget(backup, env.Home, path)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			fmt.Fprintf(stdout, "stale: FAILED %s — create %s: %v\n", path, filepath.Dir(target), err)
			failed++
			continue
		}
		if err := staleRename(path, target); err != nil {
			fmt.Fprintf(stdout, "stale: FAILED %s — %v\n", path, err)
			failed++
			continue
		}
		fmt.Fprintf(stdout, "stale: moved %s -> %s\n", path, target)
		moved++
	}
	fmt.Fprintf(stdout, "stale: backup %s — moved %d, failed %d\n", backup, moved, failed)
	if failed > 0 || failures > 0 {
		return 3
	}
	return 0
}

// purgeRoots is every WARN path once, a path inside another listed path
// dropped since it moves with its parent.
func purgeRoots(rows []hostcheck.Row) []string {
	var listed []string
	for _, row := range rows {
		if row.Severity == hostcheck.Warn {
			listed = append(listed, filepath.Clean(row.Path))
		}
	}
	sort.Strings(listed)
	var roots []string
	for _, path := range listed {
		if len(roots) > 0 {
			last := roots[len(roots)-1]
			if path == last || strings.HasPrefix(path, last+string(filepath.Separator)) {
				continue
			}
		}
		roots = append(roots, path)
	}
	return roots
}

// createStaleBackupDir makes {home}/.local/state/pfm/stale-backup/{stamp},
// suffixed .N when that stamp is taken; never an existing dir.
func createStaleBackupDir(home string, now time.Time) (string, error) {
	parent := filepath.Join(home, ".local", "state", "pfm", "stale-backup")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", parent, err)
	}
	base := filepath.Join(parent, now.Format("20060102-150405"))
	for index := 0; ; index++ {
		dir := base
		if index > 0 {
			dir = base + "." + strconv.Itoa(index)
		}
		err := os.Mkdir(dir, 0o700)
		if err == nil {
			return dir, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("create %s: %w", dir, err)
		}
	}
}

// staleBackupTarget keeps a path's place under the home; one outside it lands
// under _root with its absolute path.
func staleBackupTarget(backup, home, path string) string {
	relative, err := filepath.Rel(home, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.Join(backup, "_root", path)
	}
	return filepath.Join(backup, relative)
}
