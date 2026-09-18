package professor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"hostops/pfm/internal/deps"
	pfmpaths "hostops/pfm/internal/paths"
)

func TestStoreSHAWithRunnerUsesScriptedGit(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	fake := &deps.FakeRunner{}
	binary := deps.Executable("git")
	fake.Script([]string{binary, "rev-parse", "--short", "HEAD"}, deps.RunResult{Stdout: []byte("scripted-sha\n")}, nil)
	got, err := storeSHAWithRunner(root, fake)
	if err != nil {
		t.Fatalf("storeSHAWithRunner() error = %v", err)
	}
	if got != "scripted-sha" {
		t.Fatalf("storeSHAWithRunner() = %q, want scripted-sha", got)
	}
	calls := fake.Calls()
	if len(calls) != 1 || calls[0].Opts.Dir != root {
		t.Fatalf("Git calls = %#v, want one call in %q", calls, root)
	}
}

func TestStoreSHAWithRunnerReportsGitExitAndStartFailures(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	binary := deps.Executable("git")

	t.Run("nonzero exit", func(t *testing.T) {
		fake := &deps.FakeRunner{}
		fake.Script(
			[]string{binary, "rev-parse", "--short", "HEAD"},
			deps.RunResult{ExitCode: 128, Stderr: []byte("fatal: no HEAD\n")},
			nil,
		)
		_, err := storeSHAWithRunner(root, fake)
		if err == nil || !strings.Contains(err.Error(), "status 128") ||
			!strings.Contains(err.Error(), "fatal: no HEAD") {
			t.Fatalf("storeSHAWithRunner() error = %v, want exit status and stderr", err)
		}
	})

	t.Run("start failure", func(t *testing.T) {
		fake := &deps.FakeRunner{}
		fake.Script(
			[]string{binary, "rev-parse", "--short", "HEAD"},
			deps.RunResult{ExitCode: -1},
			errors.New("git unavailable"),
		)
		_, err := storeSHAWithRunner(root, fake)
		if err == nil || !strings.Contains(err.Error(), "UNREADABLE blueprint git state") ||
			!strings.Contains(err.Error(), "git unavailable") {
			t.Fatalf("storeSHAWithRunner() error = %v, want start failure context", err)
		}
	})
}

func TestStoreSHAWithRunnerPassesFenceGitEnvironment(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(t.TempDir(), "git-common")
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(pfmpaths.EnvDevRepoWorkTree, root)
	t.Setenv(pfmpaths.EnvDevRepoGitDir, gitDir)

	fake := &deps.FakeRunner{}
	binary := deps.Executable("git")
	fake.Script(
		[]string{binary, "rev-parse", "--short", "HEAD"},
		deps.RunResult{Stdout: []byte("fenced-sha\n")},
		nil,
	)
	got, err := storeSHAWithRunner(root, fake)
	if err != nil {
		t.Fatalf("storeSHAWithRunner() error = %v", err)
	}
	if got != "fenced-sha" {
		t.Fatalf("storeSHAWithRunner() = %q, want fenced-sha", got)
	}
	calls := fake.Calls()
	if len(calls) != 1 {
		t.Fatalf("Git calls = %#v, want one call", calls)
	}
	if calls[0].Opts.Dir != root {
		t.Fatalf("Git Dir = %q, want %q", calls[0].Opts.Dir, root)
	}
	if got := environmentValue(calls[0].Opts.Env, "GIT_DIR"); got != gitDir {
		t.Fatalf("GIT_DIR = %q, want %q", got, gitDir)
	}
	if got := environmentValue(calls[0].Opts.Env, "GIT_WORK_TREE"); got != root {
		t.Fatalf("GIT_WORK_TREE = %q, want %q", got, root)
	}
}

func environmentValue(environment []string, name string) string {
	for _, entry := range environment {
		if key, value, found := strings.Cut(entry, "="); found && key == name {
			return value
		}
	}
	return ""
}

func TestHashTemplateUsesExactBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "template.md")
	if err := os.WriteFile(path, []byte("tokens {INTACT}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := HashTemplate(path)
	if err != nil {
		t.Fatalf("HashTemplate() error = %v", err)
	}
	if want := "sha256:412cbdff5f79034e7859d0a78867404b09b44d2a46c8f38509ae8dc169563df6"; got != want {
		t.Fatalf("HashTemplate() = %q, want %q", got, want)
	}
}

func TestHashTemplateNamesUnreadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "template.md")
	if err := os.WriteFile(path, []byte("secret\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	_, err := HashTemplate(path)
	if err == nil || !strings.Contains(err.Error(), "UNREADABLE") ||
		!strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("HashTemplate() error = %v, want UNREADABLE with OS error", err)
	}
}

func TestResolveStoreDefaultsToSelfHostedUnknownWithoutGit(t *testing.T) {
	home := t.TempDir()
	blueprint := filepath.Join(home, ".professor")
	if err := os.MkdirAll(filepath.Join(blueprint, "templates", "project"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blueprint, "VERSION"), []byte("0.65.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := ResolveStore(t.TempDir(), home)
	if err != nil {
		t.Fatalf("ResolveStore() error = %v", err)
	}
	if store.Root != blueprint || store.Version != "0.65.0" || store.SHA != "self-hosted@unknown" {
		t.Fatalf("ResolveStore() = %#v", store)
	}
}

func TestStoreSHAUsesFenceGitContractForLinkedWorktree(t *testing.T) {
	linkedRoot, gitDir, want := linkedWorktreeStoreFixture(t)
	t.Setenv(pfmpaths.EnvDevRepoWorkTree, "  "+linkedRoot+"  ")
	t.Setenv(pfmpaths.EnvDevRepoGitDir, "  "+gitDir+"  ")

	got, err := storeSHA(linkedRoot)
	if err != nil {
		t.Fatalf("storeSHA() error = %v", err)
	}
	if got != want {
		t.Fatalf("storeSHA() = %q, want %q", got, want)
	}
}

func TestStoreSHAUsesFenceGitContractForSymlinkedBlueprintRoot(t *testing.T) {
	linkedRoot, gitDir, want := linkedWorktreeStoreFixture(t)
	alias := filepath.Join(t.TempDir(), "blueprint")
	if err := os.Symlink(linkedRoot, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv(pfmpaths.EnvDevRepoWorkTree, linkedRoot)
	t.Setenv(pfmpaths.EnvDevRepoGitDir, gitDir)

	got, err := storeSHA(alias)
	if err != nil {
		t.Fatalf("storeSHA() error = %v", err)
	}
	if got != want {
		t.Fatalf("storeSHA() = %q, want %q", got, want)
	}
}

func linkedWorktreeStoreFixture(t *testing.T) (string, string, string) {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repository")
	runStoreGit(t, "init", "-q", repository)
	runStoreGit(t, "-C", repository, "config", "user.email", "fixture.invalid")
	runStoreGit(t, "-C", repository, "config", "user.name", "fixture-identity")
	if err := os.WriteFile(filepath.Join(repository, "tracked.txt"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runStoreGit(t, "-C", repository, "add", "tracked.txt")
	runStoreGit(t, "-C", repository, "commit", "-qm", "fixture")

	linkedRoot := filepath.Join(t.TempDir(), "linked-worktree")
	runStoreGit(t, "-C", repository, "worktree", "add", "--detach", "-q", linkedRoot, "HEAD")
	gitDir := runStoreGit(t, "-C", linkedRoot, "rev-parse", "--git-dir")
	want := runStoreGit(t, "-C", linkedRoot, "rev-parse", "--short", "HEAD")
	if err := os.WriteFile(
		filepath.Join(linkedRoot, ".git"),
		[]byte("gitdir: /nonexistent/host/path\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	return linkedRoot, gitDir, want
}

func TestStoreSHAKeepsBrokenGitFileUnreadableWithoutFenceContract(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, ".git"),
		[]byte("gitdir: /nonexistent/host/path\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv(pfmpaths.EnvDevRepoWorkTree, "")
	t.Setenv(pfmpaths.EnvDevRepoGitDir, "")

	got, err := storeSHA(root)
	if err == nil || !strings.Contains(err.Error(), "UNREADABLE blueprint git state") {
		t.Fatalf("storeSHA() = %q, %v; want UNREADABLE error", got, err)
	}
	if got == UnknownSelfHostedSHA {
		t.Fatalf("storeSHA() = %q; broken git metadata must not look absent", got)
	}
}

func runStoreGit(t *testing.T, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
