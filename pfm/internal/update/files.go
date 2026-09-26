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

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

// updateFileKind names what an installer-owned file is to the operator: its
// residue says which, and only a hook file is checked for stranded pfm hooks.
type updateFileKind string

const (
	updateHookFile        updateFileKind = "hook file"
	updateConfigFile      updateFileKind = "config file"
	updateMCPRegistration updateFileKind = "MCP registration"
)

// updateFileSnapshot is one installer-owned file captured around the
// candidate's `install --yes`: its bytes before (the state rollback returns
// to) and right after (the only state rollback may overwrite).
type updateFileSnapshot struct {
	path          string // physical path: a symlinked account settings file is written through, never replaced
	kind          updateFileKind
	before        []byte
	beforeExisted bool
	beforeMode    fs.FileMode
	after         []byte
	afterExisted  bool
	afterErr      error
}

// snapshotUpdateOwnedFiles captures every file whose hooks the installer owns
// (installer.ExpectedHooks: each account's Claude settings) plus the
// ownership ledger they reconcile against, PLUS the machine config files
// under the config directory (issue #24 finding 3): the candidate's own
// `install --yes` renames config.json -> pfm.config.json (the v0.74.0
// migration). A rollback across that boundary runs the OLD binary, which
// reads only the legacy name — without these files restored first, it
// converges on defaults and tears down every MCP service the real config
// enabled, including the launch agent. It also captures every MCP
// registration install rewrites — each Claude user registry, $HOME/.claude.json, ~/.mcp.json,
// each Codex home's config.toml, the OpenCode config and the MCP ownership
// ledger — so a rollback never leaves the candidate's registrations behind.
func snapshotUpdateOwnedFiles(runtime config.Runtime) ([]updateFileSnapshot, error) {
	home := runtime.Paths.Home
	type candidate struct {
		path string
		kind updateFileKind
	}
	managedRoot := filepath.Dir(installer.SourceRepoPath(home))
	candidates := []candidate{{filepath.Join(managedRoot, "settings-hook-ownership.json"), updateHookFile}}
	for _, hook := range installer.ExpectedHooks(home, runtime.Config) {
		candidates = append(candidates, candidate{hook.File, updateHookFile})
	}
	if runtime.Config.Path != "" {
		configDir := filepath.Dir(runtime.Config.Path)
		for _, name := range []string{
			config.FileName,
			config.LegacyFileName,
			config.HarvesterFileName,
			config.LegacyBackupName,
		} {
			candidates = append(candidates, candidate{filepath.Join(configDir, name), updateConfigFile})
		}
	}
	for _, registry := range installer.ClaudeUserRegistries(
		home,
		runtime.Config.Accounts,
		config.AmbientClaudeConfigDir(),
	) {
		candidates = append(candidates, candidate{registry.Path, updateMCPRegistration})
	}
	// $HOME/.claude.json is rewritten even when no account wires it (install
	// sweeps pfm's legacy entries there), so it is captured either way.
	candidates = append(
		candidates,
		candidate{filepath.Join(home, ".claude.json"), updateMCPRegistration},
		candidate{filepath.Join(home, ".mcp.json"), updateMCPRegistration},
	)
	for _, codexHome := range runtime.Config.CodexHomes() {
		candidates = append(candidates, candidate{filepath.Join(codexHome, "config.toml"), updateMCPRegistration})
	}
	candidates = append(
		candidates,
		candidate{installer.OpenCodeConfigPath(home), updateMCPRegistration},
		candidate{filepath.Join(managedRoot, "mcp-ownership.json"), updateMCPRegistration},
	)
	seen := make(map[string]bool, len(candidates))
	snapshots := make([]updateFileSnapshot, 0, len(candidates))
	for _, candidate := range candidates {
		physical, err := filepath.EvalSymlinks(candidate.path)
		if errors.Is(err, fs.ErrNotExist) {
			physical = filepath.Clean(candidate.path)
		} else if err != nil {
			return nil, fmt.Errorf("resolve %s %s: %w", candidate.kind, candidate.path, err)
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
			updateFileSnapshot{
				path: physical, kind: candidate.kind, before: content, beforeExisted: existed, beforeMode: mode,
			},
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

// restoreUpdateHookFiles returns each snapshotted file to its pre-install bytes, but
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
			residue = errors.Join(residue, errors.New(updateResidueMessage(snapshot, current, existed, home)))
			continue
		}
		if snapshot.beforeExisted {
			err = atomicfile.Write(snapshot.path, snapshot.before, snapshot.beforeMode)
		} else {
			err = os.Remove(snapshot.path)
		}
		if err != nil {
			residue = errors.Join(residue, fmt.Errorf("restore %s %s: %w", snapshot.kind, snapshot.path, err))
			continue
		}
		fmt.Fprintf(stderr, "pfm update: restored %s to its pre-update state\n", snapshot.path)
	}
	return residue
}

// updateResidueMessage names a file something rewrote after the update's
// install wrote it. A hook file is also checked for stranded pfm hooks: one
// that carries them names them, and one that does not parse is named as
// unchecked with its parse error — never read as clean. A file removed since
// has nothing to check and says so.
func updateResidueMessage(snapshot updateFileSnapshot, current []byte, existed bool, home string) string {
	prefix := fmt.Sprintf("%s %s changed after the update's install wrote it; left as is", snapshot.kind, snapshot.path)
	if !existed {
		return fmt.Sprintf("%s %s was removed after the update's install wrote it; reconcile it by hand",
			snapshot.kind, snapshot.path)
	}
	if snapshot.kind != updateHookFile {
		return prefix + " — reconcile it by hand"
	}
	stranded, err := installer.UnknownPFMHookCommands(current, home)
	switch {
	case err != nil:
		return fmt.Sprintf(
			"%s — it does not parse (%v), so pfm could not check it for stranded pfm hooks; reconcile it by hand",
			prefix,
			err,
		)
	case len(stranded) > 0:
		return fmt.Sprintf(
			"%s — it still carries %s; reconcile by hand or run pfm install --yes",
			prefix,
			strings.Join(stranded, ", "),
		)
	default:
		return prefix + " — reconcile it by hand"
	}
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
