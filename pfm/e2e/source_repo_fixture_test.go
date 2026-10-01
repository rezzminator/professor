//go:build e2e

package e2e

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	pfmpaths "github.com/rezzminator/professor/pfm/internal/paths"
)

var sharedStage = struct {
	once sync.Once
	path string
	err  error
}{}

var e2eStageRoot string

func TestPrepareSourceRepoStagesEvenAReadyRepository(t *testing.T) {
	root := filepath.Join(t.TempDir(), "ready-source")
	for _, relative := range []string{
		"CLAUDE.md", "AGENTS.md", ".claude/settings.json",
	} {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, relative := range []string{
		".claude/commands", ".claude/agents", ".claude/skills",
	} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(relative)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	runGitFixture(t, root, "init", "-q")
	runGitFixture(t, root, "config", "user.email", "fixture.invalid")
	runGitFixture(t, root, "config", "user.name", "fixture-identity")
	runGitFixture(t, root, "add", "-A")
	runGitFixture(t, root, "commit", "-qm", "ready source")
	runGitFixture(t, root, "tag", "v0.0.1")
	staged := prepareSourceRepo(t, root)
	if filepath.Clean(staged) == filepath.Clean(root) {
		t.Fatalf("prepareSourceRepo returned live source %q, want a staged TempDir copy", staged)
	}
}

func TestCopySourceTreePreservesInternalSymlinks(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	linkedDir := filepath.Join(source, ".claude", "skills", "fixture")
	if err := os.MkdirAll(linkedDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linkedDir, "SKILL.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(source, ".codex", "skills", "fixture")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	const linkTarget = "../../.claude/skills/fixture"
	if err := os.Symlink(linkTarget, link); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, source, "init", "-q")
	runGitFixture(t, source, "add", "-A")

	if err := copySourceTree(source, target); err != nil {
		t.Fatalf("copy source tree: %v", err)
	}
	copiedLink := filepath.Join(target, ".codex", "skills", "fixture")
	gotTarget, err := os.Readlink(copiedLink)
	if err != nil {
		t.Fatalf("read copied symlink: %v", err)
	}
	if gotTarget != linkTarget {
		t.Fatalf("copied symlink target = %q, want %q", gotTarget, linkTarget)
	}
	if contents, err := os.ReadFile(
		filepath.Join(copiedLink, "SKILL.md"),
	); err != nil ||
		string(contents) != "fixture\n" {
		t.Fatalf("read through copied symlink: contents=%q err=%v", contents, err)
	}
}

func TestCopySourceTreeEnumeratesLinkedWorktreeWithFenceGitDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("linked fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitFixture(t, root, "init", "-q")
	runGitFixture(t, root, "config", "user.email", "fixture.invalid")
	runGitFixture(t, root, "config", "user.name", "fixture-identity")
	runGitFixture(t, root, "add", "tracked.txt")
	runGitFixture(t, root, "commit", "-qm", "linked fixture")

	source := filepath.Join(t.TempDir(), "linked-worktree")
	runGitFixture(t, root, "worktree", "add", "--detach", "-q", source, "HEAD")
	gitDirResult := runGit(source, "rev-parse", "--git-dir")
	if gitDirResult.err != nil {
		t.Fatalf("resolve linked worktree git dir: %v\n%s", gitDirResult.err, gitDirResult.stderr)
	}
	gitDir := strings.TrimSpace(gitDirResult.stdout)
	if gitDir == "" {
		t.Fatal("linked worktree returned an empty git dir")
	}
	// Simulate the fenced mount: the linked worktree's .git file points at
	// the host path, while the fence supplies its mounted git dir explicitly.
	if err := os.WriteFile(
		filepath.Join(source, ".git"),
		[]byte("gitdir: /fixture/host-only/worktree\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv(pfmpaths.EnvDevRepoWorkTree, source)
	t.Setenv(pfmpaths.EnvDevRepoGitDir, gitDir)
	target := filepath.Join(t.TempDir(), "staged")
	if err := copySourceTree(source, target); err != nil {
		t.Fatalf("copy linked worktree through fence: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(target, "tracked.txt")); err != nil || string(got) != "linked fixture\n" {
		t.Fatalf("staged linked worktree file=%q err=%v, want fixture", got, err)
	}
}

func TestCopySourceTreeSkipsTrackedDeletedPaths(t *testing.T) {
	source := filepath.Join(t.TempDir(), "source")
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"kept", "deleted"} {
		if err := os.WriteFile(filepath.Join(source, name), []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGitFixture(t, source, "init", "-q")
	runGitFixture(t, source, "add", "kept", "deleted")
	if err := os.Remove(filepath.Join(source, "deleted")); err != nil {
		t.Fatal(err)
	}

	if err := copySourceTree(source, target); err != nil {
		t.Fatalf("copy source tree with tracked deletion: %v", err)
	}
	if contents, err := os.ReadFile(filepath.Join(target, "kept")); err != nil || string(contents) != "kept\n" {
		t.Fatalf("read copied kept file: contents=%q err=%v", contents, err)
	}
	if _, err := os.Lstat(filepath.Join(target, "deleted")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("deleted tracked file was copied or inspect failed: %v", err)
	}
}

func TestCopySourceTreeRejectsExternalSymlinks(t *testing.T) {
	for name, linkTarget := range map[string]string{
		"absolute": filepath.Join(string(filepath.Separator), "outside"),
		"escape":   "../outside",
	} {
		t.Run(name, func(t *testing.T) {
			source := filepath.Join(t.TempDir(), "source")
			if err := os.MkdirAll(source, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(linkTarget, filepath.Join(source, "link")); err != nil {
				t.Fatal(err)
			}
			runGitFixture(t, source, "init", "-q")
			runGitFixture(t, source, "add", "-A")
			err := copySourceTree(source, filepath.Join(t.TempDir(), "target"))
			if err == nil || !strings.Contains(err.Error(), "points outside source fixture") {
				t.Fatalf("copy source tree error = %v, want outside-source refusal", err)
			}
		})
	}
}

func sourceRepo(t *testing.T) string {
	t.Helper()
	root, workTree, gitDir := sourceRepoRoot(t)
	return prepareSourceRepoWithGit(t, root, workTree, gitDir)
}

func sourceRepoRoot(t *testing.T) (root, workTree, gitDir string) {
	t.Helper()
	if explicit := strings.TrimSpace(os.Getenv(e2eSourceRepo)); explicit != "" {
		root, err := filepath.Abs(explicit)
		if err != nil {
			t.Fatalf("resolve %s: %v", e2eSourceRepo, err)
		}
		return root,
			strings.TrimSpace(os.Getenv(pfmpaths.EnvDevRepoWorkTree)),
			strings.TrimSpace(os.Getenv(pfmpaths.EnvDevRepoGitDir))
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate e2e source")
	}
	root = filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "pfm", "go.mod")); err != nil {
		t.Fatalf("locate repository from e2e source: %v", err)
	}
	workTree = strings.TrimSpace(os.Getenv(pfmpaths.EnvDevRepoWorkTree))
	gitDir = strings.TrimSpace(os.Getenv(pfmpaths.EnvDevRepoGitDir))
	if workTree == "" || gitDir == "" || filepath.Clean(root) != filepath.Clean(workTree) {
		workTree, gitDir = "", ""
	}
	return root, workTree, gitDir
}

func sharedSourceRepo(t *testing.T) string {
	t.Helper()
	if e2eStageRoot == "" {
		t.Fatal("e2e stage root was not made")
	}
	root, workTree, gitDir := sourceRepoRoot(t)
	currentTag := currentE2ETag(t)
	sharedStage.once.Do(func() {
		sharedStage.path = filepath.Join(e2eStageRoot, "source")
		sharedStage.err = stageSourceRepo(sharedStage.path, root, workTree, gitDir, currentTag)
	})
	if sharedStage.err != nil {
		t.Fatalf("stage shared e2e source repository: %v", sharedStage.err)
	}
	return sharedStage.path
}

func prepareSourceRepo(t *testing.T, root string) string {
	t.Helper()
	workTree := strings.TrimSpace(os.Getenv(pfmpaths.EnvDevRepoWorkTree))
	gitDir := strings.TrimSpace(os.Getenv(pfmpaths.EnvDevRepoGitDir))
	if workTree == "" || gitDir == "" || filepath.Clean(root) != filepath.Clean(workTree) {
		workTree = ""
		gitDir = ""
	}
	return prepareSourceRepoWithGit(t, root, workTree, gitDir)
}

func prepareSourceRepoWithGit(t *testing.T, root, workTree, gitDir string) string {
	t.Helper()
	fixture := filepath.Join(t.TempDir(), "source")
	if err := stageSourceRepo(fixture, root, workTree, gitDir, currentE2ETag(t)); err != nil {
		t.Fatalf("stage e2e source repository: %v", err)
	}
	return fixture
}

func stageSourceRepo(fixture, root, workTree, gitDir, currentTag string) error {
	if err := copySourceTreeWithGit(root, fixture, workTree, gitDir); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(fixture, filepath.FromSlash(e2eFixtureSkill))); errors.Is(err, fs.ErrNotExist) {
		path := filepath.Join(fixture, filepath.FromSlash(e2eFixtureSkill))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("stage e2e fixture skill directory: %w", err)
		}
		if err := os.WriteFile(path, []byte("# E2E fixture skill\n"), 0o600); err != nil {
			return fmt.Errorf("stage e2e fixture skill: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect e2e fixture skill: %w", err)
	}

	previousTag := strings.TrimSpace(os.Getenv(e2ePreviousTag))
	if previousTag == "" {
		previousTag = "v0.0.1"
	}
	if !isReleaseTag(previousTag) {
		return fmt.Errorf("invalid %s=%q: want semantic release tag", e2ePreviousTag, previousTag)
	}
	git := func(args ...string) error {
		result := runGit(fixture, args...)
		if result.err != nil {
			return fmt.Errorf(
				"source fixture git %s: %w\n%s",
				strings.Join(args, " "),
				result.err,
				strings.TrimSpace(result.stdout+result.stderr),
			)
		}
		return nil
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "fixture.invalid"},
		{"config", "user.name", "fixture-identity"},
		{"config", "core.hooksPath", ".githooks"},
		{"add", "-A"},
		{"add", "-f", filepath.ToSlash(e2eFixtureSkill)},
		{"commit", "-qm", "fixture previous release"},
		{"tag", previousTag},
	} {
		if err := git(args...); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(fixture, ".e2e-current-source"), []byte("current\n"), 0o600); err != nil {
		return fmt.Errorf("stage e2e current source marker: %w", err)
	}
	for _, args := range [][]string{
		{"add", ".e2e-current-source"},
		{"commit", "-qm", "fixture current source"},
		{"tag", currentTag},
		{"remote", "add", "origin", fixture},
	} {
		if err := git(args...); err != nil {
			return err
		}
	}
	return nil
}

func currentE2ETag(t *testing.T) string {
	t.Helper()
	if explicit := strings.TrimSpace(os.Getenv(e2eCurrentTag)); explicit != "" {
		if !isReleaseTag(explicit) {
			t.Fatalf("invalid %s=%q: want semantic release tag", e2eCurrentTag, explicit)
		}
		return explicit
	}
	previous := strings.TrimSpace(os.Getenv(e2ePreviousTag))
	if previous == "" {
		previous = "v0.0.1"
	}
	parts := strings.Split(strings.TrimPrefix(previous, "v"), ".")
	if len(parts) != 3 {
		t.Fatalf("derive current tag from invalid previous tag %q", previous)
	}
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		t.Fatalf("derive current tag from invalid previous tag %q: %v", previous, err)
	}
	return fmt.Sprintf("v%s.%s.%d", parts[0], parts[1], patch+1)
}

func copySourceTree(source, target string) error {
	workTree := strings.TrimSpace(os.Getenv(pfmpaths.EnvDevRepoWorkTree))
	gitDir := strings.TrimSpace(os.Getenv(pfmpaths.EnvDevRepoGitDir))
	if workTree == "" || gitDir == "" || filepath.Clean(source) != filepath.Clean(workTree) {
		metadata := fmt.Sprintf("worktree=%q git-dir=%q", workTree, gitDir)
		workTree = ""
		gitDir = ""
		if err := copySourceTreeWithGit(source, target, workTree, gitDir); err != nil {
			return fmt.Errorf("%s; fenced metadata not applicable (%s)", err, metadata)
		}
		return nil
	}
	return copySourceTreeWithGit(source, target, workTree, gitDir)
}

func copySourceTreeWithGit(source, target, workTree, gitDir string) error {
	if err := os.MkdirAll(target, 0o700); err != nil {
		return err
	}
	gitArgs := []string{
		"-c",
		"safe.directory=" + source,
		"-C",
		source,
		"ls-files",
		"--cached",
		"--others",
		"--exclude-standard",
		"-z",
	}
	gitContext := "repository discovery"
	if workTree != "" && gitDir != "" {
		gitArgs = append([]string{"--git-dir=" + gitDir, "--work-tree=" + workTree}, gitArgs...)
		gitContext = "explicit worktree metadata"
	}
	command := exec.Command("git", gitArgs...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"enumerate source fixture files from %q with %s: %w: %s",
			source, gitContext, err, strings.TrimSpace(string(output)),
		)
	}
	for _, rawRelative := range bytes.Split(output, []byte{0}) {
		if len(rawRelative) == 0 {
			continue
		}
		relative := filepath.Clean(filepath.FromSlash(string(rawRelative)))
		if relative == "." || filepath.IsAbs(relative) || relative == ".." ||
			strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("source fixture enumerated unsafe path %q", string(rawRelative))
		}
		path := filepath.Join(source, relative)
		destination := filepath.Join(target, relative)
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			// `git ls-files --cached` includes tracked paths deleted in the
			// working tree. The fixture represents the working tree, so omit them.
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect source fixture path %s: %w", relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("read source fixture symlink %s: %w", relative, err)
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(path), linkTarget))
			withinSource, err := filepath.Rel(source, resolved)
			if err != nil {
				return fmt.Errorf("resolve source fixture symlink %s: %w", relative, err)
			}
			if filepath.IsAbs(linkTarget) || withinSource == ".." ||
				strings.HasPrefix(withinSource, ".."+string(filepath.Separator)) {
				return fmt.Errorf("source fixture symlink %s points outside source fixture: %s", relative, linkTarget)
			}
			if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
				return fmt.Errorf("create source fixture symlink directory %s: %w", relative, err)
			}
			if err := os.Symlink(linkTarget, destination); err != nil {
				return fmt.Errorf("copy source fixture symlink %s: %w", relative, err)
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("source fixture path %s has unsupported mode %s", relative, info.Mode())
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return fmt.Errorf("create source fixture directory %s: %w", relative, err)
		}
		if err := copyFile(path, destination, info.Mode().Perm()); err != nil {
			return fmt.Errorf("copy source fixture file %s: %w", relative, err)
		}
	}
	return nil
}

func runGitFixture(t *testing.T, dir string, args ...string) {
	t.Helper()
	result := runGit(dir, args...)
	if result.err != nil {
		t.Fatalf(
			"source fixture git %s: %v\n%s",
			strings.Join(args, " "),
			result.err,
			strings.TrimSpace(result.stdout+result.stderr),
		)
	}
}
