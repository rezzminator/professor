// Package organ resolves and validates the repository-owned filesystem where
// the dreamer keeps its durable memory and run artifacts.
package organ

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/dream/artifact"
)

// Shape identifies which of the two supported Git ownership arrangements an
// organ uses. No intermediate Git root is accepted: an organ is either tracked
// by its repository or is itself the root of a nested ledger.
type Shape string

const (
	ShapeTrackedRoot  Shape = "tracked-root"
	ShapeNestedLedger Shape = "nested-ledger"
)

// HookContext deliberately keeps the actual Git worktree root separate from
// the root that owns the organ. Hooks inspect live files at GitRoot, while an
// in-repository /.worktrees/* path shares its base repository's organ.
type HookContext struct {
	GitRoot   string
	OrganRoot string
	Organ     string
}

// RepositoryRoot resolves the Git top level that owns an arbitrary working
// directory. The caller still validates the returned root through Resolve
// before performing a dream operation.
func RepositoryRoot(directory string) (string, error) {
	if err := validateCanonicalDirectory(directory, "working directory"); err != nil {
		return "", err
	}
	root, err := gitRevParse(directory, "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("working directory is not inside a Git repository: %s: %w", directory, err)
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return "", fmt.Errorf("git top level is not absolute and canonical: %s", root)
	}
	return root, nil
}

// Resolve validates a requested repository root and derives its organ and
// Claude transcript registry paths. The registry key is Claude's path encoding:
// every path separator becomes a hyphen, including the leading separator.
func Resolve(repoRoot, registryBase string) (artifact.RepoContext, error) {
	if err := validateRepositoryRoot(repoRoot); err != nil {
		return artifact.RepoContext{}, err
	}
	if !filepath.IsAbs(registryBase) {
		return artifact.RepoContext{}, fmt.Errorf("registry base must be absolute: %s", registryBase)
	}
	if filepath.Clean(registryBase) != registryBase {
		return artifact.RepoContext{}, fmt.Errorf("registry base must be canonical: %s", registryBase)
	}
	encoded := strings.ReplaceAll(repoRoot, string(filepath.Separator), "-")
	return artifact.RepoContext{
		RepoRoot: repoRoot,
		Organ:    filepath.Join(repoRoot, ".professor", "stm"),
		Registry: filepath.Join(registryBase, encoded),
	}, nil
}

// ResolveHook resolves the two roots a spawn hook needs. Only the organ base is
// stripped at the first /.worktrees/* boundary; GitRoot remains the actual
// worktree so anchor drift is checked against the files the agent can see.
func ResolveHook(cwd string) (HookContext, error) {
	if err := validateCanonicalDirectory(cwd, "hook cwd"); err != nil {
		return HookContext{}, err
	}
	gitRoot, err := gitRevParse(cwd, "--show-toplevel")
	if err != nil {
		return HookContext{}, fmt.Errorf("hook cwd is not inside a Git repository: %s: %w", cwd, err)
	}
	if err := validateRepositoryRoot(gitRoot); err != nil {
		return HookContext{}, err
	}
	organRoot := gitRoot
	marker := string(filepath.Separator) + ".worktrees" + string(filepath.Separator)
	if index := strings.Index(gitRoot, marker); index >= 0 {
		organRoot = gitRoot[:index]
		if err := validateRepositoryRoot(organRoot); err != nil {
			return HookContext{}, fmt.Errorf("validate worktree organ root %s: %w", organRoot, err)
		}
	}
	return HookContext{
		GitRoot:   gitRoot,
		OrganRoot: organRoot,
		Organ:     filepath.Join(organRoot, ".professor", "stm"),
	}, nil
}

// ResolveNudge derives the organ without invoking Git. The nudge is the alarm
// for a broken Dreamer; a Git probe failure must not make that alarm disappear
// before it can persist nudge.failed. UserPromptSubmit supplies the project
// root, matching the legacy hook contract.
func ResolveNudge(projectDirectory string) (HookContext, error) {
	if err := validateCanonicalDirectory(projectDirectory, "nudge project directory"); err != nil {
		return HookContext{}, err
	}
	organRoot := projectDirectory
	marker := string(filepath.Separator) + ".worktrees" + string(filepath.Separator)
	if index := strings.Index(projectDirectory, marker); index >= 0 {
		organRoot = projectDirectory[:index]
		if err := validateCanonicalDirectory(organRoot, "nudge organ root"); err != nil {
			return HookContext{}, err
		}
	}
	return HookContext{
		GitRoot:   projectDirectory,
		OrganRoot: organRoot,
		Organ:     filepath.Join(organRoot, ".professor", "stm"),
	}, nil
}

// RootFromOrgan derives and validates the repository root for an organ at the
// canonical {root}/.professor/stm location. ROOT is an optional assertion, not
// an alternate source of truth; when present, its sole textual value must equal
// the canonical repository root.
func RootFromOrgan(organ string) (string, error) {
	if err := validateCanonicalDirectory(organ, "organ"); err != nil {
		return "", err
	}
	professor := filepath.Dir(organ)
	if filepath.Base(organ) != "stm" || filepath.Base(professor) != ".professor" {
		return "", fmt.Errorf("organ is not at a .professor/stm boundary: %s", organ)
	}
	repoRoot := filepath.Dir(professor)
	if err := validateRepositoryRoot(repoRoot); err != nil {
		return "", err
	}
	if err := validateRootAssertion(organ, repoRoot); err != nil {
		return "", err
	}
	return repoRoot, nil
}

// Validate checks the complete organ boundary and returns its exact Git shape.
func Validate(context artifact.RepoContext) (Shape, error) {
	if err := validateRepositoryRoot(context.RepoRoot); err != nil {
		return "", err
	}
	wantOrgan := filepath.Join(context.RepoRoot, ".professor", "stm")
	if context.Organ != wantOrgan {
		return "", fmt.Errorf("organ path does not match repository root: got %s, want %s", context.Organ, wantOrgan)
	}
	if err := validateCanonicalDirectory(context.Organ, "organ"); err != nil {
		return "", err
	}
	for _, name := range []string{"maps", "dreamer", "archive"} {
		if err := validateCanonicalDirectory(
			filepath.Join(context.Organ, name),
			"organ "+name+" directory",
		); err != nil {
			return "", err
		}
	}
	if err := validateRegularFile(filepath.Join(context.Organ, "stm.md"), "organ stm.md"); err != nil {
		return "", err
	}
	if err := validateRootAssertion(context.Organ, context.RepoRoot); err != nil {
		return "", err
	}
	if err := validateCanonicalDirectory(context.Registry, "Claude registry"); err != nil {
		return "", err
	}

	organTop, err := gitRevParse(context.Organ, "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("organ is not inside a Git repository: %s: %w", context.Organ, err)
	}
	switch organTop {
	case context.RepoRoot:
		return ShapeTrackedRoot, nil
	case context.Organ:
		if err := validateCanonicalDirectory(
			filepath.Join(context.Organ, ".git"),
			"nested organ Git ledger",
		); err != nil {
			return "", fmt.Errorf("organ does not own its nested Git ledger: %w", err)
		}
		if _, err := gitRevParse(context.Organ, "--verify", "HEAD"); err != nil {
			return "", fmt.Errorf("nested organ ledger has no HEAD: %s: %w", context.Organ, err)
		}
		return ShapeNestedLedger, nil
	default:
		return "", fmt.Errorf(
			"unsupported organ Git shape: organ %s resolves to intermediate Git top %s",
			context.Organ,
			organTop,
		)
	}
}

func validateRepositoryRoot(repoRoot string) error {
	if !filepath.IsAbs(repoRoot) {
		return fmt.Errorf("repository root must be absolute: %s", repoRoot)
	}
	if filepath.Clean(repoRoot) != repoRoot {
		return fmt.Errorf("repository root must be canonical: %s", repoRoot)
	}
	if err := validateCanonicalDirectory(repoRoot, "repository root"); err != nil {
		return err
	}
	if _, err := gitRevParse(repoRoot, "--verify", "HEAD"); err != nil {
		return fmt.Errorf("repository has no HEAD: %s: %w", repoRoot, err)
	}
	top, err := gitRevParse(repoRoot, "--show-toplevel")
	if err != nil {
		return fmt.Errorf("repository has no top level: %s: %w", repoRoot, err)
	}
	if top != repoRoot {
		return fmt.Errorf("repository root is not the Git top level: %s (top is %s)", repoRoot, top)
	}
	return nil
}

func validateCanonicalDirectory(path, label string) error {
	if !filepath.IsAbs(path) {
		return artifact.ErrorAt(path, fmt.Errorf("%s must be absolute: %s", label, path))
	}
	if filepath.Clean(path) != path {
		return artifact.ErrorAt(path, fmt.Errorf("%s must be canonical: %s", label, path))
	}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return artifact.ErrorAt(path, fmt.Errorf("missing directory: %s", path))
		}
		return artifact.ErrorAt(path, fmt.Errorf("inspect %s %s: %w", label, path, err))
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return artifact.ErrorAt(path, fmt.Errorf("%s is a symlink and not canonical: %s", label, path))
	}
	if !info.IsDir() {
		return artifact.ErrorAt(path, fmt.Errorf("missing directory: %s", path))
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return artifact.ErrorAt(path, fmt.Errorf("%s does not resolve: %s: %w", label, path, err))
	}
	if resolved != path {
		return artifact.ErrorAt(path, fmt.Errorf("%s must be canonical: %s resolves to %s", label, path, resolved))
	}
	return nil
}

func validateRegularFile(path, label string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return artifact.ErrorAt(path, fmt.Errorf("missing file: %s", path))
		}
		return artifact.ErrorAt(path, fmt.Errorf("inspect %s %s: %w", label, path, err))
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return artifact.ErrorAt(path, fmt.Errorf("%s must be a regular non-symlink file: %s", label, path))
	}
	return nil
}

func validateRootAssertion(organ, repoRoot string) error {
	rootPath := filepath.Join(organ, "ROOT")
	info, err := os.Lstat(rootPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect organ ROOT %s: %w", rootPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("organ ROOT must be a regular non-symlink file: %s", rootPath)
	}
	raw, err := os.ReadFile(rootPath)
	if err != nil {
		return fmt.Errorf("read organ ROOT %s: %w", rootPath, err)
	}
	value := string(raw)
	if value != repoRoot && value != repoRoot+"\n" {
		return fmt.Errorf(
			"organ ROOT does not exactly match canonical repository root: got %q, want %q",
			value,
			repoRoot,
		)
	}
	return nil
}

// gitRevParse is the organ package's sole Git boundary. It intentionally admits
// only rev-parse, and disables Git's optional locks for every read.
func gitRevParse(directory string, arguments ...string) (string, error) {
	args := append([]string{"-C", directory, "rev-parse"}, arguments...)
	command := exec.Command(deps.Executable("git"), args...)
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			return "", fmt.Errorf("git rev-parse %s: %w", strings.Join(arguments, " "), err)
		}
		return "", fmt.Errorf("git rev-parse %s: %w: %s", strings.Join(arguments, " "), err, message)
	}
	value := strings.TrimSpace(string(output))
	if value == "" || strings.Contains(value, "\n") {
		return "", fmt.Errorf("git rev-parse %s returned invalid output %q", strings.Join(arguments, " "), value)
	}
	return value, nil
}
