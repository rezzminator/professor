package apply

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"hostops/pfm/internal/dream/artifact"
)

var provenanceLinePattern = regexp.MustCompile(`^Provenance: [0-9]{4}-[0-9]{2}-[0-9]{2} · sid ([0-9a-f]{8})$`)

func restampMap(raw []byte, today, recordedTree string, git GitReader) ([]byte, error) {
	parsed, err := artifact.ParseMap(string(raw))
	if err != nil {
		return nil, fmt.Errorf("parse candidate map: %w", err)
	}
	rows := strings.Split(string(raw), "\n")
	anchorCount := 0
	provenanceCount := 0
	inAnchors := false
	for index, row := range rows {
		if row == "## Anchors" {
			inAnchors = true
			continue
		}
		if match := provenanceLinePattern.FindStringSubmatch(row); match != nil {
			rows[index] = "Provenance: " + today + " · sid " + match[1]
			provenanceCount++
			continue
		}
		if !inAnchors || row == "" {
			continue
		}
		anchor, parseErr := artifact.ParseAnchorRow(row)
		if parseErr != nil {
			return nil, fmt.Errorf("parse anchor during restamp: %w", parseErr)
		}
		object, found, resolveErr := git.Resolve(recordedTree, anchor.LookupPath)
		if resolveErr != nil {
			return nil, fmt.Errorf(
				"resolve anchor %s at recorded tree %s: %w",
				anchor.LookupPath,
				recordedTree,
				resolveErr,
			)
		}
		if !found {
			return nil, fmt.Errorf("anchor path absent at recorded tree: %s", anchor.LookupPath)
		}
		if !objectIDPattern.MatchString(object.Hash) {
			return nil, fmt.Errorf("anchor object has invalid id for %s", anchor.LookupPath)
		}
		if object.Type != artifact.GitBlob && object.Type != artifact.GitTree {
			return nil, fmt.Errorf("anchor object has invalid type for %s: %s", anchor.LookupPath, object.Type)
		}
		anchor.Hash = object.Hash[:12]
		anchor.ObjectType = object.Type
		rows[index] = artifact.RenderAnchorRow(anchor)
		anchorCount++
	}
	if provenanceCount != 1 {
		return nil, fmt.Errorf("restamp found %d Provenance lines", provenanceCount)
	}
	if anchorCount != len(parsed.Anchors) {
		return nil, fmt.Errorf("restamp found %d of %d parsed anchors", anchorCount, len(parsed.Anchors))
	}
	return []byte(strings.Join(rows, "\n")), nil
}

func readPrivateLine(path string) (string, error) {
	raw, err := readPrivate(path)
	if err != nil {
		return "", err
	}
	return exactLine(raw, filepath.Base(path))
}

func exactLine(raw []byte, label string) (string, error) {
	if len(raw) == 0 || raw[len(raw)-1] != '\n' || bytes.Count(raw, []byte{'\n'}) != 1 {
		return "", fmt.Errorf("%s is not exactly one newline-terminated line", label)
	}
	return string(raw[:len(raw)-1]), nil
}

func readPrivate(path string) ([]byte, error) {
	if err := validatePrivateFile(path, os.Getuid()); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read private stage file %s: %w", path, err)
	}
	return raw, nil
}

func validatePrivateFile(path string, ownerUID int) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect private stage file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("private stage file is not a regular non-symlink file: %s", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("private stage file owner is unavailable: %s", path)
	}
	if int(stat.Uid) != ownerUID {
		return fmt.Errorf("private stage file has the wrong owner: %s", path)
	}
	if info.Mode().Perm() != 0o600 {
		return fmt.Errorf("private stage file mode is not 0600: %s", path)
	}
	return nil
}

func readRegular(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect regular file %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("path is not a regular non-symlink file: %s", path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read regular file %s: %w", path, err)
	}
	return raw, nil
}

func rejectUnsafeReplaceTarget(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect derived stage target %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("derived stage target is unsafe to replace: %s", path)
	}
	return nil
}

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("inspect path %s: %w", path, err)
}

func writePrivateExclusive(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create private file %s: %w", path, err)
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(raw); err != nil {
		return fmt.Errorf("write private file %s: %w", path, err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync private file %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close private file %s: %w", path, err)
	}
	keep = true
	return nil
}

func copyPrivateExclusive(source, destination string) error {
	raw, err := readRegular(source)
	if err != nil {
		return err
	}
	return writePrivateExclusive(destination, raw)
}

func privateAtomicReplace(path string, raw []byte) error {
	directory := filepath.Dir(path)
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("atomic replace directory is not a real directory: %s", directory)
	}
	temporary, err := os.CreateTemp(directory, ".dream-apply-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		_ = temporary.Close()
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	keep = true
	return nil
}

type restorePoint struct {
	path    string
	existed bool
	raw     []byte
	mode    os.FileMode
}

func commit(
	repo artifact.RepoContext,
	layout artifact.StageLayout,
	before organState,
	prepared preparation,
) (err error) {
	agentsRoot := filepath.Join(repo.Organ, "agents")
	createdAgents := false
	if !before.agents {
		if err := os.Mkdir(agentsRoot, 0o700); err != nil {
			return fmt.Errorf("create organ agents directory: %w", err)
		}
		createdAgents = true
	}

	derivedPaths := make([]string, 0, len(prepared.derived))
	for path := range prepared.derived {
		derivedPaths = append(derivedPaths, path)
	}
	sort.Strings(derivedPaths)
	restores := make([]restorePoint, 0, len(derivedPaths))
	for _, path := range derivedPaths {
		point := restorePoint{path: path}
		info, statErr := os.Lstat(path)
		if statErr == nil {
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				if createdAgents {
					_ = os.Remove(agentsRoot)
				}
				return fmt.Errorf("derived organ target is not a regular non-symlink file: %s", path)
			}
			point.existed = true
			point.mode = info.Mode().Perm()
			point.raw, statErr = os.ReadFile(path)
		}
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			if createdAgents {
				_ = os.Remove(agentsRoot)
			}
			return fmt.Errorf("snapshot derived organ target %s: %w", path, statErr)
		}
		restores = append(restores, point)
	}

	created := make([]string, 0, len(prepared.appliedMaps)+len(prepared.archivedMaps)+2)
	explorerMoved := false
	rollback := func(cause error) error {
		var failures []error
		for index := len(restores) - 1; index >= 0; index-- {
			point := restores[index]
			if point.existed {
				if restoreErr := privateAtomicReplace(point.path, point.raw); restoreErr != nil {
					failures = append(failures, fmt.Errorf("restore %s: %w", point.path, restoreErr))
				} else if restoreErr := os.Chmod(point.path, point.mode); restoreErr != nil {
					failures = append(failures, fmt.Errorf("restore mode %s: %w", point.path, restoreErr))
				}
			} else if restoreErr := os.Remove(point.path); restoreErr != nil && !errors.Is(restoreErr, os.ErrNotExist) {
				failures = append(failures, fmt.Errorf("remove new derived target %s: %w", point.path, restoreErr))
			}
		}
		if explorerMoved {
			from := filepath.Join(repo.Organ, "archive", prepared.explorerArchive)
			to := filepath.Join(repo.Organ, "explorer-index.md")
			if restoreErr := os.Rename(from, to); restoreErr != nil {
				failures = append(failures, fmt.Errorf("restore legacy explorer surface: %w", restoreErr))
			}
		}
		for index := len(created) - 1; index >= 0; index-- {
			if restoreErr := os.Remove(created[index]); restoreErr != nil && !errors.Is(restoreErr, os.ErrNotExist) {
				failures = append(
					failures,
					fmt.Errorf("remove partial apply target %s: %w", created[index], restoreErr),
				)
			}
		}
		if createdAgents {
			if restoreErr := os.Remove(agentsRoot); restoreErr != nil && !errors.Is(restoreErr, os.ErrNotExist) {
				failures = append(failures, fmt.Errorf("remove partial agents directory: %w", restoreErr))
			}
		}
		return errors.Join(append([]error{cause}, failures...)...)
	}

	for _, mapPath := range prepared.appliedMaps {
		source := filepath.Join(prepared.root, "maps", filepath.Base(mapPath))
		target := filepath.Join(repo.Organ, mapPath)
		if err := copyPrivateExclusive(source, target); err != nil {
			return rollback(fmt.Errorf("install map %s: %w", mapPath, err))
		}
		created = append(created, target)
	}
	for _, archivePath := range prepared.archivedMaps {
		source := filepath.Join(prepared.root, "refuted", filepath.Base(archivePath))
		target := filepath.Join(repo.Organ, archivePath)
		if err := copyPrivateExclusive(source, target); err != nil {
			return rollback(fmt.Errorf("install refuted archive %s: %w", archivePath, err))
		}
		created = append(created, target)
	}
	if prepared.explorerArchive != "NONE" {
		source := filepath.Join(repo.Organ, "explorer-index.md")
		target := filepath.Join(repo.Organ, "archive", prepared.explorerArchive)
		if err := os.Rename(source, target); err != nil {
			return rollback(fmt.Errorf("archive legacy explorer surface: %w", err))
		}
		explorerMoved = true
	}
	for _, path := range derivedPaths {
		if err := privateAtomicReplace(path, prepared.derived[path]); err != nil {
			return rollback(fmt.Errorf("replace derived organ file %s: %w", path, err))
		}
	}
	sweepTarget := filepath.Join(repo.Organ, "dreamer", prepared.sweepTarget)
	if err := writePrivateExclusive(sweepTarget, prepared.sweepRaw); err != nil {
		return rollback(fmt.Errorf("install sweep %s: %w", prepared.sweepTarget, err))
	}
	created = append(created, sweepTarget)
	if err := writePrivateExclusive(filepath.Join(layout.Root, "APPLIED"), prepared.appliedRaw); err != nil {
		return rollback(fmt.Errorf("mark stage applied: %w", err))
	}
	return nil
}

// RestampBytes re-resolves every anchor row of one map at recordedTree and
// re-dates its Provenance line, in canonical row shape. It is the same
// mechanical stamp the apply path runs; the consult-time repair flow calls it
// through `pfm dream restamp` so agents never hand-write hashes.
func RestampBytes(raw []byte, today, recordedTree string, git GitReader) ([]byte, error) {
	return restampMap(raw, today, recordedTree, git)
}
