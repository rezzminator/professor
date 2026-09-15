package organ

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"

	"hostops/pfm/internal/dream/artifact"
)

var lanePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ScratchRoot is the organ's single transient tree. Every artifact a night
// writes on the way to a dream lives below it and nothing else does, so one
// gitignore line covers the whole class and a new scratch kind cannot leak
// into the ledger by being born outside the ignored set.
func ScratchRoot(organRoot string) string {
	return filepath.Join(organRoot, "tmp")
}

// NewStage creates one private stage. Log paths are derived in the returned
// layout but remain absent until CreateLogs is called after a non-empty corpus
// is proven. The caller supplies the timestamp so stage naming remains
// deterministic under test; a random MkdirTemp suffix separates concurrent
// starts.
func NewStage(context artifact.RepoContext, lane string, startedAt time.Time) (artifact.StageLayout, error) {
	if _, err := Validate(context); err != nil {
		return artifact.StageLayout{}, fmt.Errorf("validate organ before creating stage: %w", err)
	}
	if !lanePattern.MatchString(lane) {
		return artifact.StageLayout{}, fmt.Errorf("lane is not lowercase kebab: %s", lane)
	}
	if startedAt.IsZero() {
		return artifact.StageLayout{}, errors.New("stage start time must not be zero")
	}

	scratchRoot := ScratchRoot(context.Organ)
	if err := ensurePrivateDirectory(scratchRoot); err != nil {
		return artifact.StageLayout{}, err
	}
	stagingRoot := filepath.Join(scratchRoot, "staging")
	if err := ensurePrivateDirectory(stagingRoot); err != nil {
		return artifact.StageLayout{}, err
	}
	prefix := lane + "-" + startedAt.UTC().Format("20060102T150405") + "."
	root, err := os.MkdirTemp(stagingRoot, prefix)
	if err != nil {
		return artifact.StageLayout{}, fmt.Errorf("create stage below %s: %w", stagingRoot, err)
	}
	layout := layoutFromRoot(context, root)
	cleanup := func(cause error) error {
		if err := os.RemoveAll(root); err != nil {
			return errors.Join(cause, fmt.Errorf("remove incomplete new stage %s: %w", root, err))
		}
		return cause
	}
	for _, directory := range []string{layout.Maps, layout.Meta} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			return artifact.StageLayout{}, cleanup(fmt.Errorf("create private stage directory %s: %w", directory, err))
		}
	}
	validated, err := ValidateStage(context, root)
	if err != nil {
		return artifact.StageLayout{}, cleanup(fmt.Errorf("validate new stage: %w", err))
	}
	return validated, nil
}

// CreateLogs creates both private run logs after corpus enumeration proves the
// run is non-empty. O_EXCL makes a stale or colliding log a failure rather than
// an accidental append target. If the second creation fails, the first file
// from this call is removed and any pre-existing artifact is left untouched.
func CreateLogs(context artifact.RepoContext, stageRoot string) error {
	layout, err := ValidateStage(context, stageRoot)
	if err != nil {
		return err
	}
	logsRoot := filepath.Dir(layout.HumanLog)
	if err := ensurePrivateDirectory(logsRoot); err != nil {
		return err
	}
	created := make([]string, 0, 2)
	cleanup := func(cause error) error {
		var cleanupErrors []error
		for _, path := range created {
			if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("remove partial stage log %s: %w", path, err))
			}
		}
		return errors.Join(append([]error{cause}, cleanupErrors...)...)
	}
	for _, path := range []string{layout.HumanLog, layout.StructuredLog} {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return cleanup(fmt.Errorf("create private stage log %s: %w", path, err))
		}
		created = append(created, path)
		if err := file.Close(); err != nil {
			return cleanup(fmt.Errorf("close private stage log %s: %w", path, err))
		}
		info, err := os.Lstat(path)
		if err != nil {
			return cleanup(fmt.Errorf("inspect private stage log %s: %w", path, err))
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			return cleanup(fmt.Errorf("private stage log mode is not 0600: %s", path))
		}
	}
	return nil
}

// ValidateStage proves that a stage is a private, canonical descendant of this
// organ's staging root and derives every artifact path from that root.
func ValidateStage(context artifact.RepoContext, stageRoot string) (artifact.StageLayout, error) {
	if _, err := Validate(context); err != nil {
		return artifact.StageLayout{}, fmt.Errorf("validate organ before stage: %w", err)
	}
	stagingRoot := filepath.Join(ScratchRoot(context.Organ), "staging")
	if !filepath.IsAbs(stageRoot) || filepath.Clean(stageRoot) != stageRoot {
		return artifact.StageLayout{}, fmt.Errorf("staging path must be absolute and canonical: %s", stageRoot)
	}
	if !strictDescendant(stagingRoot, stageRoot) {
		return artifact.StageLayout{}, fmt.Errorf("staging path is outside %s: %s", stagingRoot, stageRoot)
	}
	if err := validateCanonicalDirectory(stagingRoot, "staging root"); err != nil {
		return artifact.StageLayout{}, err
	}
	if err := validatePrivateDirectory(stageRoot, os.Getuid()); err != nil {
		return artifact.StageLayout{}, fmt.Errorf("invalid staging directory: %w", err)
	}
	layout := layoutFromRoot(context, stageRoot)
	for _, directory := range []string{layout.Maps, layout.Meta} {
		if err := validatePrivateDirectory(directory, os.Getuid()); err != nil {
			return artifact.StageLayout{}, fmt.Errorf("invalid stage leaf %s: %w", filepath.Base(directory), err)
		}
	}
	return layout, nil
}

// RemoveEmptyStage removes an empty-window stage only after the full stage
// boundary has been revalidated. The name describes the night outcome; the
// directory may contain enumeration metadata proving that the window was empty.
func RemoveEmptyStage(context artifact.RepoContext, stageRoot string) error {
	layout, err := ValidateStage(context, stageRoot)
	if err != nil {
		return err
	}
	if err := os.RemoveAll(layout.Root); err != nil {
		return fmt.Errorf("remove empty-window stage %s: %w", layout.Root, err)
	}
	if _, err := os.Lstat(layout.Root); err == nil {
		return fmt.Errorf("empty-window stage survives removal: %s", layout.Root)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("verify empty-window stage removal %s: %w", layout.Root, err)
	}
	return nil
}

func layoutFromRoot(context artifact.RepoContext, root string) artifact.StageLayout {
	stem := filepath.Base(root)
	logsRoot := filepath.Join(ScratchRoot(context.Organ), "logs")
	return artifact.StageLayout{
		Root:               root,
		Maps:               filepath.Join(root, "maps"),
		Meta:               filepath.Join(root, "meta"),
		Paths:              filepath.Join(root, "paths.txt"),
		Pin:                filepath.Join(root, "paths.sha256"),
		Coverage:           filepath.Join(root, "coverage.md"),
		Verdicts:           filepath.Join(root, "verdicts.md"),
		NormalizedVerdicts: filepath.Join(root, "verdicts-normalized.tsv"),
		StructuredLog:      filepath.Join(logsRoot, stem+".jsonl"),
		HumanLog:           filepath.Join(logsRoot, stem+".log"),
	}
}

func ensurePrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(path, 0o700); err != nil {
			return fmt.Errorf("create private directory %s: %w", path, err)
		}
		return validatePrivateDirectory(path, os.Getuid())
	}
	if err != nil {
		return fmt.Errorf("inspect private directory %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("private directory is not a real directory: %s", path)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok {
		return fmt.Errorf("private directory owner is unavailable: %s", path)
	} else if int(stat.Uid) != os.Getuid() {
		return fmt.Errorf("private directory has the wrong owner: %s", path)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("set private directory mode on %s: %w", path, err)
	}
	return validatePrivateDirectory(path, os.Getuid())
}

func validatePrivateDirectory(path string, ownerUID int) error {
	if err := validateCanonicalDirectory(path, "private directory"); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect private directory %s: %w", path, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("private directory owner is unavailable: %s", path)
	}
	if int(stat.Uid) != ownerUID {
		return fmt.Errorf("private directory has the wrong owner: %s", path)
	}
	if info.Mode().Perm() != 0o700 {
		return fmt.Errorf("private directory mode is not 0700: %s", path)
	}
	return nil
}

func strictDescendant(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) {
		return false
	}
	return relative != "" && !stringsHasDotDotPrefix(relative)
}

func stringsHasDotDotPrefix(path string) bool {
	return len(path) > 2 && path[:2] == ".." && os.IsPathSeparator(path[2])
}
