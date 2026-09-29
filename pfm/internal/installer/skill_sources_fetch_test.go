package installer

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
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

// TestSourceFetchedSkillsFailedSwapRestoresAndClosesItsJournal pins F21: a
// swap whose commit record cannot be written puts the old store back, leaves
// no journal record pending and says the old store is kept.
func TestSourceFetchedSkillsFailedSwapRestoresAndClosesItsJournal(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# v1\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
	runSkillInstall(t, home, ModeApply)
	skillFixtureCommit(t, repo, map[string]string{"SKILL.md": "# v2\n"})
	marker := skillCommitMarker(home, "god-speed")
	if err := os.RemoveAll(marker); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(marker, "blocker"), "not a commit record\n")
	journal := NewJournal(context.Background(), LayoutEnv{Home: home})

	output := runSkillInstall(t, home, ModeApply, func(options *Options) { options.Journal = journal })

	store := filepath.Join(skillStoreRoot(home), "god-speed")
	if !strings.Contains(output, "SKILL-FETCH-FAILED god-speed: ") || !strings.Contains(output, "(keeping "+store+")") {
		t.Fatalf("a failed swap was not one named skip keeping the store:\n%s", output)
	}
	if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# v1\n" {
		t.Fatalf("the output says the store is kept, the disk holds %q\n%s", got, output)
	}
	restored := false
	for _, record := range journal.records {
		if record.Result == layoutRecordPending || record.Result == layoutRecordUnrestored {
			t.Fatalf("a handled swap failure left a journal record open: %+v\n%s", record, output)
		}
		restored = restored || (record.Destination == store && record.Result == layoutRecordRestored)
	}
	if !restored {
		t.Fatalf("no restored journal record for %s: %+v\n%s", store, journal.records, output)
	}
}

// TestSourceFetchedSkillsCreateTheStoreRootOnlyUnderTheLock pins F22: while
// another install holds the skill store lock, a first install creates and
// journals no store root.
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
	writeFixture(t, script,
		"#!/bin/sh\nprintf 'askpass=[%s] ca=[%s] dir=[%s]' \"$SSH_ASKPASS\" \"$GIT_SSL_CAINFO\" \"${GIT_DIR-unset}\"\n")
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := runSkillGitWith(deps.RealRunner{}, 10*time.Second, script, t.TempDir(), "ls-remote")
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

// TestSourceFetchedSkillsRecloneADamagedStore pins F25: a store whose SKILL.md
// is gone is re-cloned although its recorded commit is current.
func TestSourceFetchedSkillsRecloneADamagedStore(t *testing.T) {
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

// TestSourceFetchedSkillsDryRunOfAnUpToDateStorePlansNoChange pins F27: a dry
// run over an existing store reads no remote and plans no change for it.
func TestSourceFetchedSkillsDryRunOfAnUpToDateStorePlansNoChange(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# gs\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
	runSkillInstall(t, home, ModeApply)

	output := runSkillInstall(t, home, ModeDryRun)

	if strings.Contains(output, "change  fetch ") || strings.Contains(output, "change  update ") {
		t.Fatalf("a dry run planned a change for an existing store:\n%s", output)
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

// TestCheckSkillFileNamesAnUninspectablePath pins F20's third case at its one
// door: a SKILL.md path lstat cannot read (ENAMETOOLONG here, which holds for
// root where a chmod would not) is an error other than fs.ErrNotExist, which
// fetchSkillSource turns into one named SKILL-FETCH-FAILED skip keeping the
// old store, and linkableStore into a SKILL-SOURCE-MISSING skip.
func TestCheckSkillFileNamesAnUninspectablePath(t *testing.T) {
	t.Parallel()
	err := checkSkillFile(filepath.Join(t.TempDir(), strings.Repeat("a", 300)))
	if !errors.Is(err, syscall.ENAMETOOLONG) || errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("an uninspectable SKILL.md path: %v", err)
	}
}
