package installer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goRuntime "runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// skillFixtureGit runs git in dir with a fixed identity; fixtures are local
// repositories only — no test here reaches a network.
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

// skillFixtureRepo creates a git repository at dir holding files, committed.
func skillFixtureRepo(t *testing.T, dir string, files map[string]string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillFixtureGit(t, dir, "init", "--quiet")
	skillFixtureCommit(t, dir, files)
	return dir
}

func skillFixtureCommit(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		writeFixture(t, filepath.Join(dir, name), content)
	}
	skillFixtureGit(t, dir, "add", "-A")
	skillFixtureGit(t, dir, "commit", "--quiet", "-m", "fixture")
}

func writeSkillRegistry(t *testing.T, home, content string) string {
	t.Helper()
	path := filepath.Join(home, ".professor", "templates", "global", "skills", "sources.json")
	writeFixture(t, path, content)
	return path
}

func skillRegistryJSON(repos map[string]string) string {
	entries := make([]string, 0, len(repos))
	for name, repo := range repos {
		entries = append(entries, `"`+name+`": {"repo": "`+repo+`", "parameterize": []}`)
	}
	return `{"_comment": "fixture", "source_fetched": {` + strings.Join(entries, ", ") + "}}\n"
}

func runSkillInstall(t *testing.T, home string, mode Mode, mutate ...func(*Options)) string {
	t.Helper()
	var output bytes.Buffer
	options := Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          mode, Home: home, Stdout: &output, Runner: &fakeRunner{},
	}
	for _, apply := range mutate {
		apply(&options)
	}
	if _, err := Run(context.Background(), options); err != nil {
		t.Fatalf("install mode=%d: %v\n%s", mode, err, output.String())
	}
	return output.String()
}

func readSkillFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(content)
}

func requireNoPath(t *testing.T, path, why string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s: %s exists (err=%v)", why, path, err)
	}
}

// TestSourceFetchedSkillsCloneAndLinkIntoEveryRegistry pins the first install:
// the registry's {GH_USER} resolves through the clone's manifest owner, the
// repo is cloned into the store, and the store is linked into every Claude
// account's skills/ and into ~/.agents/skills/.
func TestSourceFetchedSkillsCloneAndLinkIntoEveryRegistry(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	base := t.TempDir()
	skillFixtureRepo(
		t,
		filepath.Join(base, "fixture-owner", "god-speed"),
		map[string]string{"SKILL.md": "# god-speed v1\n"},
	)
	writeFixture(t, filepath.Join(home, ".professor", ".professor", "manifest.json"),
		`{"installed_from": {"repo": "https://github.com/fixture-owner/professor"}}`)
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{
		"god-speed": "file://" + base + "/{GH_USER}/god-speed",
	}))
	second := filepath.Join(home, ".cc", "2")

	output := runSkillInstall(t, home, ModeApply, func(options *Options) {
		options.ClaudeAccounts = []pfmconfig.Account{{ID: 2, ConfigDir: second}}
	})

	store := filepath.Join(skillStoreRoot(home), "god-speed")
	if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# god-speed v1\n" {
		t.Fatalf("store SKILL.md = %q\n%s", got, output)
	}
	assertLink(t, filepath.Join(home, ".claude", "skills", "god-speed"), store)
	assertLink(t, filepath.Join(home, ".agents", "skills", "god-speed"), store)
}

// TestSourceFetchedSkillsReinstallFetchesTheNewCommit pins "always latest": a
// second install hard-resets the store to the repo's new default-branch head.
func TestSourceFetchedSkillsReinstallFetchesTheNewCommit(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "ghost-writer"), map[string]string{"SKILL.md": "# v1\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"quill": "file://" + repo}))
	runSkillInstall(t, home, ModeApply)

	skillFixtureCommit(t, repo, map[string]string{"SKILL.md": "# v2\n"})
	output := runSkillInstall(t, home, ModeApply)

	store := filepath.Join(skillStoreRoot(home), "quill")
	if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# v2\n" {
		t.Fatalf("re-install left the store at %q, want the new commit\n%s", got, output)
	}
	if !strings.Contains(output, "update "+store) {
		t.Fatalf("re-install did not report the store update:\n%s", output)
	}
	third := runSkillInstall(t, home, ModeApply)
	if !strings.Contains(third, "ok      "+store+" at ") {
		t.Fatalf("an up-to-date store was not reported ok:\n%s", third)
	}
}

// TestSourceFetchedSkillsDryRunWritesNothing pins that a dry run clones,
// fetches and links nothing, while printing the planned fetch and links.
func TestSourceFetchedSkillsDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "atlas"), map[string]string{"SKILL.md": "# vf\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"atlas": "file://" + repo}))

	output := runSkillInstall(t, home, ModeDryRun)

	store := filepath.Join(skillStoreRoot(home), "atlas")
	requireNoPath(t, skillStoreRoot(home), "dry run created the store root")
	requireNoPath(t, store, "dry run cloned")
	requireNoPath(t, filepath.Join(home, ".agents", "skills", "atlas"), "dry run linked")
	requireNoPath(t, filepath.Join(home, ".claude", "skills", "atlas"), "dry run linked")
	for _, want := range []string{
		"  change  create " + skillStoreRoot(home) + "\n",
		"change  fetch file://" + repo + " -> " + store,
		"change  link " + filepath.Join(home, ".agents", "skills", "atlas") + " -> " + store,
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("dry run omitted %q:\n%s", want, output)
		}
	}
}

// TestSourceFetchedSkillsFetchFailureKeepsTheStoreCopy pins that an
// unreachable repo is one named skip: the install succeeds, an existing store
// copy stays linked, and with no copy nothing is linked.
func TestSourceFetchedSkillsFetchFailureKeepsTheStoreCopy(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "ghost-writer"), map[string]string{"SKILL.md": "# kept\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"quill": "file://" + repo}))
	runSkillInstall(t, home, ModeApply)

	gone := filepath.Join(t.TempDir(), "no-such-repo")
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{
		"quill": "file://" + gone,
		"fresh": "file://" + gone,
	}))
	output := runSkillInstall(t, home, ModeApply)

	store := filepath.Join(skillStoreRoot(home), "quill")
	for _, want := range []string{"SKILL-FETCH-FAILED quill: ", "SKILL-FETCH-FAILED fresh: "} {
		if !strings.Contains(output, want) {
			t.Fatalf("fetch failure not reported as %q:\n%s", want, output)
		}
	}
	if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# kept\n" {
		t.Fatalf("fetch failure lost the store copy: %q", got)
	}
	if strings.Contains(output, "<nil>") {
		t.Fatalf("a git failure rendered a nil error:\n%s", output)
	}
	assertLink(t, filepath.Join(home, ".agents", "skills", "quill"), store)
	requireNoPath(t, filepath.Join(skillStoreRoot(home), "fresh"), "a failed first fetch left a store")
	requireNoPath(t, filepath.Join(home, ".agents", "skills", "fresh"), "a failed first fetch linked")
	entries, err := os.ReadDir(skillStoreRoot(home))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != "quill" && entry.Name() != ".quill.commit" {
			t.Fatalf("a failed fetch left %s in the store root (a staging directory or a record temp)", entry.Name())
		}
	}
}

// TestSourceFetchedSkillsRefuseATreeWithoutSKILLMd pins the existing
// SKILL-SOURCE-MISSING wording for a fetched tree with no root SKILL.md.
func TestSourceFetchedSkillsRefuseATreeWithoutSKILLMd(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "hollow"), map[string]string{"README.md": "# not a skill\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"hollow": "file://" + repo}))

	output := runSkillInstall(t, home, ModeApply)

	if !strings.Contains(output, "SKILL-SOURCE-MISSING hollow (") {
		t.Fatalf("a tree without SKILL.md was not reported:\n%s", output)
	}
	requireNoPath(t, filepath.Join(home, ".agents", "skills", "hollow"), "a tree without SKILL.md was linked")
}

// TestSourceFetchedSkillsValidateTheRegistryAtEntry pins the named skips and
// the named failure: an unresolved non-{GH_USER} placeholder, a template-skill
// name clash, an invalid name, an unresolvable {GH_USER}, and an unparsable
// registry — none fetched, none rendered as "no skills".
func TestSourceFetchedSkillsValidateTheRegistryAtEntry(t *testing.T) {
	t.Parallel()
	t.Run("entries", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		writeFixture(
			t,
			filepath.Join(home, ".professor", "templates", "global", "skills", "shared", "SKILL.md"),
			"# t\n",
		)
		writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{
			"unresolved": "https://example.invalid/{OTHER_TOKEN}/x",
			"shared":     "https://example.invalid/shared",
			"Bad_Name":   "https://example.invalid/bad",
		}))
		output := runSkillInstall(t, home, ModeApply)
		for _, want := range []string{
			"SKILL-SOURCE-UNRESOLVED unresolved: repo https://example.invalid/{OTHER_TOKEN}/x carries unresolved placeholder {OTHER_TOKEN}",
			"SKILL-SOURCE-CLASH shared: ",
			"SKILL-SOURCE-INVALID Bad_Name: ",
		} {
			if !strings.Contains(output, want) {
				t.Fatalf("missing %q:\n%s", want, output)
			}
		}
		requireNoPath(t, skillStoreRoot(home), "an invalid entry was fetched")
		assertLink(t, filepath.Join(home, ".claude", "skills", "shared"),
			filepath.Join(home, ".professor", "templates", "global", "skills", "shared"))
	})
	t.Run("owner", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"x": "https://github.com/{GH_USER}/x"}))
		output := runSkillInstall(t, home, ModeApply)
		if !strings.Contains(output, "SKILL-SOURCES-FAILED ") ||
			!strings.Contains(output, "resolve registered placeholder {GH_USER}") {
			t.Fatalf("an unresolvable {GH_USER} was not a named failure:\n%s", output)
		}
	})
	t.Run("unparsable", func(t *testing.T) {
		t.Parallel()
		home := t.TempDir()
		store := filepath.Join(skillStoreRoot(home), "kept")
		writeFixture(t, filepath.Join(store, "SKILL.md"), "# kept\n")
		path := writeSkillRegistry(t, home, "{not json")
		output := runSkillInstall(t, home, ModeApply)
		if !strings.Contains(output, "SKILL-SOURCES-FAILED decode "+path) {
			t.Fatalf("an unparsable registry was not a failure naming %s:\n%s", path, output)
		}
		if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# kept\n" {
			t.Fatalf("an unparsable registry retired a store: %q", got)
		}
	})
}

// TestSourceFetchedSkillsNeverOverwriteARealDirectory pins that an operator's
// hand-copied skill directory at a link target is a reported conflict, left
// byte-for-byte untouched, while the other registries still get the link.
func TestSourceFetchedSkillsNeverOverwriteARealDirectory(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(
		t,
		filepath.Join(t.TempDir(), "ghost-writer"),
		map[string]string{"SKILL.md": "# upstream\n"},
	)
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"quill": "file://" + repo}))
	own := filepath.Join(home, ".claude", "skills", "quill")
	writeFixture(t, filepath.Join(own, "SKILL.md"), "# hand-copied\n")

	output := runSkillInstall(t, home, ModeApply)

	if !strings.Contains(output, "CONFLICT "+own+": a real directory, not ours; preserved") {
		t.Fatalf("the real directory was not reported as a conflict:\n%s", output)
	}
	if got := readSkillFile(t, filepath.Join(own, "SKILL.md")); got != "# hand-copied\n" {
		t.Fatalf("the operator's directory was overwritten: %q", got)
	}
	assertLink(
		t,
		filepath.Join(home, ".agents", "skills", "quill"),
		filepath.Join(skillStoreRoot(home), "quill"),
	)
}

// TestSourceFetchedSkillsRetireUnregisteredAndUninstall pins retirement (a
// skill dropped from the registry loses its links and its store; an operator's
// own link is untouched) and uninstall (every store link and the store go).
func TestSourceFetchedSkillsRetireUnregisteredAndUninstall(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	base := t.TempDir()
	keep := skillFixtureRepo(t, filepath.Join(base, "keep"), map[string]string{"SKILL.md": "# keep\n"})
	drop := skillFixtureRepo(t, filepath.Join(base, "drop"), map[string]string{"SKILL.md": "# drop\n"})
	writeSkillRegistry(
		t,
		home,
		skillRegistryJSON(map[string]string{"keep": "file://" + keep, "drop": "file://" + drop}),
	)
	runSkillInstall(t, home, ModeApply)
	operator := filepath.Join(home, ".agents", "skills", "mine")
	if err := os.Symlink(drop, operator); err != nil {
		t.Fatal(err)
	}

	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"keep": "file://" + keep}))
	output := runSkillInstall(t, home, ModeApply)
	ledger := skillLinkLedgerPath(home)
	if strings.Contains(output, ledger) {
		t.Errorf("an unchanged second apply named the ledger:\n%s", output)
	}
	if _, err := os.Stat(ledger); err != nil {
		t.Errorf("install did not record skill link dirs: %v", err)
	}

	storeRoot := skillStoreRoot(home)
	requireNoPath(t, filepath.Join(storeRoot, "drop"), "an unregistered store survived")
	requireNoPath(t, filepath.Join(home, ".agents", "skills", "drop"), "an unregistered link survived")
	requireNoPath(t, filepath.Join(home, ".claude", "skills", "drop"), "an unregistered link survived")
	assertLink(t, filepath.Join(home, ".claude", "skills", "keep"), filepath.Join(storeRoot, "keep"))
	assertLink(t, operator, drop)

	runSkillInstall(t, home, ModeUninstall)
	requireNoPath(t, filepath.Join(home, ".claude", "skills", "keep"), "uninstall left an account link")
	requireNoPath(t, filepath.Join(home, ".agents", "skills", "keep"), "uninstall left the .agents link")
	requireNoPath(t, storeRoot, "uninstall left the skill store")
	requireNoPath(t, ledger, "uninstall left the skill link record")
	assertLink(t, operator, drop)
}

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
		skillFixtureRepo(t, home, map[string]string{"tracked.txt": "parent\n"})
		writeFixture(t, filepath.Join(home, "untracked.txt"), "mine\n")
		parentHead := cleanGitOutput(t, home, "rev-parse", "HEAD")
		repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "skill"), map[string]string{"SKILL.md": "# new\n"})
		writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"skill": "file://" + repo}))
		store := filepath.Join(skillStoreRoot(home), "skill")
		writeFixture(t, filepath.Join(store, "SKILL.md"), "# hand-made, no .git\n")
		if inherited {
			t.Setenv("GIT_DIR", filepath.Join(home, ".git"))
		}

		output := runSkillInstall(t, home, ModeApply)

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

// TestSourceFetchedSkillsKeepTheOldStoreWhenTheNewTreeHasNoSKILLMd pins that
// an upstream commit dropping SKILL.md never replaces a working store copy.
func TestSourceFetchedSkillsKeepTheOldStoreWhenTheNewTreeHasNoSKILLMd(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "ghost-writer"), map[string]string{"SKILL.md": "# v1\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"quill": "file://" + repo}))
	runSkillInstall(t, home, ModeApply)
	skillFixtureGit(t, repo, "rm", "--quiet", "SKILL.md")
	skillFixtureCommit(t, repo, map[string]string{"README.md": "# moved\n"})

	output := runSkillInstall(t, home, ModeApply)

	store := filepath.Join(skillStoreRoot(home), "quill")
	if !strings.Contains(output, "SKILL-SOURCE-MISSING quill (") {
		t.Fatalf("a new tree without SKILL.md was not reported:\n%s", output)
	}
	if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# v1\n" {
		t.Fatalf("the old store copy was replaced: %q\n%s", got, output)
	}
	assertLink(t, filepath.Join(home, ".agents", "skills", "quill"), store)
}

// TestSourceFetchedSkillsStoreThatIsNotADirectoryIsOneSkip pins F3: a stray
// file at the store path is one named skip, preserved, and the install runs on.
func TestSourceFetchedSkillsStoreThatIsNotADirectoryIsOneSkip(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "vf"), map[string]string{"SKILL.md": "# vf\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"atlas": "file://" + repo}))
	store := filepath.Join(skillStoreRoot(home), "atlas")
	writeFixture(t, store, "stray\n")

	output := runSkillInstall(t, home, ModeApply)

	if !strings.Contains(output, "SKILL-FETCH-FAILED atlas: store "+store+" is not a directory") {
		t.Fatalf("a non-directory store was not one named skip:\n%s", output)
	}
	if got := readSkillFile(t, store); got != "stray\n" {
		t.Fatalf("the stray file was changed: %q", got)
	}
	requireNoPath(t, filepath.Join(home, ".agents", "skills", "atlas"), "a non-directory store was linked")
}

// TestSourceFetchedSkillsRefuseASymlinkedStoreRoot pins F6: a store root that
// is a link is never read through — the operator's directories at its target
// survive install and uninstall.
func TestSourceFetchedSkillsRefuseASymlinkedStoreRoot(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "keep"), map[string]string{"SKILL.md": "# keep\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"keep": "file://" + repo}))
	operator := t.TempDir()
	writeFixture(t, filepath.Join(operator, "mine", "keep.txt"), "operator\n")
	storeRoot := skillStoreRoot(home)
	if err := os.MkdirAll(filepath.Dir(storeRoot), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(operator, storeRoot); err != nil {
		t.Fatal(err)
	}

	output := runSkillInstall(t, home, ModeApply)
	if !strings.Contains(output, "SKILL-SOURCES-FAILED refuse skill store root "+storeRoot) {
		t.Fatalf("a symlinked store root was not refused by name:\n%s", output)
	}
	if got := readSkillFile(t, filepath.Join(operator, "mine", "keep.txt")); got != "operator\n" {
		t.Fatalf("install changed the operator's directory: %q", got)
	}
	uninstall := runSkillInstall(t, home, ModeUninstall)
	if !strings.Contains(uninstall, "SKILL-SOURCES-FAILED refuse skill store root "+storeRoot) ||
		!strings.Contains(uninstall, "pfm uninstall") {
		t.Fatalf("uninstall did not skip the symlinked store root by name with its own remedy:\n%s", uninstall)
	}
	if got := readSkillFile(t, filepath.Join(operator, "mine", "keep.txt")); got != "operator\n" {
		t.Fatalf("uninstall changed the operator's directory: %q", got)
	}
}

// TestSourceFetchedSkillsOfflineNamesWhatIsLinked pins F7: the offline skip
// says whether a store copy exists, never claiming one that is not there.
func TestSourceFetchedSkillsOfflineNamesWhatIsLinked(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# gs\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
	offline := func(options *Options) { options.SkillSourcesOffline = true }

	output := runSkillInstall(t, home, ModeApply, offline)
	if !strings.Contains(
		output,
		"SKILL-FETCH-SKIPPED god-speed: PFM_SKILL_SOURCES_OFFLINE=1 (no store copy; nothing linked)",
	) {
		t.Fatalf("offline with no store claimed a linked copy:\n%s", output)
	}
	requireNoPath(t, skillStoreRoot(home), "offline install fetched")

	runSkillInstall(t, home, ModeApply)
	output = runSkillInstall(t, home, ModeApply, offline)
	if !strings.Contains(
		output,
		"SKILL-FETCH-SKIPPED god-speed: PFM_SKILL_SOURCES_OFFLINE=1 (the store copy is still linked)",
	) {
		t.Fatalf("offline with a store did not say it stays linked:\n%s", output)
	}
	assertLink(
		t,
		filepath.Join(home, ".agents", "skills", "god-speed"),
		filepath.Join(skillStoreRoot(home), "god-speed"),
	)
}

// TestSourceFetchedSkillsNewClashLinksTheTemplateInOneInstall pins F11: a name
// that newly ships as a template skill is linked to the template by the first
// install after the clash, its old store link retired first.
func TestSourceFetchedSkillsNewClashLinksTheTemplateInOneInstall(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "shared"), map[string]string{"SKILL.md": "# fetched\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"shared": "file://" + repo}))
	runSkillInstall(t, home, ModeApply)
	template := filepath.Join(home, ".professor", "templates", "global", "skills", "shared")
	writeFixture(t, filepath.Join(template, "SKILL.md"), "# template\n")

	output := runSkillInstall(t, home, ModeApply)

	if !strings.Contains(output, "SKILL-SOURCE-CLASH shared: ") {
		t.Fatalf("the clash was not reported:\n%s", output)
	}
	assertLink(t, filepath.Join(home, ".claude", "skills", "shared"), template)
	requireNoPath(t, filepath.Join(home, ".agents", "skills", "shared"), "the clashing store link survived")
}

// TestSourceFetchedSkillsBusyStoreIsLeftAlone pins F13: while another install
// holds the store root, this one fetches, links and retires nothing there —
// the other install's staging directory survives.
func TestSourceFetchedSkillsBusyStoreIsLeftAlone(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# gs\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
	storeRoot := skillStoreRoot(home)
	staging := filepath.Join(storeRoot, ".god-speed.fetch-123")
	writeFixture(t, filepath.Join(staging, "SKILL.md"), "# in flight\n")
	holdSkillStoreLock(t, home)

	output := runSkillInstall(t, home, ModeApply)

	if !strings.Contains(output, "SKILL-SOURCES-BUSY ") {
		t.Fatalf("a held store root was not reported busy:\n%s", output)
	}
	if got := readSkillFile(t, filepath.Join(staging, "SKILL.md")); got != "# in flight\n" {
		t.Fatalf("the other install's staging directory was retired: %q", got)
	}
	requireNoPath(t, filepath.Join(storeRoot, "god-speed"), "a busy store root was fetched into")
	uninstall := runSkillInstall(t, home, ModeUninstall)
	if !strings.Contains(uninstall, "SKILL-SOURCES-BUSY ") || !strings.Contains(uninstall, "pfm uninstall") {
		t.Fatalf("uninstall did not skip a held store by name and run on:\n%s", uninstall)
	}
}

// holdSkillStoreLock takes the skill store lock — the flock on the store
// root's parent — for the rest of the test, as a concurrent install would.
func holdSkillStoreLock(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Dir(skillStoreRoot(home))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	holder, err := os.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := holder.Close(); err != nil {
			t.Errorf("close %s: %v", dir, err)
		}
	})
	if err := syscall.Flock(int(holder.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
}

// TestSourceFetchedSkillsRegistryWithoutTheObjectRetiresNothing pins F14: a
// registry of null or one missing source_fetched is a named failure, never an
// empty registry that retires every store.
func TestSourceFetchedSkillsRegistryWithoutTheObjectRetiresNothing(t *testing.T) {
	t.Parallel()
	for _, content := range []string{"null\n", `{"_comment": "no object"}` + "\n"} {
		home := t.TempDir()
		repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# gs\n"})
		writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
		runSkillInstall(t, home, ModeApply)
		path := writeSkillRegistry(t, home, content)

		output := runSkillInstall(t, home, ModeApply)

		if !strings.Contains(output, "SKILL-SOURCES-FAILED decode "+path+": no source_fetched object") {
			t.Fatalf("registry %q was not a named failure:\n%s", content, output)
		}
		store := filepath.Join(skillStoreRoot(home), "god-speed")
		if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# gs\n" {
			t.Fatalf("registry %q retired the store: %q", content, got)
		}
		assertLink(t, filepath.Join(home, ".agents", "skills", "god-speed"), store)
	}
}

// TestSourceFetchedSkillsDryRunOfAnExistingStoreChangesNothing pins the F18
// dry-run-update gap: a dry run over an existing store only plans the clone.
func TestSourceFetchedSkillsDryRunOfAnExistingStoreChangesNothing(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "gs"), map[string]string{"SKILL.md": "# v1\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"god-speed": "file://" + repo}))
	runSkillInstall(t, home, ModeApply)
	skillFixtureCommit(t, repo, map[string]string{"SKILL.md": "# v2\n"})

	output := runSkillInstall(t, home, ModeDryRun)

	store := filepath.Join(skillStoreRoot(home), "god-speed")
	if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# v1\n" {
		t.Fatalf("a dry run changed the store: %q\n%s", got, output)
	}
	if !strings.Contains(output, "file://"+repo) {
		t.Fatalf("a dry run did not plan the clone:\n%s", output)
	}
}

// TestRunSkillGitReturnsWithinItsBound pins F4: a git call whose grandchild
// keeps stderr open still returns shortly after its timeout, carrying git's
// stderr tail.
func TestRunSkillGitReturnsWithinItsBound(t *testing.T) {
	t.Parallel()
	script := filepath.Join(t.TempDir(), "git")
	if err := testjail.WriteExecutable(
		script,
		[]byte("#!/bin/sh\necho 'fatal: slow remote' >&2\nsleep 12 &\nsleep 12\n"),
		0o755,
	); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := runSkillGitWith(
		deps.RealRunner{},
		200*time.Millisecond,
		100*time.Millisecond,
		script,
		t.TempDir(),
		"clone",
	)
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Fatalf("git returned after %s, past its bound (err=%v)", elapsed, err)
	}
	if err == nil || !strings.Contains(err.Error(), "timed out") ||
		!strings.Contains(err.Error(), "fatal: slow remote") {
		t.Fatalf("timeout error does not carry git's stderr tail: %v", err)
	}
}

// TestShippedSkillSourcesRegistryLoads loads the clone's real
// templates/global/skills/sources.json the way pfm install does: a registry
// that does not parse, or an entry without a usable repo, would pass every
// fixture-driven test here and then fail pfm install on every host.
func TestShippedSkillSourcesRegistryLoads(t *testing.T) {
	_, source, _, ok := goRuntime.Caller(0)
	if !ok {
		t.Fatal("find test source")
	}
	repo := filepath.Join(filepath.Dir(source), "..", "..", "..")
	sources, present, err := loadSkillSources(repo, ThemeManifestURL(""))
	if err != nil || !present || len(sources) == 0 {
		t.Fatalf("load %s: present=%t sources=%d err=%v", skillSourcesRelative, present, len(sources), err)
	}
	for _, entry := range sources {
		if !strings.HasPrefix(entry.Repo, "https://") || entry.Problem != "" {
			t.Fatalf("entry %s: repo=%q problem=%q", entry.Name, entry.Repo, entry.Problem)
		}
	}
}

// TestSkillStoreUnlockReleasesForkedCopies pins the release against a child
// forked while the lock was held: until it execs, the child keeps a copy of
// the lock's descriptor, and closing ours alone left the lock with it.
func TestSkillStoreUnlockReleasesForkedCopies(t *testing.T) {
	if goRuntime.GOOS != "linux" {
		return // the copy is found through /proc/self/fd
	}
	home := t.TempDir()
	root := skillStoreRoot(home)
	parent := filepath.Dir(root)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	installer := &engine{options: Options{Home: home, Stdout: &bytes.Buffer{}}, apply: true}
	unlock, busy, err := installer.lockSkillStore(root)
	if err != nil || busy {
		t.Fatalf("first lock: busy=%t err=%v", busy, err)
	}
	physical, err := filepath.EvalSymlinks(parent)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	forked := -1
	for _, entry := range entries {
		if target, err := os.Readlink("/proc/self/fd/" + entry.Name()); err == nil && target == physical {
			fd := 0
			if _, err := fmt.Sscan(entry.Name(), &fd); err != nil {
				t.Fatal(err)
			}
			if forked, err = syscall.Dup(fd); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if forked < 0 {
		t.Fatalf("no descriptor open on %s", physical)
	}
	t.Cleanup(func() {
		if err := syscall.Close(forked); err != nil {
			t.Error(err)
		}
	})
	unlock()
	relock, busy, err := installer.lockSkillStore(root)
	if err != nil || busy {
		t.Fatalf("lock after release with a forked copy open: busy=%t err=%v", busy, err)
	}
	relock()
}
