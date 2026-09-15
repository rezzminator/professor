package organ

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/dream/artifact"
)

func TestResolveDerivesOrganAndEncodedRegistry(t *testing.T) {
	repo := newRepository(t)
	registryBase := filepath.Join(t.TempDir(), "projects")
	mustMkdir(t, registryBase, 0o700)

	context, err := Resolve(repo, registryBase)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if context.RepoRoot != repo {
		t.Fatalf("RepoRoot = %q, want %q", context.RepoRoot, repo)
	}
	if want := filepath.Join(repo, ".professor", "stm"); context.Organ != want {
		t.Fatalf("Organ = %q, want %q", context.Organ, want)
	}
	encoded := strings.ReplaceAll(repo, string(filepath.Separator), "-")
	if want := filepath.Join(registryBase, encoded); context.Registry != want {
		t.Fatalf("Registry = %q, want %q", context.Registry, want)
	}
}

func TestRepositoryRootResolvesFromNestedWorkingDirectory(t *testing.T) {
	repo := newRepository(t)
	workingDirectory := filepath.Join(repo, "nested", "work")
	mustMkdir(t, workingDirectory, 0o700)

	root, err := RepositoryRoot(workingDirectory)
	if err != nil {
		t.Fatalf("RepositoryRoot: %v", err)
	}
	if root != repo {
		t.Fatalf("RepositoryRoot = %q, want %q", root, repo)
	}
}

func TestResolveRejectsNonCanonicalAndNonRootRepositories(t *testing.T) {
	repo := newRepository(t)
	registryBase := t.TempDir()

	if _, err := Resolve(
		repo+string(filepath.Separator)+".",
		registryBase,
	); err == nil ||
		!strings.Contains(err.Error(), "canonical") {
		t.Fatalf("Resolve(non-canonical) error = %v, want canonical failure", err)
	}

	link := filepath.Join(t.TempDir(), "repository-link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(link, registryBase); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("Resolve(symlink) error = %v, want canonical failure", err)
	}

	subdirectory := filepath.Join(repo, "subdirectory")
	mustMkdir(t, subdirectory, 0o700)
	if _, err := Resolve(subdirectory, registryBase); err == nil || !strings.Contains(err.Error(), "Git top level") {
		t.Fatalf("Resolve(subdirectory) error = %v, want Git-top failure", err)
	}

	if _, err := Resolve(
		"relative/repository",
		registryBase,
	); err == nil ||
		!strings.Contains(err.Error(), "absolute") {
		t.Fatalf("Resolve(relative) error = %v, want absolute failure", err)
	}
}

func TestValidateAcceptsExactlyTrackedRootAndNestedLedger(t *testing.T) {
	tests := []struct {
		name  string
		shape Shape
		nest  bool
	}{
		{name: "tracked root", shape: ShapeTrackedRoot},
		{name: "nested ledger", shape: ShapeNestedLedger, nest: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := newRepository(t)
			registryBase := t.TempDir()
			context, err := Resolve(repo, registryBase)
			if err != nil {
				t.Fatal(err)
			}
			makeSkeleton(t, context)
			mustMkdir(t, context.Registry, 0o700)
			writeFile(t, filepath.Join(context.Organ, "ROOT"), repo+"\n", 0o600)
			if test.nest {
				initAndCommit(t, context.Organ)
			}

			shape, err := Validate(context)
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if shape != test.shape {
				t.Fatalf("shape = %q, want %q", shape, test.shape)
			}
		})
	}
}

func TestValidateRejectsIntermediateLedgerAndBadSkeleton(t *testing.T) {
	repo := newRepository(t)
	registryBase := t.TempDir()
	context, err := Resolve(repo, registryBase)
	if err != nil {
		t.Fatal(err)
	}
	makeSkeleton(t, context)
	mustMkdir(t, context.Registry, 0o700)
	intermediate := filepath.Dir(context.Organ)
	initAndCommit(t, intermediate)
	if _, err := Validate(context); err == nil || !strings.Contains(err.Error(), "unsupported organ Git shape") {
		t.Fatalf("Validate(intermediate ledger) error = %v, want shape failure", err)
	}

	// Remove the intermediate ledger so the parent repository is authoritative,
	// then prove every skeleton member is checked by its own name.
	if err := os.RemoveAll(filepath.Join(intermediate, ".git")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(context.Organ, "stm.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(context); err == nil || !strings.Contains(err.Error(), "stm.md") {
		t.Fatalf("Validate(missing stm.md) error = %v, want named skeleton failure", err)
	}
}

func TestValidateRejectsNestedWorktreeThatDoesNotOwnItsLedger(t *testing.T) {
	repo := newRepository(t)
	context, err := Resolve(repo, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mustMkdir(t, filepath.Dir(context.Organ), 0o700)
	ledger := newRepository(t)
	runGit(t, ledger, "worktree", "add", "-q", "-b", "organ-non-owner", context.Organ)
	makeSkeleton(t, context)
	mustMkdir(t, context.Registry, 0o700)

	if _, err := Validate(context); err == nil || !strings.Contains(err.Error(), "does not own") {
		t.Fatalf("Validate(nested worktree) error = %v, want own-ledger failure", err)
	}
}

func TestValidateRejectsMismatchedROOT(t *testing.T) {
	repo := newRepository(t)
	context, err := Resolve(repo, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	makeSkeleton(t, context)
	mustMkdir(t, context.Registry, 0o700)
	writeFile(t, filepath.Join(context.Organ, "ROOT"), repo+"-wrong\n", 0o600)
	if _, err := Validate(context); err == nil || !strings.Contains(err.Error(), "ROOT") {
		t.Fatalf("Validate(bad ROOT) error = %v, want ROOT failure", err)
	}
}

func TestRootFromOrganValidatesPlacementAndOptionalROOT(t *testing.T) {
	repo := newRepository(t)
	context, err := Resolve(repo, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	makeSkeleton(t, context)

	root, err := RootFromOrgan(context.Organ)
	if err != nil {
		t.Fatalf("RootFromOrgan without ROOT: %v", err)
	}
	if root != repo {
		t.Fatalf("root = %q, want %q", root, repo)
	}
	writeFile(t, filepath.Join(context.Organ, "ROOT"), repo, 0o600)
	root, err = RootFromOrgan(context.Organ)
	if err != nil || root != repo {
		t.Fatalf("RootFromOrgan with ROOT = %q, %v; want %q", root, err, repo)
	}

	wronglyPlaced := filepath.Join(repo, "stm")
	mustMkdir(t, wronglyPlaced, 0o700)
	if _, err := RootFromOrgan(wronglyPlaced); err == nil || !strings.Contains(err.Error(), ".professor/stm") {
		t.Fatalf("RootFromOrgan(wrong placement) error = %v", err)
	}
}

func TestResolveHookPreservesWorktreeGitRootAndUsesBaseOrgan(t *testing.T) {
	repo := newRepository(t)
	worktree := filepath.Join(repo, ".worktrees", "topic")
	mustMkdir(t, filepath.Dir(worktree), 0o700)
	runGit(t, repo, "worktree", "add", "-q", "-b", "organ-test-topic", worktree)
	worktreeCWD := filepath.Join(worktree, "nested", "cwd")
	mustMkdir(t, worktreeCWD, 0o700)

	context, err := ResolveHook(worktreeCWD)
	if err != nil {
		t.Fatalf("ResolveHook: %v", err)
	}
	if context.GitRoot != worktree {
		t.Fatalf("GitRoot = %q, want actual worktree %q", context.GitRoot, worktree)
	}
	if context.OrganRoot != repo {
		t.Fatalf("OrganRoot = %q, want base repository %q", context.OrganRoot, repo)
	}
	if want := filepath.Join(repo, ".professor", "stm"); context.Organ != want {
		t.Fatalf("Organ = %q, want %q", context.Organ, want)
	}
}

func newRepository(t *testing.T) string {
	t.Helper()
	repo := filepath.Join(t.TempDir(), "repository")
	mustMkdir(t, repo, 0o700)
	initAndCommit(t, repo)
	return repo
}

func makeSkeleton(t *testing.T, context artifact.RepoContext) {
	t.Helper()
	for _, name := range []string{"maps", "dreamer", "archive"} {
		mustMkdir(t, filepath.Join(context.Organ, name), 0o700)
	}
	writeFile(t, filepath.Join(context.Organ, "stm.md"), "# fixture STM\n", 0o600)
}

func initAndCommit(t *testing.T, repo string) {
	t.Helper()
	runGit(t, repo, "init", "-q", "--initial-branch=main")
	runGit(t, repo, "config", "user.email", "dreamer-test@example.invalid")
	runGit(t, repo, "config", "user.name", "Dreamer Test")
	writeFile(t, filepath.Join(repo, ".dreamer-test"), "fixture\n", 0o600)
	runGit(t, repo, "add", ".dreamer-test")
	runGit(t, repo, "commit", "-q", "-m", "fixture")
}

func runGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	args := append([]string{"-C", directory}, arguments...)
	command := exec.Command("git", args...)
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}

func mustMkdir(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(path, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}
