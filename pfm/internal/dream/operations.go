package dream

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"hostops/pfm/internal/dream/artifact"
	"hostops/pfm/internal/dream/lane"
	"hostops/pfm/internal/dream/organ"
	"hostops/pfm/internal/dream/resources"
)

// Inspect resolves and validates the repository boundary without changing it.
func Inspect(repoRoot, registryBase string) (artifact.RepoContext, organ.Shape, error) {
	repository, err := organ.Resolve(repoRoot, registryBase)
	if err != nil {
		return artifact.RepoContext{}, "", err
	}
	shape, err := organ.Validate(repository)
	if err != nil {
		return artifact.RepoContext{}, "", err
	}
	return repository, shape, nil
}

// RepositoryRoot resolves the repository that contains a working directory.
func RepositoryRoot(directory string) (string, error) {
	return organ.RepositoryRoot(directory)
}

// InspectLane gives --agent semantic validation while preserving inspect's
// legacy role as a repository-boundary probe: it resolves the lane through the
// same alias-aware resolver every other entry point uses, but does not require
// a profile or start a night.
func InspectLane(repoRoot, agentType, registryBase, resourcesRoot string) (
	artifact.RepoContext,
	organ.Shape,
	artifact.LaneContext,
	error,
) {
	repository, shape, err := Inspect(repoRoot, registryBase)
	if err != nil {
		return artifact.RepoContext{}, "", artifact.LaneContext{}, err
	}
	if err := validateResourcesRoot(resourcesRoot); err != nil {
		return artifact.RepoContext{}, "", artifact.LaneContext{}, err
	}
	laneName, err := lane.FromAgentTypeIn(
		agentType,
		resources.NewResources(resourcesRoot, repository.Organ),
	)
	if err != nil {
		return artifact.RepoContext{}, "", artifact.LaneContext{}, err
	}
	return repository, shape, artifact.LaneContext{AgentType: agentType, Lane: laneName}, nil
}

func validateResourcesRoot(root string) error {
	if root == "" {
		return nil
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return fmt.Errorf("dream resources root must be absolute and canonical: %s", root)
	}
	return nil
}

type MigrationResult struct {
	Organ     string
	Maps      int
	Rewritten int
	Rows      int
	Files     []MigrationFileResult
}

type MigrationFileOutcome string

const (
	MigrationRewritten  MigrationFileOutcome = "REWRITTEN"
	MigrationUnchanged  MigrationFileOutcome = "UNCHANGED"
	MigrationSkipped    MigrationFileOutcome = "SKIPPED"
	MigrationRejected   MigrationFileOutcome = "REJECTED"
	MigrationNotWritten MigrationFileOutcome = "NOT-WRITTEN"
)

type MigrationFileResult struct {
	MapPath string
	Outcome MigrationFileOutcome
	Rows    int
	Reason  string
}

type migrationRewrite struct {
	path       string
	before     []byte
	after      []byte
	translated int
	outcome    int
}

func RenderMigrationResult(result MigrationResult) string {
	return RenderMigrationOutcomes(result) + fmt.Sprintf(
		"MIGRATE PASS organ=%s maps=%d rewritten=%d rows=%d\n",
		result.Organ,
		result.Maps,
		result.Rewritten,
		result.Rows,
	)
}

func RenderMigrationOutcomes(result MigrationResult) string {
	var output strings.Builder
	for _, file := range result.Files {
		fmt.Fprintf(
			&output,
			"MIGRATE FILE path=%s outcome=%s rows=%d",
			file.MapPath,
			file.Outcome,
			file.Rows,
		)
		if file.Reason != "" {
			fmt.Fprintf(&output, " reason=%q", file.Reason)
		}
		output.WriteByte('\n')
	}
	return output.String()
}

var legacyAnchorPattern = regexp.MustCompile(
	"^[-] `([^`]+)` — `git log -1`: `[0-9a-f]{12}` \\([0-9]{4}-[0-9]{2}-[0-9]{2}\\); (blob|tree) `([0-9a-f]{12})`$",
)

// MigrateAnchors performs the legacy anchor translation textually. It keeps
// the recorded object type and hash, so migration cannot erase existing drift
// by consulting today's repository. All candidate maps are parsed before the
// first replacement and a failed replacement rolls earlier files back.
func MigrateAnchors(organRoot string) (result MigrationResult, returnErr error) {
	result = MigrationResult{Organ: organRoot}
	if _, err := organ.RootFromOrgan(organRoot); err != nil {
		return result, err
	}
	release, err := acquireRunnerLock(organRoot)
	if err != nil {
		return result, err
	}
	defer func() {
		if releaseErr := release(); releaseErr != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("release Dreamer runner lock: %w", releaseErr))
		}
	}()
	mapsRoot := filepath.Join(organRoot, "maps")
	info, err := os.Lstat(mapsRoot)
	if err != nil {
		return result, fmt.Errorf("inspect maps directory %s: %w", mapsRoot, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return result, fmt.Errorf("maps path is not a real directory: %s", mapsRoot)
	}
	entries, err := os.ReadDir(mapsRoot)
	if err != nil {
		return result, fmt.Errorf("enumerate maps for migration: %w", err)
	}

	var rewrites []migrationRewrite
	var preflightErrors []error
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(mapsRoot, entry.Name())
		mapPath := filepath.ToSlash(filepath.Join("maps", entry.Name()))
		if !artifact.ValidMapFilename(entry.Name()) {
			result.Files = append(result.Files, MigrationFileResult{
				MapPath: mapPath,
				Outcome: MigrationSkipped,
				Reason:  "invalid map filename",
			})
			continue
		}
		result.Maps++
		fileInfo, err := os.Lstat(path)
		if err != nil {
			result.Files = append(result.Files, MigrationFileResult{
				MapPath: mapPath,
				Outcome: MigrationRejected,
				Reason:  "inspect map: " + err.Error(),
			})
			preflightErrors = append(
				preflightErrors,
				fmt.Errorf("migration outcome REJECTED for %s: inspect map: %w", mapPath, err),
			)
			continue
		}
		if fileInfo.Mode()&os.ModeSymlink != 0 || !fileInfo.Mode().IsRegular() {
			result.Files = append(result.Files, MigrationFileResult{
				MapPath: mapPath,
				Outcome: MigrationRejected,
				Reason:  "map is not a regular non-symlink file",
			})
			preflightErrors = append(
				preflightErrors,
				fmt.Errorf("migration outcome REJECTED for %s: map is not a regular non-symlink file", mapPath),
			)
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			result.Files = append(result.Files, MigrationFileResult{
				MapPath: mapPath,
				Outcome: MigrationRejected,
				Reason:  "read map: " + err.Error(),
			})
			preflightErrors = append(
				preflightErrors,
				fmt.Errorf("migration outcome REJECTED for %s: read map: %w", mapPath, err),
			)
			continue
		}
		rows := strings.Split(string(raw), "\n")
		translated := 0
		for index, row := range rows {
			match := legacyAnchorPattern.FindStringSubmatch(row)
			if match == nil {
				continue
			}
			rows[index] = "- `" + match[1] + "` — " + match[2] + " `" + match[3] + "`"
			translated++
		}
		after := []byte(strings.Join(rows, "\n"))
		if _, err := artifact.ParseMap(string(after)); err != nil {
			result.Files = append(result.Files, MigrationFileResult{
				MapPath: mapPath,
				Outcome: MigrationRejected,
				Reason:  err.Error(),
			})
			preflightErrors = append(
				preflightErrors,
				fmt.Errorf("migration outcome REJECTED for %s: migrated map does not parse: %w", mapPath, err),
			)
			continue
		}
		if translated > 0 {
			result.Files = append(result.Files, MigrationFileResult{
				MapPath: mapPath,
				Outcome: MigrationNotWritten,
				Rows:    translated,
				Reason:  "validated; write pending",
			})
			rewrites = append(rewrites, migrationRewrite{
				path: path, before: raw, after: after, translated: translated,
				outcome: len(result.Files) - 1,
			})
			continue
		}
		result.Files = append(result.Files, MigrationFileResult{
			MapPath: mapPath,
			Outcome: MigrationUnchanged,
		})
	}
	sort.Slice(rewrites, func(left, right int) bool { return rewrites[left].path < rewrites[right].path })
	if len(preflightErrors) != 0 {
		markMigrationNotWritten(&result, "migration aborted by rejected map")
		return result, errors.Join(preflightErrors...)
	}

	replaced := make([]migrationRewrite, 0, len(rewrites))
	for _, candidate := range rewrites {
		if err := atomicPrivateReplace(candidate.path, candidate.after); err != nil {
			rollbackErrors := rollbackMigration(&result, replaced, atomicPrivateReplace)
			result.Files[candidate.outcome].Outcome = MigrationRejected
			result.Files[candidate.outcome].Reason = "atomic replacement failed"
			markMigrationNotWritten(&result, "migration aborted during atomic replacement")
			return result, errors.Join(
				append(
					[]error{
						fmt.Errorf(
							"migration outcome REJECTED for %s: replace migrated map: %w",
							result.Files[candidate.outcome].MapPath,
							err,
						),
					},
					rollbackErrors...)...)
		}
		result.Files[candidate.outcome].Outcome = MigrationRewritten
		result.Files[candidate.outcome].Reason = ""
		result.Rewritten++
		result.Rows += candidate.translated
		replaced = append(replaced, candidate)
	}
	return result, nil
}

func rollbackMigration(
	result *MigrationResult,
	replaced []migrationRewrite,
	replace func(string, []byte) error,
) []error {
	var rollbackErrors []error
	for index := len(replaced) - 1; index >= 0; index-- {
		rolledBack := replaced[index]
		if rollbackErr := replace(rolledBack.path, rolledBack.before); rollbackErr != nil {
			result.Files[rolledBack.outcome].Reason = "rollback failed: " + rollbackErr.Error()
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore %s: %w", rolledBack.path, rollbackErr))
			continue
		}
		result.Files[rolledBack.outcome].Outcome = MigrationNotWritten
		result.Files[rolledBack.outcome].Reason = "rolled back after later replacement failure"
		result.Rewritten--
		result.Rows -= rolledBack.translated
	}
	return rollbackErrors
}

func markMigrationNotWritten(result *MigrationResult, reason string) {
	for index := range result.Files {
		if result.Files[index].Outcome == MigrationNotWritten {
			result.Files[index].Reason = reason
		}
	}
}

func atomicPrivateReplace(path string, raw []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".dream-migrate-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	clean := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	if err := temporary.Chmod(0o600); err != nil {
		clean()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		clean()
		return err
	}
	if err := temporary.Sync(); err != nil {
		clean()
		return err
	}
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		_ = os.Remove(temporaryPath)
		return err
	}
	return nil
}
