package installer

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"hostops/pfm/internal/atomicfile"
)

const (
	memoryRepoTemplateLine = `REPO="${CLAUDE_MEMORY_REPO:-$HOME/work/{MEMORY_VAULT_DIR}}"`
	memoryRepoLinePrefix   = `REPO="${CLAUDE_MEMORY_REPO:-$HOME/work/`
	memoryRepoLineSuffix   = `}"`
)

type memoryHelperMigration struct {
	oldPath           string
	newPath           string
	content           []byte
	mode              fs.FileMode
	createDestination bool
	aliases           []string
}

type memoryHelperSettingsRewrite struct {
	path    string
	content []byte
	mode    fs.FileMode
}

var retiredMemoryHelpers = []struct {
	oldName          string
	newName          string
	normalizedSHA256 string
}{
	{
		oldName:          "cc-memory-wire.sh",
		newName:          "memory-wire.sh",
		normalizedSHA256: "ed3d1028ec8299e84d42c2fcd2e9797e64b6213116631cf1d9534df27c36c0a6",
	},
	{
		oldName:          "cc-memory-consolidate.sh",
		newName:          "memory-consolidate.sh",
		normalizedSHA256: "d7d1672964f35cb2cd5a41a1ed38d5d3f5a2f1a37a4ba530d9e75bc48e6538a7",
	},
}

// migrateMemoryHelpers retires the optional memory helpers' old cc-prefixed
// filenames without enabling memory backup on a host that never installed it.
// An exact historical-template fingerprint is the ownership proof: a custom
// script at the same basename is a conflict, never an installer asset.
func (installer *engine) migrateMemoryHelpers() error {
	migrations, err := installer.planMemoryHelperMigrations()
	if err != nil {
		return err
	}
	if len(migrations) == 0 {
		installer.ok("retired memory helper filenames absent")
		return nil
	}

	hookPaths := make(map[string]string)
	for _, migration := range migrations {
		if filepath.Base(migration.oldPath) == "cc-memory-wire.sh" {
			hookPaths[migration.oldPath] = migration.newPath
			for _, alias := range migration.aliases {
				hookPaths[alias] = filepath.Join(filepath.Dir(alias), filepath.Base(migration.newPath))
			}
			physical, resolveErr := filepath.EvalSymlinks(migration.oldPath)
			if resolveErr != nil {
				return fmt.Errorf(
					"resolve retired memory helper %s for hook aliases: %w",
					migration.oldPath,
					resolveErr,
				)
			}
			// A seat directory alias is visited only once, but settings may still
			// spell the helper through any configured lexical path.
			lexicalDirs := []string{filepath.Join(installer.options.Home, ".claude"), installer.options.ConfigDir}
			lexicalDirs = append(lexicalDirs, installer.options.ConfigDirs...)
			for _, dir := range lexicalDirs {
				if strings.TrimSpace(dir) == "" {
					continue
				}
				oldPath := filepath.Join(filepath.Clean(dir), "scripts", filepath.Base(migration.oldPath))
				resolved, aliasErr := filepath.EvalSymlinks(oldPath)
				if errors.Is(aliasErr, fs.ErrNotExist) {
					continue
				}
				if aliasErr != nil {
					return fmt.Errorf("resolve retired memory helper hook alias %s: %w", oldPath, aliasErr)
				}
				if filepath.Clean(resolved) == filepath.Clean(physical) {
					hookPaths[oldPath] = filepath.Join(filepath.Dir(oldPath), filepath.Base(migration.newPath))
				}
			}
		}
	}
	settings, err := installer.planMemoryHelperSettingsRewrites(hookPaths)
	if err != nil {
		return err
	}

	// New helpers land first. If any copy fails, every old hook and helper is
	// still intact. Settings then move to paths that are known to exist; old
	// helpers are removed only after every settings rewrite succeeds.
	for _, migration := range migrations {
		if !migration.createDestination {
			installer.ok("memory helper destination already matches " + migration.newPath)
			continue
		}
		migration := migration
		if err := installer.change("migrate memory helper "+migration.oldPath+" -> "+migration.newPath, func() error {
			if err := atomicfile.Write(migration.newPath, migration.content, migration.mode); err != nil {
				return fmt.Errorf("copy owned memory helper %s to %s: %w", migration.oldPath, migration.newPath, err)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	for _, rewrite := range settings {
		rewrite := rewrite
		if err := installer.change(
			"rewrite memory helper hook path in "+rewrite.path+" (backup preserved)",
			func() error {
				backup := availableBackup(rewrite.path, installer.stamp)
				if err := copyBackup(rewrite.path, backup); err != nil {
					return fmt.Errorf(
						"backup settings before memory helper hook migration %s to %s: %w",
						rewrite.path,
						backup,
						err,
					)
				}
				if err := atomicfile.Write(rewrite.path, rewrite.content, rewrite.mode); err != nil {
					return fmt.Errorf("rewrite memory helper hook path in %s: %w", rewrite.path, err)
				}
				return nil
			},
		); err != nil {
			return err
		}
	}
	for _, migration := range migrations {
		migration := migration
		if err := installer.change("remove migrated memory helper "+migration.oldPath, func() error {
			if err := os.Remove(migration.oldPath); err != nil {
				return fmt.Errorf("remove migrated memory helper %s: %w", migration.oldPath, err)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func (installer *engine) planMemoryHelperMigrations() ([]memoryHelperMigration, error) {
	var migrations []memoryHelperMigration
	physical := make(map[string]int)
	for _, configDir := range installer.memoryHelperConfigDirs() {
		for _, helper := range retiredMemoryHelpers {
			oldPath := filepath.Join(configDir, "scripts", helper.oldName)
			info, err := os.Lstat(oldPath)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("inspect retired memory helper %s: %w", oldPath, err)
			}
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf(
					"refuse to migrate unowned memory helper %s: expected a regular file, found mode %s",
					oldPath,
					info.Mode(),
				)
			}
			resolved, err := filepath.EvalSymlinks(oldPath)
			if err != nil {
				return nil, fmt.Errorf("resolve retired memory helper %s: %w", oldPath, err)
			}
			if index, exists := physical[resolved]; exists {
				migrations[index].aliases = append(migrations[index].aliases, oldPath)
				continue
			}
			content, err := os.ReadFile(oldPath)
			if err != nil {
				return nil, fmt.Errorf("read retired memory helper %s: %w", oldPath, err)
			}
			fingerprint, err := normalizedMemoryHelperFingerprint(content)
			if err != nil {
				return nil, fmt.Errorf("refuse to migrate unowned memory helper %s: %w", oldPath, err)
			}
			if fingerprint != helper.normalizedSHA256 {
				return nil, fmt.Errorf(
					"refuse to migrate unowned memory helper %s: content differs from the canonical Professor helper",
					oldPath,
				)
			}

			newPath := filepath.Join(configDir, "scripts", helper.newName)
			createDestination := false
			destinationInfo, err := os.Lstat(newPath)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				createDestination = true
			case err != nil:
				return nil, fmt.Errorf("inspect memory helper destination %s: %w", newPath, err)
			case !destinationInfo.Mode().IsRegular():
				return nil, fmt.Errorf(
					"refuse to overwrite memory helper destination %s: expected a regular file, found mode %s",
					newPath,
					destinationInfo.Mode(),
				)
			default:
				destination, readErr := os.ReadFile(newPath)
				if readErr != nil {
					return nil, fmt.Errorf("read memory helper destination %s: %w", newPath, readErr)
				}
				if !bytes.Equal(destination, content) {
					return nil, fmt.Errorf(
						"refuse to overwrite memory helper destination %s: content conflicts with owned source %s",
						newPath,
						oldPath,
					)
				}
				if destinationInfo.Mode().Perm() != info.Mode().Perm() {
					return nil, fmt.Errorf(
						"refuse to overwrite memory helper destination %s: mode %o conflicts with owned source %s mode %o",
						newPath,
						destinationInfo.Mode().Perm(),
						oldPath,
						info.Mode().Perm(),
					)
				}
			}
			physical[resolved] = len(migrations)
			migrations = append(migrations, memoryHelperMigration{
				oldPath: oldPath, newPath: newPath, content: content,
				mode: info.Mode().Perm(), createDestination: createDestination,
			})
		}
	}
	return migrations, nil
}

func (installer *engine) planMemoryHelperSettingsRewrites(
	hookPaths map[string]string,
) ([]memoryHelperSettingsRewrite, error) {
	if len(hookPaths) == 0 {
		return nil, nil
	}
	var rewrites []memoryHelperSettingsRewrite
	physical := make(map[string]bool)
	for _, configDir := range installer.memoryHelperConfigDirs() {
		for _, filename := range []string{"settings.json", "settings.local.json"} {
			path := filepath.Join(configDir, filename)
			info, err := os.Lstat(path)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("inspect settings for memory helper hook migration %s: %w", path, err)
			}
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("refuse to migrate memory helper hooks in non-regular settings file %s", path)
			}
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return nil, fmt.Errorf("resolve settings for memory helper hook migration %s: %w", path, err)
			}
			if physical[resolved] {
				continue
			}
			physical[resolved] = true
			raw, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read settings for memory helper hook migration %s: %w", path, err)
			}
			updated, changed, err := rewriteMemoryHelperHookPaths(raw, hookPaths, installer.options.Home)
			if err != nil {
				return nil, fmt.Errorf("parse settings for memory helper hook migration %s: %w", path, err)
			}
			if changed {
				rewrites = append(
					rewrites,
					memoryHelperSettingsRewrite{path: path, content: updated, mode: info.Mode().Perm()},
				)
			}
		}
	}
	return rewrites, nil
}

func (installer *engine) memoryHelperConfigDirs() []string {
	dirs := append([]string{filepath.Join(installer.options.Home, ".claude")}, installer.seatConfigDirs()...)
	return dedupePhysicalDirs(dirs)
}

func normalizedMemoryHelperFingerprint(content []byte) (string, error) {
	lines := bytes.Split(content, []byte("\n"))
	repoLines := 0
	for index, rawLine := range lines {
		line := string(rawLine)
		if !strings.HasPrefix(line, "REPO=") {
			continue
		}
		repoLines++
		if !strings.HasPrefix(line, memoryRepoLinePrefix) || !strings.HasSuffix(line, memoryRepoLineSuffix) {
			return "", fmt.Errorf("REPO line is not a placeholder-only vault path substitution")
		}
		value := strings.TrimSuffix(strings.TrimPrefix(line, memoryRepoLinePrefix), memoryRepoLineSuffix)
		if value == "" || (value != "{MEMORY_VAULT_DIR}" && strings.ContainsAny(value, "\"${}`\\")) {
			return "", fmt.Errorf("REPO line is not a placeholder-only vault path substitution")
		}
		lines[index] = []byte(memoryRepoTemplateLine)
	}
	if repoLines != 1 {
		return "", fmt.Errorf("expected exactly one canonical REPO line, found %d", repoLines)
	}
	normalized := bytes.Join(lines, []byte("\n"))
	digest := sha256.Sum256(normalized)
	return fmt.Sprintf("%x", digest), nil
}
