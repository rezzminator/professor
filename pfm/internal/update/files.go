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

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

type updateFileKind string

const (
	updateHookFile        updateFileKind = "hook file"
	updateConfigFile      updateFileKind = "config file"
	updateMCPRegistration updateFileKind = "MCP registration"
)

// updateFileSnapshot captures the states rollback returns to and may overwrite.
type updateFileSnapshot struct {
	path          string // physical path: restore through an account's symlink, preserving the link
	kind          updateFileKind
	before        []byte
	beforeExisted bool
	beforeMode    fs.FileMode

	after        []byte
	afterExisted bool
	afterErr     error
	afterPath    string
	afterObject  fs.FileInfo
	afterTarget  fs.FileInfo
}

func snapshotUpdateOwnedFiles(runtime config.Runtime) ([]updateFileSnapshot, error) {
	home := runtime.Paths.Home
	type candidate struct {
		path string
		kind updateFileKind
	}
	managedRoot := installer.ManagedRoot(home)
	candidates := []candidate{
		{filepath.Join(managedRoot, "settings-hook-ownership.json"), updateHookFile},
	}
	for _, codexHome := range runtime.Config.CodexHomes() {
		candidates = append(candidates, candidate{filepath.Join(codexHome, "hooks.json"), updateHookFile})
	}
	if runtime.Config.Path != "" {
		candidates = append(candidates, candidate{runtime.Config.Path, updateConfigFile})
	}
	for _, registry := range installer.ClaudeUserRegistries(
		home,
		runtime.Config.Accounts,
		config.AmbientClaudeConfigDir(),
	) {
		candidates = append(candidates, candidate{registry.Path, updateMCPRegistration})
	}
	for _, codexHome := range runtime.Config.CodexHomes() {
		candidates = append(candidates,
			candidate{filepath.Join(codexHome, "config.toml"), updateMCPRegistration},
			candidate{filepath.Join(codexHome, ".professor-hook-trust.json"), updateMCPRegistration},
		)
	}
	candidates = append(candidates,
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
		snapshot := updateFileSnapshot{path: physical, kind: candidate.kind}
		snapshot.before, snapshot.beforeMode, snapshot.beforeExisted, err = snapshot.readOwnedFile()
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	sort.Slice(snapshots, func(left, right int) bool { return snapshots[left].path < snapshots[right].path })
	return snapshots, nil
}

// recordUpdateOwnedFilesAfter records unreadable states as errors, so rollback
// cannot overwrite a file whose candidate state could not be captured.
func recordUpdateOwnedFilesAfter(snapshots []updateFileSnapshot) {
	for index := range snapshots {
		snapshot := &snapshots[index]
		snapshot.afterPath = snapshot.path
		snapshot.after, _, snapshot.afterExisted, snapshot.afterErr = snapshot.readOwnedFile()
		if snapshot.afterExisted && snapshot.afterErr == nil {
			snapshot.afterPath, snapshot.afterObject, snapshot.afterTarget, snapshot.afterErr = updateFileIdentity(
				snapshot.path,
			)
		}
	}
}

// restoreUpdateOwnedFiles preserves changes made after the candidate's install.
func restoreUpdateOwnedFiles(snapshots []updateFileSnapshot, stderr io.Writer) error {
	var residue error
	for _, snapshot := range snapshots {
		current, _, existed, err := snapshot.readOwnedFile()
		if err != nil {
			residue = errors.Join(residue, err)
			continue
		}
		if snapshot.afterErr == nil && snapshot.afterExisted == snapshot.beforeExisted &&
			bytes.Equal(snapshot.after, snapshot.before) {
			continue
		}
		physical, object, target, identityErr := updateFileIdentity(snapshot.path)
		if identityErr != nil || (snapshot.afterExisted &&
			(physical != snapshot.afterPath || object == nil || target == nil ||
				!os.SameFile(object, snapshot.afterObject) || !os.SameFile(target, snapshot.afterTarget))) {
			residue = errors.Join(residue, errors.New(updateResidueMessage(snapshot, existed)), identityErr)
			continue
		}
		if snapshot.afterErr != nil || existed != snapshot.afterExisted || !bytes.Equal(current, snapshot.after) {
			residue = errors.Join(residue, errors.New(updateResidueMessage(snapshot, existed)))
			continue
		}
		if snapshot.beforeExisted {
			err = atomicfile.Write(snapshot.afterPath, snapshot.before, snapshot.beforeMode)
		} else {
			err = os.Remove(snapshot.afterPath)
		}
		if err != nil {
			residue = errors.Join(residue, fmt.Errorf("restore %s %s: %w", snapshot.kind, snapshot.path, err))
			continue
		}
		fmt.Fprintf(stderr, "pfm update: restored %s to its pre-update state\n", snapshot.path)
	}
	return residue
}

// updateFileIdentity records both the path object and its physical target;
// equal bytes alone cannot prove the candidate still owns either object.
func updateFileIdentity(path string) (string, fs.FileInfo, fs.FileInfo, error) {
	object, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return path, nil, nil, nil
	}
	if err != nil {
		return "", nil, nil, fmt.Errorf("inspect rollback object %s: %w", path, err)
	}
	physical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", object, nil, fmt.Errorf("resolve rollback object %s: %w", path, err)
	}
	target, err := os.Stat(physical)
	if err != nil {
		return physical, object, nil, fmt.Errorf("inspect rollback target %s: %w", physical, err)
	}
	return physical, object, target, nil
}

func updateResidueMessage(snapshot updateFileSnapshot, existed bool) string {
	if !existed {
		return fmt.Sprintf("%s %s was removed after the update's install wrote it; reconcile it by hand",
			snapshot.kind, snapshot.path)
	}
	return fmt.Sprintf("%s %s changed after the update's install wrote it; left as is — reconcile it by hand",
		snapshot.kind, snapshot.path)
}

func (snapshot updateFileSnapshot) readOwnedFile() ([]byte, fs.FileMode, bool, error) {
	content, mode, existed, err := readUpdateOwnedFile(snapshot.path)
	if err == nil {
		return content, mode, existed, nil
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return nil, 0, false, fmt.Errorf("%s %s %s: %w", pathErr.Op, snapshot.kind, snapshot.path, pathErr.Err)
	}
	return nil, 0, false, fmt.Errorf("%s %w", snapshot.kind, err)
}

func readUpdateOwnedFile(path string) ([]byte, fs.FileMode, bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, false, nil
	}
	if err != nil {
		return nil, 0, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, 0, false, fmt.Errorf("%s is not a regular file", path)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, false, &fs.PathError{Op: "read", Path: path, Err: err}
	}
	return content, info.Mode().Perm(), true, nil
}
