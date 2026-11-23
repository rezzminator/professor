//go:build jail

package installer

// A git whose grandchild keeps stderr open must still return within its bound: the real process group and pipes are the subject.
// A source-fetched skill's clone and fetch must leave a parent repository and a home repository's config alone: real git's
// own discovery and config reading are the subject, which no fake git reproduces.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// TestRunSkillGitReturnsWithinItsBound pins F4: a git call whose grandchild
// keeps stderr open still returns shortly after its timeout, carrying git's
// stderr tail. The timeout is timed on a fake, fired once the fake git has
// printed its line; the grace after the kill is os/exec's own WaitDelay.
func TestRunSkillGitReturnsWithinItsBound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	printed := filepath.Join(dir, "printed")
	script := filepath.Join(dir, "git")
	if err := testjail.WriteExecutable(
		script,
		[]byte("#!/bin/sh\necho 'fatal: slow remote' >&2\n: > '"+printed+"'\n"+
			testwait.Hold+" &\n"+testwait.Hold+"\n"),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	const timeout = 200 * time.Millisecond
	fake := clock.NewFake(time.Unix(0, 0))
	ctx, cancel := clock.WithTimeout(context.Background(), fake, timeout)
	defer cancel()
	returned := make(chan error, 1)
	go func() {
		_, err := runSkillGitContext(
			ctx, deps.RealRunner{}, timeout, 100*time.Millisecond, script, t.TempDir(), "clone",
		)
		returned <- err
	}()
	testwait.Until(t, "the fake git to print its stderr line ("+printed+")", func() (bool, string) {
		_, err := os.Stat(printed)
		return err == nil, fmt.Sprintf("stat %s: %v", printed, err)
	})
	fake.Advance(timeout)
	err, _ := testwait.Receive(t, "the timed-out git call to return", returned)
	if err == nil || err.Error() != "git clone timed out after 200ms: fatal: slow remote" {
		t.Fatalf("timeout error does not carry git's stderr tail: %v", err)
	}
}

// skillFixtureGit runs real git in dir with a fixed identity, for the tests
// whose subject is real git's own behaviour; fixtures are local repositories
// only — no test here reaches a network.
func skillFixtureGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{
		"-C", dir, "-c", "user.name=fixture",
		"-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false",
	}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
}

// skillRealRepo creates a real git repository at dir holding files, committed.
func skillRealRepo(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillFixtureGit(t, dir, "init", "--quiet")
	for name, content := range files {
		writeFixture(t, filepath.Join(dir, name), content)
	}
	skillFixtureGit(t, dir, "add", "-A")
	skillFixtureGit(t, dir, "commit", "--quiet", "-m", "fixture")
	return dir
}

// realSkillGit runs a fetch through the real git on PATH.
func realSkillGit(options *Options) { options.ProcessRunner = nil }

// cleanGitOutput runs git in dir with every inherited GIT_* variable stripped,
// so a test's own GIT_DIR never steers the probe that checks for damage.
func cleanGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			command.Env = append(command.Env, entry)
		}
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, output)
	}
	return strings.TrimSpace(string(output))
}

// TestSourceFetchedSkillsNeverTouchAParentRepository pins F1: a store with no
// .git of its own, inside a $HOME that IS a git repository — reached by git
// discovery, or named by an inherited GIT_DIR — is replaced by a fresh clone,
// and the parent repository, its tracked and its untracked files stay as they
// were. Serial: the GIT_DIR case sets the process environment.
func TestSourceFetchedSkillsNeverTouchAParentRepository(t *testing.T) {
	for _, inherited := range []bool{false, true} {
		name := "discovery"
		if inherited {
			name = "GIT_DIR"
		}
		t.Run(name, func(t *testing.T) { requireParentRepositoryUntouched(t, inherited) })
	}
}

func requireParentRepositoryUntouched(t *testing.T, inherited bool) {
	t.Helper()
	{
		home := t.TempDir()
		skillRealRepo(t, home, map[string]string{"tracked.txt": "parent\n"})
		writeFixture(t, filepath.Join(home, "untracked.txt"), "mine\n")
		parentHead := cleanGitOutput(t, home, "rev-parse", "HEAD")
		repo := skillRealRepo(t, filepath.Join(t.TempDir(), "skill"), map[string]string{"SKILL.md": "# new\n"})
		writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"skill": "file://" + repo}))
		store := filepath.Join(skillStoreRoot(home), "skill")
		writeFixture(t, filepath.Join(store, "SKILL.md"), "# hand-made, no .git\n")
		if inherited {
			t.Setenv("GIT_DIR", filepath.Join(home, ".git"))
		}

		output := runSkillInstall(t, home, ModeApply, realSkillGit)

		if got := cleanGitOutput(t, home, "rev-parse", "HEAD"); got != parentHead {
			t.Fatalf("GIT_DIR=%v: parent HEAD moved %s -> %s\n%s", inherited, parentHead, got, output)
		}
		if got := readSkillFile(t, filepath.Join(home, "tracked.txt")); got != "parent\n" {
			t.Fatalf("GIT_DIR=%v: parent tracked file = %q\n%s", inherited, got, output)
		}
		if got := readSkillFile(t, filepath.Join(home, "untracked.txt")); got != "mine\n" {
			t.Fatalf("GIT_DIR=%v: parent untracked file = %q\n%s", inherited, got, output)
		}
		requireNoPath(t, filepath.Join(home, "SKILL.md"), "the skill tree landed in the parent worktree")
		if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# new\n" {
			t.Fatalf("GIT_DIR=%v: store SKILL.md = %q, want the fresh clone\n%s", inherited, got, output)
		}
	}
}

// TestSourceFetchedSkillsIgnoreAHomeRepositoryConfig pins F24: a dotfiles
// repository at $HOME whose config rewrites every URL never reaches a skill
// fetch — no git call discovers it.
func TestSourceFetchedSkillsIgnoreAHomeRepositoryConfig(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	skillFixtureGit(t, home, "init", "--quiet")
	skillFixtureGit(t, home, "config", "url.file:///nonexistent-rewrite/.insteadOf", "file://")
	repo := skillRealRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# gs\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))

	output := runSkillInstall(t, home, ModeApply, realSkillGit)

	if strings.Contains(output, "SKILL-FETCH-FAILED") {
		t.Fatalf("a home repository's config steered the fetch:\n%s", output)
	}
	if got := readSkillFile(t, filepath.Join(skillStoreRoot(home), "god-speed", "SKILL.md")); got != "# gs\n" {
		t.Fatalf("store SKILL.md = %q\n%s", got, output)
	}
}
