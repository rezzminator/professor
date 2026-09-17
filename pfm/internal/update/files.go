package update

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"hostops/pfm/internal/atomicfile"
	"hostops/pfm/internal/config"
	"hostops/pfm/internal/installer"
)

// updateFileSnapshot is one installer-owned file captured around the
// candidate's `install --yes`: its bytes before (the state rollback returns
// to) and right after (the only state rollback may overwrite).
type updateFileSnapshot struct {
	path          string // physical path: a symlinked account settings file is written through, never replaced
	before        []byte
	beforeExisted bool
	beforeMode    fs.FileMode
	after         []byte
	afterExisted  bool
	afterErr      error
}

// snapshotUpdateOwnedFiles captures every file whose hooks the installer owns
// (installer.ExpectedHooks: each account's Claude settings, each Codex hooks
// file) plus the ownership ledger they reconcile against, PLUS the machine
// config files under the config directory (issue #24 finding 3): the
// candidate's own `install --yes` renames config.json -> pfm.config.json
// (the v0.74.0 migration). A rollback across that boundary runs the OLD
// binary, which reads only the legacy name — without these files restored
// first, it converges on defaults and tears down every MCP service the real
// config enabled, including the launch agent.
func snapshotUpdateOwnedFiles(runtime config.Runtime) ([]updateFileSnapshot, error) {
	home := runtime.Paths.Home
	candidates := []string{filepath.Join(filepath.Dir(installer.SourceRepoPath(home)), "settings-hook-ownership.json")}
	for _, hook := range installer.ExpectedHooks(home, runtime.Config) {
		candidates = append(candidates, hook.File)
	}
	if runtime.Config.Path != "" {
		configDir := filepath.Dir(runtime.Config.Path)
		for _, name := range []string{
			config.FileName,
			config.LegacyFileName,
			config.HarvesterFileName,
			config.LegacyBackupName,
		} {
			candidates = append(candidates, filepath.Join(configDir, name))
		}
	}
	seen := make(map[string]bool, len(candidates))
	snapshots := make([]updateFileSnapshot, 0, len(candidates))
	for _, candidate := range candidates {
		physical, err := filepath.EvalSymlinks(candidate)
		if errors.Is(err, fs.ErrNotExist) {
			physical = filepath.Clean(candidate)
		} else if err != nil {
			return nil, fmt.Errorf("resolve hook file %s: %w", candidate, err)
		}
		if seen[physical] {
			continue
		}
		seen[physical] = true
		content, mode, existed, err := readUpdateHookFile(physical)
		if err != nil {
			return nil, err
		}
		snapshots = append(
			snapshots,
			updateFileSnapshot{path: physical, before: content, beforeExisted: existed, beforeMode: mode},
		)
	}
	sort.Slice(snapshots, func(left, right int) bool { return snapshots[left].path < snapshots[right].path })
	return snapshots, nil
}

// recordUpdateHookAfter captures each file exactly as the candidate's install
// left it. A file that cannot be read keeps its error, and restore then
// refuses to touch it.
func recordUpdateHookAfter(snapshots []updateFileSnapshot) {
	for index := range snapshots {
		snapshot := &snapshots[index]
		snapshot.after, _, snapshot.afterExisted, snapshot.afterErr = readUpdateHookFile(snapshot.path)
	}
}

// restoreUpdateHookFiles returns each hook file to its pre-install bytes, but
// only while it still holds exactly what the candidate's install left: a file
// something else rewrote since — a live chat saving its settings — is never
// clobbered. It is named as residue instead — and when that residue still
// carries a hook of pfm's own shape naming a subcommand this binary does not
// implement (issue #24 finding 2: exactly what a rollback across the update
// this file's install just performed can leave stranded), the residue
// message names those entries so the operator's repair instruction is
// concrete rather than a bare "reconcile it by hand".
func restoreUpdateHookFiles(snapshots []updateFileSnapshot, home string, stderr io.Writer) error {
	var residue error
	for _, snapshot := range snapshots {
		current, _, existed, err := readUpdateHookFile(snapshot.path)
		if err != nil {
			residue = errors.Join(residue, err)
			continue
		}
		if existed == snapshot.beforeExisted && bytes.Equal(current, snapshot.before) {
			continue
		}
		if snapshot.afterErr != nil || existed != snapshot.afterExisted || !bytes.Equal(current, snapshot.after) {
			message := fmt.Sprintf(
				"hook file %s changed after the update's install wrote it; left as is — reconcile it by hand",
				snapshot.path,
			)
			if stranded := installer.UnknownPFMHookCommands(current, home); len(stranded) > 0 {
				message = fmt.Sprintf(
					"hook file %s changed after the update's install wrote it; left as is — it still carries %s; reconcile by hand or run pfm install --yes",
					snapshot.path,
					strings.Join(stranded, ", "),
				)
			}
			residue = errors.Join(residue, errors.New(message))
			continue
		}
		if snapshot.beforeExisted {
			err = atomicfile.Write(snapshot.path, snapshot.before, snapshot.beforeMode)
		} else {
			err = os.Remove(snapshot.path)
		}
		if err != nil {
			residue = errors.Join(residue, fmt.Errorf("restore hook file %s: %w", snapshot.path, err))
			continue
		}
		fmt.Fprintf(stderr, "pfm update: restored %s to its pre-update state\n", snapshot.path)
	}
	return residue
}

func readUpdateHookFile(path string) ([]byte, fs.FileMode, bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, fmt.Errorf("stat hook file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, 0, false, fmt.Errorf("hook file %s is not a regular file", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, false, fmt.Errorf("read hook file %s: %w", path, err)
	}
	return content, info.Mode().Perm(), true, nil
}

// updateConfigPathAfterInstall resolves the --config path the candidate's OWN
// install left behind (issue #24 finding 4): runtime.Config.Path was
// resolved BEFORE the migration renamed it inside the candidate process only.
// A non-ENOENT stat error on either candidate path (issue #24 F2 — EACCES,
// ENOTDIR, or anything else) is returned as an error, exactly like sibling
// readUpdateHookFile: it is never folded into the "gone/migrated" notes,
// which would misreport a stat failure as an absent file. The caller treats
// a returned error as a failed update step.
func updateConfigPathAfterInstall(runtime config.Runtime) (path, note string, err error) {
	original := runtime.Config.Path
	if original == "" {
		return "", "", nil
	}
	if _, statErr := os.Stat(original); statErr == nil {
		return original, "", nil
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return "", "", fmt.Errorf("stat config %s: %w", original, statErr)
	}
	migrated := filepath.Join(filepath.Dir(original), config.FileName)
	if _, statErr := os.Stat(migrated); statErr == nil {
		return migrated, fmt.Sprintf("config migrated by the update: %s → %s", original, migrated), nil
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return "", "", fmt.Errorf("stat migrated config %s: %w", migrated, statErr)
	}
	return "", fmt.Sprintf("config %s is gone after install and no %s replaced it", original, config.FileName), nil
}
