//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	e2eHomeEnv         = "PFM_E2E_HOME"
	e2ePreviousTag     = "PFM_E2E_PREVIOUS_TAG"
	e2eCurrentTag      = "PFM_E2E_CURRENT_TAG"
	e2eSourceRepo      = "PFM_E2E_SOURCE_REPO"
	e2eHarvestSkipLine = "harvestpy: skipped (blocked, not attempted)"
	e2eFixtureSkill    = ".claude/skills/e2e-fixture/SKILL.md"
	e2eBinaryEnv       = "PFM_E2E_BINARY"
	e2eSourceMarker    = ".local/share/pfm/install/source-repo"
	e2eManagedRoot     = ".local/share/pfm/install"
	e2eCanonicalPFM    = ".local/bin/pfm"
	e2eCanonicalClaude = ".local/bin/claude"
	e2eSettings        = ".cc/1/settings.json"
	e2eCodexHooks      = ".codex/hooks.json"
	e2eZshrc           = ".zshrc"
	e2eCommandRoot     = ".claude/commands"
)

var commandLinks = []string{
	"reload.md",
}

var managedAssets = []string{
	"reload.command.md",
	"shim/pfm.zsh",
	"bin/claude",
}

var managedSettings = []string{
	".cc/1/settings.json",
	".cc/2/settings.json",
	".cc/3/settings.json",
}

var expectedHooks = []string{
	"usage-hook",
	"internal explore-deny",
	"internal epic-inject",
	"internal launcher-repair",
}

type e2eHarness struct {
	t          *testing.T
	repo       string
	headBinary string
	goCache    string
	goModCache string
}

type commandResult struct {
	stdout string
	stderr string
	err    error
}

type surfaceSnapshot map[string]string

func TestInstallInitUpdateUninstallE2E(t *testing.T) {
	runInstallE2E(t)
}

func TestE2EFenceIsRequiredEvenWithoutHome(t *testing.T) {
	const helper = "PFM_E2E_REQUIRE_FENCE_HELPER"
	if os.Getenv(helper) == "1" {
		requireE2EFence(t)
		return
	}
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	run := func(home string, fenced bool) commandResult {
		t.Helper()
		command := exec.Command(os.Args[0], "-test.run", "^TestE2EFenceIsRequiredEvenWithoutHome$")
		environment := map[string]string{
			"HOME": home,
			helper: "1",
		}
		if fenced {
			environment["PFM_DEV_FENCE"] = "1"
		}
		command.Env = appendCleanEnv(os.Environ(), environment)
		var stdout, stderr bytes.Buffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		err := command.Run()
		return commandResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
	}
	for _, home := range []string{realHome, ""} {
		if result := run(home, false); result.err == nil || !strings.Contains(result.stdout+result.stderr, "e2e harness refuses") {
			t.Fatalf("unfenced HOME=%q helper result=%+v, want refusal", home, result)
		}
	}
	for _, home := range []string{realHome, ""} {
		if result := run(home, true); result.err != nil {
			t.Fatalf("fenced HOME=%q helper result=%+v, want allowed", home, result)
		}
	}
}

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
	linkedDirectory := filepath.Join(source, ".claude", "skills", "fixture")
	if err := os.MkdirAll(linkedDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linkedDirectory, "SKILL.md"), []byte("fixture\n"), 0o600); err != nil {
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
	if contents, err := os.ReadFile(filepath.Join(copiedLink, "SKILL.md")); err != nil || string(contents) != "fixture\n" {
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
	if err := os.WriteFile(filepath.Join(source, ".git"), []byte("gitdir: /fixture/host-only/worktree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_DEV_REPO_WORK_TREE", source)
	t.Setenv("PFM_DEV_REPO_GIT_DIR", gitDir)
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

func runInstallE2E(t *testing.T) {
	t.Helper()
	requireE2EFence(t)
	repo := sourceRepo(t)
	harness := &e2eHarness{
		t:          t,
		repo:       repo,
		goCache:    requiredGoEnv(t, "GOCACHE"),
		goModCache: requiredGoEnv(t, "GOMODCACHE"),
	}
	harness.headBinary = harness.build(repo, filepath.Join(t.TempDir(), "pfm-head"))

	var fresh surfaceSnapshot
	var freshHome string
	t.Run("install", func(t *testing.T) {
		home := harness.newHome(harness.headBinary)
		freshHome = home
		result := harness.pfm(home, "install", "--yes", "--skip-harvest")
		harness.requireSuccess("install", result)
		harness.requireHarvestGate("install", result)
		harness.assertInstalled(home)
		result = harness.pfm(home, "doctor")
		harness.requireSkippedHarvestDoctor(result)
		fresh, _ = harness.snapshot(home)
	})

	t.Run("init", func(t *testing.T) {
		project := filepath.Join(t.TempDir(), "project")
		result := harness.pfm(freshHome, "init", project)
		harness.requireSuccess("init", result)
		harness.assertInit(project, repo)
		harness.requireSuccess("init Codex build", harness.pfm(freshHome, "codex", "build", "--home", freshHome, project))
		harness.requireSuccess("init Codex check", harness.pfm(freshHome, "codex", "check", "--home", freshHome, project))
		harness.assertInitPath(filepath.Join(project, "AGENTS.md"), "AGENTS.md")
	})

	t.Run("launcher", func(t *testing.T) {
		harness.assertLauncherRuntime(freshHome)
	})

	t.Run("vscode terminal profile", func(t *testing.T) {
		home := harness.newHome(harness.headBinary)
		platform := "linux"
		settings := filepath.Join(home, ".config", "Code", "User", "settings.json")
		if runtime.GOOS == "darwin" {
			platform = "osx"
			settings = filepath.Join(home, "Library", "Application Support", "Code", "User", "settings.json")
		}
		if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
			t.Fatal(err)
		}
		defaultKey := "terminal.integrated.defaultProfile." + platform
		original := fmt.Sprintf("{\n  // e2e operator setting\n  \"editor.fontSize\": 16,\n  %q: \"zsh\",\n}\n", defaultKey)
		if err := os.WriteFile(settings, []byte(original), 0o600); err != nil {
			t.Fatal(err)
		}
		result := harness.pfm(home, "install", "--yes", "--vscode", "--skip-harvest")
		harness.requireSuccess("VS Code terminal install", result)
		merged, err := os.ReadFile(settings)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"// e2e operator setting", `"PFM"`, `"PFM_AUTO_OPEN": "pfm"`, fmt.Sprintf("%q: \"PFM\"", defaultKey)} {
			if !strings.Contains(string(merged), want) {
				t.Fatalf("VS Code settings missing %q after install:\n%s", want, merged)
			}
		}
		result = harness.pfm(home, "uninstall")
		harness.requireSuccess("VS Code terminal uninstall", result)
		restored, err := os.ReadFile(settings)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(restored), fmt.Sprintf("%q: \"zsh\"", defaultKey)) ||
			strings.Contains(string(restored), `"PFM"`) || strings.Contains(string(restored), "PFM_AUTO_OPEN") {
			t.Fatalf("VS Code settings were not selectively restored:\n%s", restored)
		}
	})

	t.Run("update", func(t *testing.T) {
		previous := harness.previousBinary()
		home := harness.newHome(previous)
		result := harness.pfm(home, "install", "--yes", "--skip-harvest")
		harness.requireSuccess("previous install", result)
		harness.requireHarvestGate("previous install", result)
		currentTag := currentE2ETag(t)
		result = harness.pfm(home, "update", "--skip-harvest", "--to", currentTag, "--repo", harness.repo)
		harness.requireSuccess("self-update from previous release", result)
		harness.requireHarvestGate("self-update from previous release", result)
		if !strings.Contains(result.stdout, "updated "+currentTag) {
			t.Fatalf("self-update output=%q, want target %s", result.stdout, currentTag)
		}
		version := harness.pfm(home, "version")
		harness.requireSuccess("updated version", version)
		wantVersion := "pfm " + currentTag
		if strings.TrimSpace(version.stdout) != wantVersion {
			t.Fatalf("updated pfm version=%q, want %s", strings.TrimSpace(version.stdout), wantVersion)
		}
		harness.assertInstalled(home)
		updated, _ := harness.snapshot(home)
		if differences := snapshotDifferences(fresh, updated); len(differences) != 0 {
			t.Fatalf("update convergence failed; differing paths: %s", strings.Join(differences, ", "))
		}
	})

	t.Run("uninstall", func(t *testing.T) {
		harness.plantManualState(freshHome)
		result := harness.pfm(freshHome, "uninstall")
		harness.requireSuccess("uninstall", result)
		harness.assertUninstalled(freshHome)
	})
}

func requireE2EFence(t *testing.T) {
	t.Helper()
	if os.Getenv("PFM_DEV_FENCE") != "1" {
		t.Fatal("e2e harness refuses to run without PFM_DEV_FENCE=1")
	}
}

func requiredGoEnv(t *testing.T, name string) string {
	t.Helper()
	command := exec.Command("go", "env", name)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("resolve go environment %s: %v: %s", name, err, strings.TrimSpace(string(output)))
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		t.Fatalf("resolve go environment %s: empty output", name)
	}
	return value
}

func sourceRepo(t *testing.T) string {
	t.Helper()
	if explicit := strings.TrimSpace(os.Getenv(e2eSourceRepo)); explicit != "" {
		root, err := filepath.Abs(explicit)
		if err != nil {
			t.Fatalf("resolve %s: %v", e2eSourceRepo, err)
		}
		return prepareSourceRepoWithGit(
			t,
			root,
			strings.TrimSpace(os.Getenv("PFM_DEV_REPO_WORK_TREE")),
			strings.TrimSpace(os.Getenv("PFM_DEV_REPO_GIT_DIR")),
		)
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate e2e source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "pfm", "go.mod")); err != nil {
		t.Fatalf("locate repository from e2e source: %v", err)
	}
	return prepareSourceRepo(t, root)
}

func prepareSourceRepo(t *testing.T, root string) string {
	t.Helper()
	workTree := strings.TrimSpace(os.Getenv("PFM_DEV_REPO_WORK_TREE"))
	gitDir := strings.TrimSpace(os.Getenv("PFM_DEV_REPO_GIT_DIR"))
	if workTree == "" || gitDir == "" || filepath.Clean(root) != filepath.Clean(workTree) {
		workTree = ""
		gitDir = ""
	}
	return prepareSourceRepoWithGit(t, root, workTree, gitDir)
}

func prepareSourceRepoWithGit(t *testing.T, root, workTree, gitDir string) string {
	t.Helper()
	fixture := filepath.Join(t.TempDir(), "source")
	if err := copySourceTreeWithGit(root, fixture, workTree, gitDir); err != nil {
		t.Fatalf("stage e2e source repository: %v", err)
	}
	if _, err := os.Stat(filepath.Join(fixture, filepath.FromSlash(e2eFixtureSkill))); errors.Is(err, fs.ErrNotExist) {
		path := filepath.Join(fixture, filepath.FromSlash(e2eFixtureSkill))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatalf("stage e2e fixture skill directory: %v", err)
		}
		if err := os.WriteFile(path, []byte("# E2E fixture skill\n"), 0o600); err != nil {
			t.Fatalf("stage e2e fixture skill: %v", err)
		}
	} else if err != nil {
		t.Fatalf("inspect e2e fixture skill: %v", err)
	}

	previousTag := strings.TrimSpace(os.Getenv(e2ePreviousTag))
	if previousTag == "" {
		previousTag = "v0.0.1"
	}
	if !isReleaseTag(previousTag) {
		t.Fatalf("invalid %s=%q: want semantic release tag", e2ePreviousTag, previousTag)
	}
	runGitFixture(t, fixture, "init", "-q")
	runGitFixture(t, fixture, "config", "user.email", "fixture.invalid")
	runGitFixture(t, fixture, "config", "user.name", "fixture-identity")
	runGitFixture(t, fixture, "config", "core.hooksPath", ".githooks")
	runGitFixture(t, fixture, "add", "-A")
	runGitFixture(t, fixture, "add", "-f", filepath.ToSlash(e2eFixtureSkill))
	runGitFixture(t, fixture, "commit", "-qm", "fixture previous release")
	runGitFixture(t, fixture, "tag", previousTag)
	if err := os.WriteFile(filepath.Join(fixture, ".e2e-current-source"), []byte("current\n"), 0o600); err != nil {
		t.Fatalf("stage e2e current source marker: %v", err)
	}
	runGitFixture(t, fixture, "add", ".e2e-current-source")
	runGitFixture(t, fixture, "commit", "-qm", "fixture current source")
	runGitFixture(t, fixture, "tag", currentE2ETag(t))
	runGitFixture(t, fixture, "remote", "add", "origin", fixture)
	return fixture
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
	workTree := strings.TrimSpace(os.Getenv("PFM_DEV_REPO_WORK_TREE"))
	gitDir := strings.TrimSpace(os.Getenv("PFM_DEV_REPO_GIT_DIR"))
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
	gitArgs := []string{"-c", "safe.directory=" + source, "-C", source, "ls-files", "--cached", "--others", "--exclude-standard", "-z"}
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
		if relative == "." || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
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
			if filepath.IsAbs(linkTarget) || withinSource == ".." || strings.HasPrefix(withinSource, ".."+string(filepath.Separator)) {
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

func runGitFixture(t *testing.T, directory string, args ...string) {
	t.Helper()
	result := runGit(directory, args...)
	if result.err != nil {
		t.Fatalf("source fixture git %s: %v\n%s", strings.Join(args, " "), result.err, strings.TrimSpace(result.stdout+result.stderr))
	}
}

func (h *e2eHarness) build(source, output string) string {
	h.t.Helper()
	if explicit := strings.TrimSpace(os.Getenv(e2eBinaryEnv)); explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			h.t.Fatalf("%s=%s: %v", e2eBinaryEnv, explicit, err)
		}
		return explicit
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		h.t.Fatal(err)
	}
	command := exec.Command("go", "-C", filepath.Join(source, "pfm"), "build", "-o", output, "./cmd/pfm")
	command.Dir = source
	command.Env = appendCleanEnv(os.Environ(), map[string]string{
		"GOFLAGS": "",
		"HOME":    os.Getenv("HOME"),
	})
	outputBytes, err := command.CombinedOutput()
	if err != nil {
		h.t.Fatalf("build %s: %v\n%s", output, err, strings.TrimSpace(string(outputBytes)))
	}
	return output
}

func (h *e2eHarness) newHome(binary string) string {
	h.t.Helper()
	home, err := os.MkdirTemp("/tmp", "pfm-e2e-home-")
	if err != nil {
		h.t.Fatalf("create short e2e home: %v", err)
	}
	h.t.Cleanup(func() {
		if err := os.RemoveAll(home); err != nil {
			h.t.Errorf("remove short e2e home: %v", err)
		}
	})
	for _, relative := range []string{
		".claude", ".cc/1/projects", ".cc/2/projects", ".cc/3/projects",
		".codex", ".config", "proc", "cgroup", "tmux", "tmp", ".local/bin",
	} {
		if err := os.MkdirAll(filepath.Join(home, relative), 0o700); err != nil {
			h.t.Fatal(err)
		}
	}
	for _, relative := range managedSettings {
		path := filepath.Join(home, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			h.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			h.t.Fatal(err)
		}
	}
	if err := copyFile(binary, filepath.Join(home, e2eCanonicalPFM), 0o755); err != nil {
		h.t.Fatalf("stage pfm binary: %v", err)
	}
	testBinary, err := os.Executable()
	if err != nil {
		h.t.Fatal(err)
	}
	// Named a real vMAJOR.MINOR.PATCH string (matching the fixture's own
	// --version output below) rather than an arbitrary "fixture": pfm's
	// launcher now selects the versions/ candidate by parsed semantic
	// version, in Go, and an unparsed name is never chosen (see
	// internal/installer/claude_versions.go).
	native := filepath.Join(home, ".local", "share", "claude", "versions", "2.1.238")
	launcherEvidence := filepath.Join(home, "launcher-evidence")
	body := "#!/bin/sh\n" +
		"if [ \"${1-}\" = -p ]; then exec env PFM_E2E_CLAUDE_CAPTURE=1 " + shellQuoteFixture(testBinary) + " -test.run '^TestClaudeHarnessCaptureFixture$' -- \"$@\"; fi\n" +
		"if [ \"${1-}\" = --version ]; then printf '2.1.238 (Claude Code)\\n'; exit 0; fi\n" +
		"printf '%s\\n' \"${TMUX%%,*}\" > " + shellQuoteFixture(launcherEvidence) + "\n" +
		"exit 0\n"
	if err := os.MkdirAll(filepath.Dir(native), 0o700); err != nil {
		h.t.Fatal(err)
	}
	if err := os.WriteFile(native, []byte(body), 0o700); err != nil {
		h.t.Fatal(err)
	}
	if err := os.Symlink(native, filepath.Join(home, e2eCanonicalClaude)); err != nil {
		h.t.Fatal(err)
	}
	codex := filepath.Join(home, ".local", "bin", "codex")
	codexBody := "#!/bin/sh\n" +
		"if [ \"${1-}\" = app-server ]; then exec env PFM_E2E_CODEX_HOOK_FIXTURE=1 " + shellQuoteFixture(testBinary) + " -test.run '^TestCodexHookAPIFixture$'; fi\n" + `
if [ "${1-}" = --version ]; then printf 'codex-cli 0.149.0\n'; exit 0; fi
if [ "${1-}" = doctor ] && [ "${2-}" = --help ]; then printf 'usage: codex doctor\n'; exit 0; fi
if [ "${1-}" = doctor ]; then printf 'healthy\n'; exit 0; fi
exit 2
`
	if err := os.WriteFile(codex, []byte(codexBody), 0o700); err != nil {
		h.t.Fatal(err)
	}
	auth := filepath.Join(home, ".codex", "auth.json")
	if err := os.WriteFile(auth, []byte(`{"tokens":{"access_token":"fixture-token","account_id":"fixture-account"}}`+"\n"), 0o600); err != nil {
		h.t.Fatal(err)
	}
	stageSchedulerFixtures(h.t, home)
	return home
}

func shellQuoteFixture(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (h *e2eHarness) pfm(home string, args ...string) commandResult {
	return h.pfmWithBinary(filepath.Join(home, e2eCanonicalPFM), home, args...)
}

func (h *e2eHarness) pfmWithBinary(binary, home string, args ...string) commandResult {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = h.repo
	command.Env = h.environment(home)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("command timed out")
	}
	return commandResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func (h *e2eHarness) environment(home string) []string {
	h.t.Helper()
	path := filepath.Join(home, ".local", "bin") + string(os.PathListSeparator) + os.Getenv("PATH")
	roots := []string{
		filepath.Join(home, ".cc", "1", "projects"),
		filepath.Join(home, ".cc", "2", "projects"),
		filepath.Join(home, ".cc", "3", "projects"),
	}
	values := map[string]string{
		"GOCACHE":               h.goCache,
		"GOMODCACHE":            h.goModCache,
		"HOME":                  home,
		"PFM_HOME":              home,
		"PFM_DB":                filepath.Join(home, ".local", "state", "pfm", "fleet.db"),
		"PFM_SHARED_DB":         filepath.Join(home, ".cc", "fleet.db"),
		"PFM_SID_DIR":           filepath.Join(home, "sid"),
		"PFM_CLAUDE_ROOTS":      strings.Join(roots, string(os.PathListSeparator)),
		"PFM_CODEX_ROOT":        filepath.Join(home, ".codex"),
		"PFM_TMUX_DIR":          filepath.Join(home, "tmux"),
		"PFM_TMUX_CONF":         filepath.Join(home, "tmux.conf"),
		"PFM_PROC_ROOT":         filepath.Join(home, "proc"),
		"PFM_CGROUP_ROOT":       filepath.Join(home, "cgroup"),
		"TMUX_TMPDIR":           filepath.Join(home, "tmux"),
		"TMPDIR":                filepath.Join(home, "tmp"),
		"XDG_CONFIG_HOME":       filepath.Join(home, ".config"),
		"PATH":                  path,
		e2eSourceRepo:           h.repo,
		e2eHomeEnv:              home,
		"PFM_HARVESTPY_OFFLINE": "1",
	}
	return appendCleanEnv(os.Environ(), values)
}

func appendCleanEnv(base []string, values map[string]string) []string {
	result := make([]string, 0, len(base)+len(values))
	for _, entry := range base {
		name, _, ok := strings.Cut(entry, "=")
		_, overridden := values[name]
		if !ok || overridden || name == "HOME" || name == "PATH" || strings.HasPrefix(name, "PFM_") ||
			name == "TMUX" || name == "TMUX_TMPDIR" || name == "TMPDIR" || name == "XDG_CONFIG_HOME" {
			continue
		}
		result = append(result, entry)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

func (h *e2eHarness) requireSuccess(phase string, result commandResult) {
	h.t.Helper()
	if result.err == nil {
		return
	}
	differences := []string{"command exit"}
	output := result.stdout + result.stderr
	if strings.Contains(strings.ToLower(output), "harvestpy") && !strings.Contains(output, e2eHarvestSkipLine) {
		differences = append(differences, "harvestpy provisioning attempted; expected blocked, not attempted")
	}
	h.t.Fatalf(
		"%s failed; differing paths: %s; status: %v; stdout=%q stderr=%q",
		phase, strings.Join(differences, ", "), result.err, result.stdout, result.stderr,
	)
}

func (h *e2eHarness) requireHarvestGate(phase string, result commandResult) {
	h.t.Helper()
	if !strings.Contains(result.stdout+result.stderr, e2eHarvestSkipLine) {
		h.t.Fatalf("%s failed; differing paths: harvestpy gate output; want %q", phase, e2eHarvestSkipLine)
	}
}

func (h *e2eHarness) requireSkippedHarvestDoctor(result commandResult) {
	h.t.Helper()
	output := result.stdout + result.stderr
	if result.err == nil {
		h.t.Fatalf("doctor after --skip-harvest succeeded, want named unprovisioned dependencies; output=%q", output)
	}
	for _, want := range []string{
		"doctor: dep uv path= broken",
		"doctor: dep harvestpy path= broken",
		"doctor: harvestpy skipped",
		"doctor: pre-push gate=armed core.hooksPath=.githooks",
		"doctor: harness-prompt: matches baseline",
		"doctor: warnings=2",
	} {
		if !strings.Contains(output, want) {
			h.t.Fatalf("doctor after --skip-harvest omitted %q; stdout=%q stderr=%q", want, result.stdout, result.stderr)
		}
	}
	// M2 (issue #24 finding 1): the unprovisioned harvestpy sidecar deps
	// (uv, harvestpy) above are warnings, not failures — the fleet engine
	// runs without them, and `--skip-harvest` is pfm's own decision not to
	// provision them. `doctor: failures=` must never appear here.
	if strings.Contains(output, "doctor: failures=") {
		h.t.Fatalf("doctor after --skip-harvest printed a failures= line for warnings-only rows; stdout=%q stderr=%q", result.stdout, result.stderr)
	}
}

func (h *e2eHarness) assertInstalled(home string) {
	h.t.Helper()
	managed := filepath.Join(home, e2eManagedRoot)
	if info, err := os.Stat(managed); err != nil || !info.IsDir() {
		h.t.Fatalf("install surface failed; differing paths: %s; status: %v", e2eManagedRoot, err)
	}
	for _, relative := range managedAssets {
		if _, err := os.Stat(filepath.Join(managed, relative)); err != nil {
			h.t.Fatalf("install surface failed; differing paths: %s; status: %v", filepath.Join(e2eManagedRoot, relative), err)
		}
	}
	for _, relative := range []string{"source-repo", "binary-ownership.json", "settings-hook-ownership.json"} {
		if _, err := os.Stat(filepath.Join(managed, relative)); err != nil {
			h.t.Fatalf("install surface failed; differing paths: %s; status: %v", filepath.Join(e2eManagedRoot, relative), err)
		}
	}
	canonicalClaude := filepath.Join(home, e2eCanonicalClaude)
	managedClaude := filepath.Join(managed, "bin", "claude")
	info, err := os.Lstat(canonicalClaude)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		h.t.Fatalf("install surface failed; differing paths: %s launcher link; status: %v", e2eCanonicalClaude, err)
	}
	target, err := os.Readlink(canonicalClaude)
	if err != nil || filepath.Clean(target) != filepath.Clean(managedClaude) {
		h.t.Fatalf("install surface failed; differing paths: %s target=%q; status: %v", e2eCanonicalClaude, target, err)
	}
	if _, err := os.Stat(filepath.Join(managed, "launcher.state")); err != nil {
		h.t.Fatalf("install surface failed; differing paths: launcher.state; status: %v", err)
	}
	h.readJSON(filepath.Join(managed, "binary-ownership.json"))
	h.readJSON(filepath.Join(managed, "settings-hook-ownership.json"))
	if runtime.GOOS == "linux" {
		for _, relative := range []string{
			"systemd/pfm-name-sync.path", "systemd/pfm-name-sync.service", "systemd/pfm-name-sync.timer",
		} {
			if _, err := os.Stat(filepath.Join(managed, relative)); err != nil {
				h.t.Fatalf("install surface failed; differing paths: %s; status: %v", filepath.Join(e2eManagedRoot, relative), err)
			}
		}
	}
	for _, relative := range managedSettings {
		document := h.readJSON(filepath.Join(home, relative))
		if got, ok := document["cleanupPeriodDays"].(float64); !ok || got != 36500 {
			h.t.Fatalf("install surface failed; differing paths: %s cleanupPeriodDays", relative)
		}
		for _, suffix := range expectedHooks {
			if !containsHookCommand(document, filepath.Join(home, ".local", "bin", "pfm")+" "+suffix) {
				h.t.Fatalf("install surface failed; differing paths: %s hook %s", relative, suffix)
			}
		}
	}
	for _, relative := range commandLinks {
		path := filepath.Join(home, e2eCommandRoot, relative)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			h.t.Fatalf("install surface failed; differing paths: %s; status: %v", relative, err)
		}
		if _, err := filepath.EvalSymlinks(path); err != nil {
			h.t.Fatalf("install surface failed; differing paths: %s unresolved symlink; status: %v", relative, err)
		}
	}
	shim := filepath.Join(managed, "shim", "pfm.zsh")
	if result := runTool(home, "zsh", "-n", shim); result.err != nil {
		h.t.Fatalf("install surface failed; differing paths: shim/pfm.zsh syntax; status: %v", result.err)
	}
	if !hasSourceLine(filepath.Join(home, e2eZshrc), shim) {
		h.t.Fatalf("install surface failed; differing paths: .zshrc source line")
	}
	h.assertTmuxConfig(home)
	codexHooksPath := filepath.Join(home, e2eCodexHooks)
	if raw, err := os.ReadFile(codexHooksPath); err == nil {
		var codex map[string]any
		if err := json.Unmarshal(raw, &codex); err != nil {
			h.t.Fatalf("install surface failed; differing paths: .codex/hooks.json parse; status: %v", err)
		}
		if containsJSONString(codex, filepath.Join(home, ".local", "bin", "pfm")+" internal clear-kill") {
			h.t.Fatal("install surface failed; differing paths: .codex/hooks.json retained retired clear-kill")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		h.t.Fatalf("install surface failed; differing paths: .codex/hooks.json; status: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, e2eSourceMarker)); err != nil {
		h.t.Fatalf("install surface failed; differing paths: %s; status: %v", e2eSourceMarker, err)
	}
	if _, err := os.Stat(filepath.Join(home, e2eCanonicalPFM)); err != nil {
		h.t.Fatalf("install surface failed; differing paths: %s; status: %v", e2eCanonicalPFM, err)
	}
	if runtime.GOOS == "linux" {
		for _, name := range []string{"pfm-name-sync.path", "pfm-name-sync.service", "pfm-name-sync.timer"} {
			if _, err := os.Stat(filepath.Join(home, ".config", "systemd", "user", name)); err != nil {
				h.t.Fatalf("install surface failed; differing paths: systemd/%s; status: %v", name, err)
			}
		}
	} else if _, err := os.Stat(filepath.Join(home, "Library", "LaunchAgents", "com.professor.pfm.name-sync.plist")); err != nil {
		h.t.Fatalf("install surface failed; differing paths: launchd name-sync; status: %v", err)
	}
}

func (h *e2eHarness) assertLauncherRuntime(home string) {
	h.t.Helper()
	tmuxBefore, err := os.ReadDir(filepath.Join(home, "tmux"))
	if err != nil {
		h.t.Fatal(err)
	}
	version := h.tool(home, filepath.Join(home, e2eCanonicalClaude), "--version")
	if version.err != nil || version.stdout != "2.1.238 (Claude Code)\n" {
		h.t.Fatalf("launcher version pass-through failed: output=%q stderr=%q status=%v", version.stdout, version.stderr, version.err)
	}
	tmuxAfter, err := os.ReadDir(filepath.Join(home, "tmux"))
	if err != nil {
		h.t.Fatal(err)
	}
	if strings.Join(dirEntryNames(tmuxAfter), "\x00") != strings.Join(dirEntryNames(tmuxBefore), "\x00") {
		h.t.Fatalf("launcher version pass-through changed tmux state: before=%v after=%v", tmuxBefore, tmuxAfter)
	}
	interactive := h.tool(home, filepath.Join(home, e2eCanonicalClaude), "--resume", "fixture-session")
	if interactive.err != nil {
		h.t.Fatalf("interactive no-TTY launcher failed: stdout=%q stderr=%q status=%v", interactive.stdout, interactive.stderr, interactive.err)
	}
	if !strings.HasPrefix(interactive.stdout, "pfm launch: cc-") {
		h.t.Fatalf("interactive no-TTY launcher omitted socket line: %q", interactive.stdout)
	}
	evidence, err := os.ReadFile(filepath.Join(home, "launcher-evidence"))
	if err != nil {
		h.t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(strings.TrimSpace(string(evidence))), "cc-") {
		h.t.Fatalf("interactive fake Claude ran outside cc tmux: %q", evidence)
	}
}

func dirEntryNames(entries []os.DirEntry) []string {
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	return names
}

func (h *e2eHarness) tool(home, name string, args ...string) commandResult {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = h.repo
	command.Env = h.environment(home)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("command timed out")
	}
	return commandResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func (h *e2eHarness) assertTmuxConfig(home string) {
	h.t.Helper()
	configuration := filepath.Join(home, "tmux.conf")
	if err := os.WriteFile(configuration, []byte("set -g history-limit 100\n"), 0o600); err != nil {
		h.t.Fatalf("tmux probe setup failed; differing paths: tmux.conf; status: %v", err)
	}
	socketRoot, err := os.MkdirTemp("/tmp", "pfm-e2e-tmux-")
	if err != nil {
		h.t.Fatalf("tmux probe setup failed; create short socket root: %v", err)
	}
	h.t.Cleanup(func() {
		if err := os.RemoveAll(socketRoot); err != nil {
			h.t.Errorf("tmux probe cleanup failed: %v", err)
		}
	})
	socket := filepath.Join(socketRoot, "socket")
	result := runTool(home, "tmux", "-f", configuration, "-S", socket, "new-session", "-d", "-s", "pfm-e2e-probe")
	if result.err != nil {
		h.t.Fatalf(
			"tmux probe failed; differing paths: tmux config/socket; status: %v; stdout=%q stderr=%q",
			result.err, result.stdout, result.stderr,
		)
	}
	cleanup := runTool(home, "tmux", "-S", socket, "kill-server")
	if cleanup.err != nil {
		h.t.Fatalf(
			"tmux probe cleanup failed; status: %v; stdout=%q stderr=%q",
			cleanup.err, cleanup.stdout, cleanup.stderr,
		)
	}
}

func (h *e2eHarness) assertInit(project, source string) {
	h.t.Helper()
	templates := filepath.Join(source, "templates", "project")
	shaResult := runGit(source, "rev-parse", "--short", "HEAD")
	if shaResult.err != nil {
		h.t.Fatalf("resolve init source SHA: %v", shaResult.err)
	}
	sha := strings.TrimSpace(shaResult.stdout)
	for _, mapping := range []struct{ source, target string }{
		{"CLAUDE.md", "CLAUDE.md"},
		{"settings.json", ".claude/settings.json"},
	} {
		h.assertInitFile(
			filepath.Join(templates, mapping.source), filepath.Join(project, mapping.target),
			mapping.target, filepath.ToSlash(filepath.Join("project", mapping.source)), sha,
		)
	}
	for _, mapping := range []struct{ source, target, skip string }{
		{"commands", ".claude/commands", ""},
		{"agents", ".claude/agents", "per-project"},
		{"scripts", ".claude/scripts", ""},
		{"skills", ".claude/skills", ""},
		{"epics", "docs/epics", ""},
		{"codex", ".codex", ""},
		{"docs-commands", "docs/commands", ""},
		{"docs-agents", "docs/agents", ""},
	} {
		sourceDir := filepath.Join(templates, mapping.source)
		targetDir := filepath.Join(project, mapping.target)
		h.assertInitPath(targetDir, mapping.target)
		if err := filepath.WalkDir(sourceDir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if mapping.skip != "" && path == filepath.Join(sourceDir, mapping.skip) {
					return filepath.SkipDir
				}
				return nil
			}
			relative, relErr := filepath.Rel(sourceDir, path)
			if relErr != nil {
				return relErr
			}
			target := filepath.Join(targetDir, relative)
			local := filepath.ToSlash(filepath.Join(mapping.target, relative))
			template := filepath.ToSlash(filepath.Join("project", mapping.source, relative))
			h.assertInitFile(path, target, local, template, sha)
			return nil
		}); err != nil {
			h.t.Fatalf("init scaffold failed; differing paths: %s; status: %v", mapping.source, err)
		}
	}
	h.readJSON(filepath.Join(project, ".claude", "settings.json"))
}

func (h *e2eHarness) assertInitFile(source, target, local, template, sha string) {
	h.t.Helper()
	want, err := os.ReadFile(source)
	if err != nil {
		h.t.Fatalf("read init source %s: %v", source, err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		h.t.Fatalf("init scaffold failed; differing paths: %s; status: %v", local, err)
	}
	marker := []byte(fmt.Sprintf("# pfm-scaffold: %s@%s — this file is YOURS; upstream deltas arrive via pfm update, reviewed and hand-applied\n", template, sha))
	marked := false
	if local != "CLAUDE.md" && local != "AGENTS.md" && strings.HasSuffix(local, ".md") && bytes.HasPrefix(want, []byte("---\n")) {
		prefix := append([]byte("---\n"), marker...)
		if !bytes.HasPrefix(got, prefix) {
			h.t.Fatalf("init scaffold failed; differing paths: %s; exact frontmatter marker absent", local)
		}
		got = append([]byte("---\n"), got[len(prefix):]...)
		marked = true
	}
	if strings.HasSuffix(local, ".sh") {
		if newline := bytes.IndexByte(want, '\n'); newline >= 0 && bytes.HasPrefix(want, []byte("#!")) {
			prefix := append(append([]byte(nil), want[:newline+1]...), marker...)
			if !bytes.HasPrefix(got, prefix) {
				h.t.Fatalf("init scaffold failed; differing paths: %s; exact shebang marker absent", local)
			}
			got = append(append([]byte(nil), want[:newline+1]...), got[len(prefix):]...)
			marked = true
		}
	}
	if !marked && bytes.Contains(got, []byte("# pfm-scaffold:")) {
		h.t.Fatalf("init scaffold failed; differing paths: %s; unexpected scaffold marker", local)
	}
	if !bytes.Equal(got, want) {
		h.t.Fatalf("init scaffold failed; differing paths: %s; bytes do not match after exact marker removal", local)
	}
}

func (h *e2eHarness) assertInitPath(path, relative string) {
	h.t.Helper()
	if _, err := os.Stat(path); err != nil {
		h.t.Fatalf("init scaffold failed; differing paths: %s; status: %v", relative, err)
	}
}

func (h *e2eHarness) previousBinary() string {
	h.t.Helper()
	clone := filepath.Join(h.t.TempDir(), "previous")
	if result := runGit(h.repo, "clone", "--no-local", h.repo, clone); result.err != nil {
		h.t.Fatalf("previous release setup failed; differing paths: local clone; status: %v", result.err)
	}
	tag := strings.TrimSpace(os.Getenv(e2ePreviousTag))
	if tag == "" {
		result := runGit(clone, "tag", "--list", "v*", "--sort=-v:refname")
		if result.err != nil {
			h.t.Fatalf("previous release setup failed; differing paths: release tags; status: %v", result.err)
		}
		for _, candidate := range strings.Fields(result.stdout) {
			if isReleaseTag(candidate) {
				tag = candidate
				break
			}
		}
	}
	if tag == "" {
		h.t.Fatalf("previous release setup failed; differing paths: semantic release tag; status: none found")
	}
	if result := runGit(clone, "checkout", "--detach", "--quiet", tag); result.err != nil {
		h.t.Fatalf("previous release setup failed; differing paths: checkout %s; status: %v", tag, result.err)
	}
	return h.build(clone, filepath.Join(h.t.TempDir(), "pfm-previous"))
}

func isReleaseTag(value string) bool {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) != 3 || !strings.HasPrefix(parts[0], "v") {
		return false
	}
	for index, part := range parts {
		if index == 0 {
			part = strings.TrimPrefix(part, "v")
		}
		if part == "" {
			return false
		}
		if _, err := strconv.Atoi(part); err != nil {
			return false
		}
	}
	return true
}

func (h *e2eHarness) plantManualState(home string) {
	h.t.Helper()
	path := filepath.Join(home, e2eSettings)
	document := h.readJSON(path)
	hooks, _ := document["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		document["hooks"] = hooks
	}
	entries, _ := hooks["UserPromptSubmit"].([]any)
	entries = append(entries, map[string]any{
		"matcher": "",
		"hooks": []any{map[string]any{
			"type": "command", "command": "manual-fixture-hook",
		}},
	})
	hooks["UserPromptSubmit"] = entries
	h.writeJSON(path, document)
	foreign := filepath.Join(home, ".claude", "commands", "foreign-fixture.md")
	if err := os.WriteFile(foreign, []byte("foreign fixture\n"), 0o600); err != nil {
		h.t.Fatal(err)
	}
}

func (h *e2eHarness) assertUninstalled(home string) {
	h.t.Helper()
	for _, relative := range []string{e2eManagedRoot, e2eSourceMarker} {
		if _, err := os.Lstat(filepath.Join(home, relative)); !os.IsNotExist(err) {
			h.t.Fatalf("uninstall failed; differing paths: %s; status: %v", relative, err)
		}
	}
	for _, relative := range commandLinks {
		if _, err := os.Lstat(filepath.Join(home, e2eCommandRoot, relative)); !os.IsNotExist(err) {
			h.t.Fatalf("uninstall failed; differing paths: %s; status: %v", relative, err)
		}
	}
	canonical := filepath.Join(home, e2eCanonicalClaude)
	target, err := os.Readlink(canonical)
	if err != nil || !strings.HasSuffix(filepath.ToSlash(target), "/.local/share/claude/versions/2.1.238") {
		h.t.Fatalf("uninstall failed; differing paths: native Claude launcher restore target=%q status=%v", target, err)
	}
	if runtime.GOOS == "linux" {
		for _, relative := range []string{
			".config/systemd/user/pfm-name-sync.path",
			".config/systemd/user/pfm-name-sync.service",
			".config/systemd/user/pfm-name-sync.timer",
			".config/systemd/user/default.target.wants/pfm-name-sync.path",
			".config/systemd/user/timers.target.wants/pfm-name-sync.timer",
		} {
			if _, err := os.Lstat(filepath.Join(home, relative)); !os.IsNotExist(err) {
				h.t.Fatalf("uninstall failed; differing paths: %s; status: %v", relative, err)
			}
		}
	} else if _, err := os.Lstat(filepath.Join(home, "Library", "LaunchAgents", "com.professor.pfm.name-sync.plist")); !os.IsNotExist(err) {
		h.t.Fatalf("uninstall failed; differing paths: launchd name-sync; status: %v", err)
	}
	if hasSourceLine(filepath.Join(home, e2eZshrc), filepath.Join(home, e2eManagedRoot, "shim", "pfm.zsh")) {
		h.t.Fatalf("uninstall failed; differing paths: .zshrc installer source line")
	}
	document := h.readJSON(filepath.Join(home, e2eSettings))
	if !containsJSONString(document, "manual-fixture-hook") {
		h.t.Fatalf("uninstall failed; differing paths: manual settings hook")
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "commands", "foreign-fixture.md")); err != nil {
		h.t.Fatalf("uninstall failed; differing paths: foreign-fixture.md; status: %v", err)
	}
	if raw, err := os.ReadFile(filepath.Join(home, e2eCodexHooks)); err == nil {
		var codex map[string]any
		if err := json.Unmarshal(raw, &codex); err != nil {
			h.t.Fatalf("uninstall failed; differing paths: .codex/hooks.json parse; status: %v", err)
		}
		if containsJSONString(codex, filepath.Join(home, ".local", "bin", "pfm")+" internal clear-kill") {
			h.t.Fatalf("uninstall failed; differing paths: retired installer Codex hook")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		h.t.Fatalf("uninstall failed; differing paths: .codex/hooks.json; status: %v", err)
	}
}

func (h *e2eHarness) snapshot(home string) (surfaceSnapshot, error) {
	h.t.Helper()
	snapshot := surfaceSnapshot{}
	for _, relative := range []string{e2eManagedRoot, e2eCommandRoot, e2eCodexHooks, e2eZshrc, e2eSettings, ".cc/1/settings.json", ".cc/2/settings.json", ".cc/3/settings.json", ".config/systemd/user", "Library/LaunchAgents"} {
		root := filepath.Join(home, relative)
		if err := addSnapshot(root, home, snapshot); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
	}
	return snapshot, nil
}

func addSnapshot(root, home string, snapshot surfaceSnapshot) error {
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() {
				return walkErr
			}
			return addSnapshotFile(path, home, snapshot)
		})
	}
	return addSnapshotFile(root, home, snapshot)
}

func addSnapshotFile(path, home string, snapshot surfaceSnapshot) error {
	if strings.Contains(filepath.Base(path), ".pre-professor-") {
		return nil
	}
	relative, err := filepath.Rel(home, path)
	if err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	key := filepath.ToSlash(relative)
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return err
		}
		snapshot[key] = "link:" + normalizeHome(target, home)
		return nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256([]byte(normalizeHome(string(body), home)))
	snapshot[key] = "file:" + hex.EncodeToString(sum[:])
	return nil
}

func snapshotDifferences(left, right surfaceSnapshot) []string {
	seen := make(map[string]bool, len(left)+len(right))
	for key := range left {
		seen[key] = true
	}
	for key := range right {
		seen[key] = true
	}
	var differences []string
	for key := range seen {
		if left[key] != right[key] {
			differences = append(differences, key)
		}
	}
	sort.Strings(differences)
	return differences
}

func normalizeHome(value, home string) string {
	return strings.ReplaceAll(value, home, "<E2E_HOME>")
}

func (h *e2eHarness) readJSON(path string) map[string]any {
	h.t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatalf("read JSON %s: %v", path, err)
	}
	var document map[string]any
	if err := json.Unmarshal(body, &document); err != nil {
		h.t.Fatalf("parse JSON %s: %v", path, err)
	}
	return document
}

func (h *e2eHarness) writeJSON(path string, document map[string]any) {
	h.t.Helper()
	body, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		h.t.Fatalf("encode JSON %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		h.t.Fatalf("write JSON %s: %v", path, err)
	}
}

func containsHookCommand(document map[string]any, want string) bool {
	hooks, _ := document["hooks"].(map[string]any)
	for _, value := range hooks {
		entries, _ := value.([]any)
		for _, entryValue := range entries {
			entry, _ := entryValue.(map[string]any)
			inner, _ := entry["hooks"].([]any)
			for _, hookValue := range inner {
				hook, _ := hookValue.(map[string]any)
				if hook["command"] == want {
					return true
				}
			}
		}
	}
	return false
}

func containsJSONString(value any, want string) bool {
	switch typed := value.(type) {
	case string:
		return typed == want
	case []any:
		for _, child := range typed {
			if containsJSONString(child, want) {
				return true
			}
		}
	case map[string]any:
		for _, child := range typed {
			if containsJSONString(child, want) {
				return true
			}
		}
	}
	return false
}

func hasSourceLine(path, shim string) bool {
	body, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	wanted := `[[ -r "` + shim + `" ]] && source "` + shim + `"`
	for _, line := range strings.Split(string(body), "\n") {
		if strings.TrimSpace(line) == wanted {
			return true
		}
	}
	return false
}

func copyFile(source, target string, mode os.FileMode) error {
	body, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	return os.WriteFile(target, body, mode)
}

func runTool(home, name string, args ...string) commandResult {
	command := exec.Command(name, args...)
	command.Env = appendCleanEnv(os.Environ(), map[string]string{
		"HOME": home, "TMPDIR": filepath.Join(home, "tmp"), "TMUX_TMPDIR": filepath.Join(home, "tmux"),
	})
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return commandResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func runGit(directory string, args ...string) commandResult {
	command := exec.Command("git", args...)
	command.Dir = directory
	command.Env = appendCleanEnv(os.Environ(), map[string]string{"GIT_CONFIG_NOSYSTEM": "1"})
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return commandResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}
