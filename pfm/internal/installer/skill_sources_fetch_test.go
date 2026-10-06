package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// skillCommitMarker is the pfm-owned sibling recording the commit a store was
// cloned at — beside the store, never inside the fetched tree.
func skillCommitMarker(home, name string) string {
	return filepath.Join(skillStoreRoot(home), "."+name+".commit")
}

func symlinkFixture(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func repoHead(t *testing.T, repo string) string {
	t.Helper()
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse %s: %v", repo, err)
	}
	return strings.TrimSpace(string(head))
}

// TestSourceFetchedSkillsCommitMarkerNeverFollowsTheFetchedTree pins F19: a
// repo shipping .pfm-skill-commit as a link to a file outside the store, a
// dangling link, a directory or a forged file never steers a write outside the
// store, and the next install still skips the up-to-date store.
func TestSourceFetchedSkillsCommitMarkerNeverFollowsTheFetchedTree(t *testing.T) {
	t.Parallel()
	cases := map[string]func(t *testing.T, repo, outside string){
		"symlink": func(t *testing.T, repo, outside string) {
			symlinkFixture(t, filepath.Join(outside, "victim"), filepath.Join(repo, ".pfm-skill-commit"))
		},
		"dangling symlink": func(t *testing.T, repo, outside string) {
			symlinkFixture(t, filepath.Join(outside, "created"), filepath.Join(repo, ".pfm-skill-commit"))
		},
		"directory": func(t *testing.T, repo, _ string) {
			writeFixture(t, filepath.Join(repo, ".pfm-skill-commit", "keep"), "dir\n")
		},
		"file": func(t *testing.T, repo, _ string) {
			writeFixture(t, filepath.Join(repo, ".pfm-skill-commit"), "forged\n")
		},
	}
	for name, plant := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			home, outside := t.TempDir(), t.TempDir()
			victim := filepath.Join(outside, "victim")
			writeFixture(t, victim, "PRECIOUS\n")
			repo := filepath.Join(t.TempDir(), "gs")
			plant(t, repo, outside)
			skillFixtureRepo(t, repo, map[string]string{"SKILL.md": "# gs\n"})
			writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))

			first := runSkillInstall(t, home, ModeApply)
			second := runSkillInstall(t, home, ModeApply)

			if got := readSkillFile(t, victim); got != "PRECIOUS\n" {
				t.Fatalf("a file outside the store was rewritten: %q\n%s", got, first)
			}
			entries, err := os.ReadDir(outside)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 || entries[0].Name() != "victim" {
				t.Fatalf("install created paths outside the store: %v\n%s", entries, first)
			}
			store := filepath.Join(skillStoreRoot(home), "god-speed")
			if got := readSkillFile(t, skillCommitMarker(home, "god-speed")); got != repoHead(t, repo)+"\n" {
				t.Fatalf("commit record = %q, want the cloned head", got)
			}
			if !strings.Contains(second, "ok      "+store+" at ") {
				t.Fatalf("the up-to-date store was not skipped on the next install:\n%s", second)
			}
		})
	}
}

// TestSourceFetchedSkillsTreeProblemsAreOneSkipKeepingTheStore pins F20: a
// fetched tree whose SKILL.md is a symlink loop, a directory or a link out of
// the tree is one named SKILL-FETCH-FAILED skip; the old store stays linked
// and the install runs on.
func TestSourceFetchedSkillsTreeProblemsAreOneSkipKeepingTheStore(t *testing.T) {
	t.Parallel()
	cases := map[string]func(t *testing.T, repo, outside string){
		"symlink loop": func(t *testing.T, repo, _ string) {
			symlinkFixture(t, "SKILL.md", filepath.Join(repo, "SKILL.md"))
		},
		"directory": func(t *testing.T, repo, _ string) {
			writeFixture(t, filepath.Join(repo, "SKILL.md", "nested.md"), "# nested\n")
		},
		"link out of the tree": func(t *testing.T, repo, outside string) {
			writeFixture(t, filepath.Join(outside, "elsewhere.md"), "# elsewhere\n")
			symlinkFixture(t, filepath.Join(outside, "elsewhere.md"), filepath.Join(repo, "SKILL.md"))
		},
		"relative link out of the tree": func(t *testing.T, repo, _ string) {
			symlinkFixture(t, "../.gs.commit", filepath.Join(repo, "SKILL.md"))
		},
	}
	for name, plant := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# v1\n"})
			writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"gs": "file://" + repo}))
			runSkillInstall(t, home, ModeApply)
			if err := os.Remove(filepath.Join(repo, "SKILL.md")); err != nil {
				t.Fatal(err)
			}
			plant(t, repo, t.TempDir())
			skillFixtureCommit(t, repo, map[string]string{"README.md": "# v2\n"})

			output := runSkillInstall(t, home, ModeApply)

			store := filepath.Join(skillStoreRoot(home), "gs")
			if !strings.Contains(output, "SKILL-FETCH-FAILED gs: ") ||
				!strings.Contains(output, "(keeping "+store+")") {
				t.Fatalf("a %s SKILL.md was not one named skip keeping the store:\n%s", name, output)
			}
			if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# v1\n" {
				t.Fatalf("the old store copy was replaced: %q\n%s", got, output)
			}
			assertLink(t, filepath.Join(home, ".agents", "skills", "gs"), store)
		})
	}
}

// TestSourceFetchedSkillsCreateTheStoreRootOnlyUnderTheLock pins F22: while
// another install holds the skill store lock, a first install creates no
// store root.
func TestSourceFetchedSkillsCreateTheStoreRootOnlyUnderTheLock(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# gs\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
	holdSkillStoreLock(t, home)

	output := runSkillInstall(t, home, ModeApply)

	if !strings.Contains(output, "SKILL-SOURCES-BUSY ") {
		t.Fatalf("a held lock was not reported busy:\n%s", output)
	}
	requireNoPath(t, skillStoreRoot(home), "the store root was created outside the lock")
}

// TestRunSkillGitStripsOnlyRepositorySelection pins F23: the prompt overrides
// win over the user's own, transport settings pass through, and a variable
// selecting a repository is stripped.
func TestRunSkillGitStripsOnlyRepositorySelection(t *testing.T) {
	t.Setenv("SSH_ASKPASS", "/user/askpass")
	t.Setenv("GIT_SSL_CAINFO", "/user/ca.pem")
	t.Setenv("GIT_DIR", "/elsewhere/.git")
	script := filepath.Join(t.TempDir(), "git")
	if err := testjail.WriteExecutable(
		script,
		[]byte(
			"#!/bin/sh\nprintf 'askpass=[%s] ca=[%s] dir=[%s]' \"$SSH_ASKPASS\" \"$GIT_SSL_CAINFO\" \"${GIT_DIR-unset}\"\n",
		),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	got, err := runSkillGitWith(deps.RealRunner{}, 10*time.Second, skillGitWaitDelay, script, t.TempDir(), "ls-remote")
	if err != nil {
		t.Fatal(err)
	}
	if want := "askpass=[] ca=[/user/ca.pem] dir=[unset]"; got != want {
		t.Fatalf("git environment = %q, want %q", got, want)
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
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# gs\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))

	output := runSkillInstall(t, home, ModeApply)

	if strings.Contains(output, "SKILL-FETCH-FAILED") {
		t.Fatalf("a home repository's config steered the fetch:\n%s", output)
	}
	if got := readSkillFile(t, filepath.Join(skillStoreRoot(home), "god-speed", "SKILL.md")); got != "# gs\n" {
		t.Fatalf("store SKILL.md = %q\n%s", got, output)
	}
}

// TestSourceFetchedSkillsRecloneAStoreWithoutSKILLMd pins F25: a store whose
// SKILL.md is gone is re-cloned although its recorded commit is current. Other
// damage (a deleted .git, a changed file) is not detected: pfm never runs git
// inside a store.
func TestSourceFetchedSkillsRecloneAStoreWithoutSKILLMd(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# gs\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
	runSkillInstall(t, home, ModeApply)
	store := filepath.Join(skillStoreRoot(home), "god-speed")
	if err := os.Remove(filepath.Join(store, "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	output := runSkillInstall(t, home, ModeApply)

	if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# gs\n" {
		t.Fatalf("a damaged store at the current commit was not re-cloned: %q\n%s", got, output)
	}
	assertLink(t, filepath.Join(home, ".agents", "skills", "god-speed"), store)
}

func TestSourceFetchedSkillsDryRunKeepsExistingStore(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# gs\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
	runSkillInstall(t, home, ModeApply)
	was := repoHead(t, repo)
	skillFixtureCommit(t, repo, map[string]string{"SKILL.md": "# gs v2\n"})

	output := runSkillInstall(t, home, ModeDryRun)
	store := filepath.Join(skillStoreRoot(home), "god-speed")
	want := "check   " + store + " against file://" + repo +
		" (a dry run reads no remote; the apply replaces it if the head moved)\n"
	if !strings.Contains(output, want) {
		t.Fatalf("preview missing %q:\n%s", want, output)
	}
	if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# gs\n" {
		t.Fatalf("preview changed the store to %q", got)
	}
	if got := readSkillFile(t, skillCommitPath(store)); got != was+"\n" {
		t.Fatalf("preview changed the commit record to %q", got)
	}
}

// TestSourceFetchedSkillsAdoptAHandMadeClone pins the host state a manual
// setup leaves: a plain shallow clone at the store path with no commit record,
// already linked from every registry. The first install re-clones and swaps
// it with no conflict; the next one finds it up to date.
func TestSourceFetchedSkillsAdoptAHandMadeClone(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# gs\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
	storeRoot := skillStoreRoot(home)
	store := filepath.Join(storeRoot, "god-speed")
	if err := os.MkdirAll(storeRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	skillFixtureGit(t, storeRoot, "clone", "--depth", "1", "--quiet", "file://"+repo, "god-speed")
	links := []string{
		filepath.Join(home, ".claude", "skills", "god-speed"),
		filepath.Join(home, ".agents", "skills", "god-speed"),
	}
	for _, link := range links {
		symlinkFixture(t, store, link)
	}

	output := runSkillInstall(t, home, ModeApply)

	if !strings.Contains(output, "update "+store+" unrecorded -> ") {
		t.Fatalf("the hand-made clone was not re-cloned and swapped:\n%s", output)
	}
	for _, bad := range []string{
		"SKILL-FETCH-FAILED god-speed", "SKILL-SOURCE-MISSING god-speed", "CONFLICT " + links[0],
		"CONFLICT " + links[1],
	} {
		if strings.Contains(output, bad) {
			t.Fatalf("adopting the hand-made clone reported %q:\n%s", bad, output)
		}
	}
	for _, link := range links {
		assertLink(t, link, store)
	}
	if got := readSkillFile(t, skillCommitMarker(home, "god-speed")); got != repoHead(t, repo)+"\n" {
		t.Fatalf("commit record = %q after adoption", got)
	}
	if second := runSkillInstall(t, home, ModeApply); !strings.Contains(second, "ok      "+store+" at ") {
		t.Fatalf("the adopted store was not up to date on the next install:\n%s", second)
	}
}

// TestSourceFetchedSkillsAcceptALinkInsideTheTree pins F34: a root SKILL.md
// linking to a file inside its own tree is fetched, linked and up to date on
// the next install, and doctor reads it as linked.
func TestSourceFetchedSkillsAcceptALinkInsideTheTree(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := filepath.Join(t.TempDir(), "gs")
	symlinkFixture(t, filepath.Join("docs", "skill.md"), filepath.Join(repo, "SKILL.md"))
	skillFixtureRepo(t, repo, map[string]string{filepath.Join("docs", "skill.md"): "# linked\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"gs": "file://" + repo}))

	first := runSkillInstall(t, home, ModeApply)

	store := filepath.Join(skillStoreRoot(home), "gs")
	if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# linked\n" {
		t.Fatalf("an in-tree SKILL.md link was not fetched: %q\n%s", got, first)
	}
	assertLink(t, filepath.Join(home, ".agents", "skills", "gs"), store)
	if second := runSkillInstall(t, home, ModeApply); !strings.Contains(second, "ok      "+store+" at ") {
		t.Fatalf("an in-tree SKILL.md link was not up to date on the next install:\n%s", second)
	}

	if statuses := InspectSkillSources(home, false); len(statuses) != 1 ||
		statuses[0].State != SkillSourceLinked {
		t.Fatalf("doctor did not read an in-tree SKILL.md link as linked: %+v", statuses)
	}
}

// blockSkillCommitRecord puts a directory at name's commit record, so the
// record write that follows a swap fails.
func blockSkillCommitRecord(t *testing.T, home, name string) {
	t.Helper()
	marker := skillCommitMarker(home, name)
	if err := os.RemoveAll(marker); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(marker, "blocker"), "not a commit record\n")
}

// A failed commit record write leaves the new clone at the store and reports it.
func TestSourceFetchedSkillsFailedRecordWriteNamesTheTreeInPlace(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# v1\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
	runSkillInstall(t, home, ModeApply)
	skillFixtureCommit(t, repo, map[string]string{"SKILL.md": "# v2\n"})
	blockSkillCommitRecord(t, home, "god-speed")

	output := runSkillInstall(t, home, ModeApply)

	store := filepath.Join(skillStoreRoot(home), "god-speed")
	if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# v2\n" {
		t.Fatalf("store SKILL.md = %q, want the new clone\n%s", got, output)
	}
	if strings.Contains(output, "(keeping "+store+")") ||
		!strings.Contains(output, store+" holds the new clone at "+shortCommit(repoHead(t, repo))) {
		t.Fatalf("the skip does not name the new clone at the store:\n%s", output)
	}
}

func TestSourceFetchedSkillsFailedSwapOfAFreshStoreKeepsCloneAndRefetches(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# v1\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
	blockSkillCommitRecord(t, home, "god-speed")

	output := runSkillInstall(t, home, ModeApply)
	store := filepath.Join(skillStoreRoot(home), "god-speed")
	if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# v1\n" {
		t.Fatalf("failed record write left tree %q\n%s", got, output)
	}
	if got := readSkillFile(t, filepath.Join(skillCommitPath(store), "blocker")); got != "not a commit record\n" {
		t.Fatalf("blocked record changed to %q", got)
	}
	if !strings.Contains(output, "SKILL-FETCH-FAILED god-speed: ") ||
		!strings.Contains(output, " ("+store+" holds the new clone at "+shortCommit(repoHead(t, repo))+")\n") {
		t.Fatalf("failure did not name the store's new clone:\n%s", output)
	}
	writeFixture(t, filepath.Join(store, "SKILL.md"), "# incomplete\n")
	if err := os.RemoveAll(skillCommitPath(store)); err != nil {
		t.Fatal(err)
	}
	next := runSkillInstall(t, home, ModeApply)
	wantUpdate := "  change  update " + store + " unrecorded -> " + shortCommit(
		repoHead(t, repo),
	) + " from file://" + repo + "\n"
	if !strings.Contains(next, wantUpdate) {
		t.Fatalf("next install missing %q:\n%s", wantUpdate, next)
	}
	if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# v1\n" {
		t.Fatalf("next install failed to refetch: %q\n%s", got, next)
	}
	if got := readSkillFile(t, skillCommitPath(store)); got != repoHead(t, repo)+"\n" {
		t.Fatalf("refetched record %q does not name the new head", got)
	}
	assertLink(t, filepath.Join(home, ".agents", "skills", "god-speed"), store)
}

// TestCheckSkillFileNamesAnUninspectablePath pins F20's third case at its one
// door: a SKILL.md path lstat cannot read (ENAMETOOLONG here, which holds for
// root where a chmod would not) is an error other than fs.ErrNotExist, which
// fetchSkillSource turns into one named SKILL-FETCH-FAILED skip keeping the
// old store, and linkableStore into a SKILL-SOURCE-MISSING skip.
func TestCheckSkillFileNamesAnUninspectablePath(t *testing.T) {
	t.Parallel()
	err := checkSkillFile(filepath.Join(t.TempDir(), strings.Repeat("a", 300)))
	if !errors.Is(err, syscall.ENAMETOOLONG) || errors.Is(err, fs.ErrNotExist) || errors.Is(err, errSkillFileUnusable) {
		t.Fatalf("an uninspectable SKILL.md path: %v", err)
	}
}

func TestSourceFetchedSkillsSwapFailure(t *testing.T) {
	for _, restoreFails := range []bool{true, false} {
		name := "restored"
		if restoreFails {
			name = "old-copy-kept"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# v1\n"})
			writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
			runSkillInstall(t, home, ModeApply)
			skillFixtureCommit(t, repo, map[string]string{"SKILL.md": "# v2\n"})
			store := filepath.Join(skillStoreRoot(home), "god-speed")
			previous := skillStoreRename
			t.Cleanup(func() { skillStoreRename = previous })
			var staging string
			var moveErr error
			skillStoreRename = func(from, to string) error {
				base := filepath.Base(from)
				if strings.HasPrefix(base, ".god-speed.fetch-") && !strings.HasSuffix(base, ".old") {
					staging = from
					moveErr = &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.EIO}
					return moveErr
				}
				if restoreFails && strings.HasSuffix(from, ".old") {
					return &os.LinkError{Op: "rename", Old: from, New: to, Err: syscall.EIO}
				}
				return os.Rename(from, to)
			}
			var output bytes.Buffer
			_, err := Run(context.Background(), Options{
				Mode: ModeApply, Home: home, MCPConfigPath: testConfigPath(t), Stdout: &output, Runner: &fakeRunner{},
			})
			if restoreFails {
				if err == nil || !strings.Contains(err.Error(), "replace skill store "+store+": ") ||
					!strings.Contains(err.Error(), "(the old copy is kept at "+staging+".old)") {
					t.Fatalf("failed swap did not retain and name the old copy: %v\n%s", err, output.String())
				}
				if got := readSkillFile(t, filepath.Join(staging+".old", "SKILL.md")); got != "# v1\n" {
					t.Fatalf("old copy = %q, want v1", got)
				}
				if strings.Contains(output.String(), "(keeping "+store+")") {
					t.Fatalf("failed restore reported a store in place:\n%s", output.String())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf(
				"  skip    SKILL-FETCH-FAILED god-speed: move %s into place (old store restored): %v (keeping %s)\n",
				staging,
				moveErr,
				store,
			)
			if !strings.Contains(output.String(), want) {
				t.Fatalf("restored skip missing %q:\n%s", want, output.String())
			}
			if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# v1\n" {
				t.Fatalf("restored store = %q, want v1", got)
			}
			entries, err := filepath.Glob(filepath.Join(skillStoreRoot(home), ".god-speed.fetch-*"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("swap leftovers = %v, err=%v", entries, err)
			}
		})
	}
}

func TestSourceFetchedSkillsDryRunReclonesAnUnusableStore(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# v1\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
	runSkillInstall(t, home, ModeApply)
	store := filepath.Join(skillStoreRoot(home), "god-speed")
	skill := filepath.Join(store, "SKILL.md")
	if err := os.Remove(skill); err != nil {
		t.Fatal(err)
	}
	output := runSkillInstall(t, home, ModeDryRun)
	want := "  change  re-clone " + store + " from file://" + repo +
		" (inspect " + skill + ": lstat " + skill + ": no such file or directory)\n"
	if !strings.Contains(output, want) ||
		!strings.Contains(output, "  ok      "+filepath.Join(home, ".agents", "skills", "god-speed")+"\n") ||
		strings.Contains(output, "SKILL-SOURCE-MISSING god-speed") {
		t.Fatalf("dry run did not preview the usable replacement %q:\n%s", want, output)
	}
}

func TestSourceFetchedSkillsInterruptedSwap(t *testing.T) {
	for _, restoreFails := range []bool{false, true} {
		name := "restored"
		if restoreFails {
			name = "restore-failed"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# v1\n"})
			writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
			if err := os.RemoveAll(repo); err != nil {
				t.Fatal(err)
			}
			root := skillStoreRoot(home)
			store := filepath.Join(root, "god-speed")
			staging := filepath.Join(root, ".god-speed.fetch-abc")
			trash := staging + ".old"
			writeFixture(t, filepath.Join(trash, "SKILL.md"), "# v1\n")
			if err := os.Mkdir(staging, 0o755); err != nil {
				t.Fatal(err)
			}
			var restoreErr error
			if restoreFails {
				previous := skillStoreRename
				t.Cleanup(func() { skillStoreRename = previous })
				restoreErr = &os.LinkError{Op: "rename", Old: trash, New: store, Err: syscall.EIO}
				skillStoreRename = func(from, to string) error {
					if from == trash {
						return restoreErr
					}
					return os.Rename(from, to)
				}
			}
			var output bytes.Buffer
			_, err := Run(context.Background(), Options{
				Mode: ModeApply, Home: home, MCPConfigPath: testConfigPath(t), Stdout: &output, Runner: &fakeRunner{},
			})
			if restoreFails {
				want := fmt.Sprintf("restore %s from %s: %v", store, trash, restoreErr)
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("restore error missing %q: %v\n%s", want, err, output.String())
				}
				if got := readSkillFile(t, filepath.Join(trash, "SKILL.md")); got != "# v1\n" {
					t.Fatalf("failed recovery lost v1: %q", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := "  change  restore " + store + " from " + trash + " (an interrupted swap)\n"
			if !strings.Contains(output.String(), want) ||
				!strings.Contains(output.String(), "  skip    SKILL-FETCH-FAILED god-speed: ") ||
				!strings.Contains(output.String(), " (keeping "+store+")\n") {
				t.Fatalf("recovery did not name the restored store %q:\n%s", want, output.String())
			}
			if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# v1\n" {
				t.Fatalf("recovered store = %q, want v1", got)
			}
			requireNoPath(t, staging, "recovery left staging")
			assertLink(t, filepath.Join(home, ".agents", "skills", "god-speed"), store)
		})
	}
}

func TestRunSkillGitSSHEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, command, program, want string
	}{
		{name: "no-prompt", want: "ssh -o BatchMode=yes"},
		{name: "inherited-command", command: "ssh -i /user/key", want: "ssh -i /user/key"},
		{name: "inherited-program", program: "/user/ssh-wrap", want: "unset"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GIT_SSH_COMMAND", tc.command)
			t.Setenv("GIT_SSH", tc.program)
			for key, value := range map[string]string{"GIT_SSH_COMMAND": tc.command, "GIT_SSH": tc.program} {
				if value == "" {
					if err := os.Unsetenv(key); err != nil {
						t.Fatal(err)
					}
				}
			}
			script := filepath.Join(t.TempDir(), "git")
			if err := testjail.WriteExecutable(
				script,
				[]byte("#!/bin/sh\nprintf '%s' \"${GIT_SSH_COMMAND-unset}\"\n"),
				0o755,
			); err != nil {
				t.Fatal(err)
			}
			got, err := runSkillGitWith(
				deps.RealRunner{},
				10*time.Second,
				skillGitWaitDelay,
				script,
				t.TempDir(),
				"ls-remote",
			)
			if err != nil || got != tc.want {
				t.Fatalf("git ssh command = %q, err=%v; want %q", got, err, tc.want)
			}
		})
	}
}

type skillDeadlineRunner struct {
	deps.Runner
	deadlines []time.Time
}

func TestSourceFetchedSkillsInterruptedSwapPreviewAndSelection(t *testing.T) {
	t.Parallel()
	for _, mode := range []Mode{ModeDryRun, ModeApply} {
		name := "apply"
		if mode == ModeDryRun {
			name = "dry-run"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			repo := filepath.Join(t.TempDir(), "deleted-repo")
			writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
			root := skillStoreRoot(home)
			store := filepath.Join(root, "god-speed")
			older := filepath.Join(root, ".god-speed.fetch-aaa.old")
			trash := filepath.Join(root, ".god-speed.fetch-abc.old")
			writeFixture(t, filepath.Join(older, "SKILL.md"), "# earlier\n")
			writeFixture(t, filepath.Join(trash, "SKILL.md"), "# v1\n")
			output := runSkillInstall(t, home, mode)
			want := "  change  restore " + store + " from " + trash + " (an interrupted swap)\n"
			if !strings.Contains(output, want) {
				t.Fatalf("recovery did not choose the lexically last copy %q:\n%s", want, output)
			}
			if mode == ModeDryRun {
				if strings.Contains(output, "retire "+trash+" ") {
					t.Fatalf("dry run also retired its recovery source:\n%s", output)
				}
				if got := readSkillFile(t, filepath.Join(trash, "SKILL.md")); got != "# v1\n" {
					t.Fatalf("dry-run recovery changed the old copy to %q", got)
				}
				requireNoPath(t, store, "dry-run recovery changed the store")
				return
			}
			if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# v1\n" {
				t.Fatalf("recovered store = %q, want v1", got)
			}
			requireNoPath(t, older, "recovery left an older interrupted copy")
		})
	}
}

func (runner *skillDeadlineRunner) Run(ctx context.Context, _ []string, _ deps.RunOptions) (deps.RunResult, error) {
	deadline, _ := ctx.Deadline()
	runner.deadlines = append(runner.deadlines, deadline)
	return deps.RunResult{Stdout: []byte("fixture\n")}, nil
}

func TestRunSkillGitBudget(t *testing.T) {
	t.Parallel()
	t.Run("spent", func(t *testing.T) {
		runner := &deps.FakeRunner{}
		installer := &engine{options: Options{Home: t.TempDir(), ProcessRunner: runner}}
		_, err := installer.runSkillGit(time.Now().Add(-time.Second), "ls-remote", "--", "file:///nowhere", "HEAD")
		want := "skill fetch budget of 2m0s spent before git ls-remote -- file:///nowhere HEAD"
		if err == nil || err.Error() != want {
			t.Fatalf("spent budget = %v, want %q", err, want)
		}
		if calls := runner.Calls(); len(calls) != 0 {
			t.Fatalf("spent budget reached the runner: %+v", calls)
		}
	})
	for _, budget := range []time.Duration{5 * time.Second, 10 * time.Minute} {
		t.Run(budget.String(), func(t *testing.T) {
			runner := &skillDeadlineRunner{}
			installer := &engine{options: Options{Home: t.TempDir(), ProcessRunner: runner}}
			start := time.Now()
			deadline := start.Add(budget)
			if _, err := installer.runSkillGit(deadline, "ls-remote"); err != nil {
				t.Fatal(err)
			}
			if len(runner.deadlines) != 1 {
				t.Fatalf("recorded deadlines = %v", runner.deadlines)
			}
			got := runner.deadlines[0]
			if budget < skillGitTimeout && (got.After(deadline) || got.Before(deadline.Add(-time.Second))) {
				t.Fatalf("call deadline = %v, want at most %v", got, deadline)
			}
			if budget > skillGitTimeout &&
				(got.Before(start.Add(59*time.Second)) || got.After(start.Add(61*time.Second))) {
				t.Fatalf("call deadline = %v, want 60s +/- 1s after %v", got, start)
			}
		})
	}
}
