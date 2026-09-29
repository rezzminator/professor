package installer

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
		options.ConfigDirs = []string{second}
	})

	store := filepath.Join(skillStoreRoot(home), "god-speed")
	if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# god-speed v1\n" {
		t.Fatalf("store SKILL.md = %q\n%s", got, output)
	}
	assertLink(t, filepath.Join(home, ".claude", "skills", "god-speed"), store)
	assertLink(t, filepath.Join(second, "skills", "god-speed"), store)
	assertLink(t, filepath.Join(home, ".agents", "skills", "god-speed"), store)
}

// TestSourceFetchedSkillsReinstallFetchesTheNewCommit pins "always latest": a
// second install hard-resets the store to the repo's new default-branch head.
func TestSourceFetchedSkillsReinstallFetchesTheNewCommit(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "ghost-writer"), map[string]string{"SKILL.md": "# v1\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"ghostwriter": "file://" + repo}))
	runSkillInstall(t, home, ModeApply)

	skillFixtureCommit(t, repo, map[string]string{"SKILL.md": "# v2\n"})
	output := runSkillInstall(t, home, ModeApply)

	store := filepath.Join(skillStoreRoot(home), "ghostwriter")
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
	repo := skillFixtureRepo(t, filepath.Join(t.TempDir(), "vision-factory"), map[string]string{"SKILL.md": "# vf\n"})
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"vision-factory": "file://" + repo}))

	output := runSkillInstall(t, home, ModeDryRun)

	store := filepath.Join(skillStoreRoot(home), "vision-factory")
	requireNoPath(t, store, "dry run cloned")
	requireNoPath(t, filepath.Join(home, ".agents", "skills", "vision-factory"), "dry run linked")
	requireNoPath(t, filepath.Join(home, ".claude", "skills", "vision-factory"), "dry run linked")
	for _, want := range []string{
		"change  fetch file://" + repo + " -> " + store,
		"change  link " + filepath.Join(home, ".agents", "skills", "vision-factory") + " -> " + store,
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
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"ghostwriter": "file://" + repo}))
	runSkillInstall(t, home, ModeApply)

	gone := filepath.Join(t.TempDir(), "no-such-repo")
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{
		"ghostwriter": "file://" + gone,
		"fresh":       "file://" + gone,
	}))
	output := runSkillInstall(t, home, ModeApply)

	store := filepath.Join(skillStoreRoot(home), "ghostwriter")
	for _, want := range []string{"SKILL-FETCH-FAILED ghostwriter: ", "SKILL-FETCH-FAILED fresh: "} {
		if !strings.Contains(output, want) {
			t.Fatalf("fetch failure not reported as %q:\n%s", want, output)
		}
	}
	if got := readSkillFile(t, filepath.Join(store, "SKILL.md")); got != "# kept\n" {
		t.Fatalf("fetch failure lost the store copy: %q", got)
	}
	assertLink(t, filepath.Join(home, ".agents", "skills", "ghostwriter"), store)
	requireNoPath(t, filepath.Join(skillStoreRoot(home), "fresh"), "a failed first fetch left a store")
	requireNoPath(t, filepath.Join(home, ".agents", "skills", "fresh"), "a failed first fetch linked")
	entries, err := os.ReadDir(skillStoreRoot(home))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			t.Fatalf("a failed clone left staging directory %s", entry.Name())
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
		path := writeSkillRegistry(t, home, "{not json")
		output := runSkillInstall(t, home, ModeApply)
		if !strings.Contains(output, "SKILL-SOURCES-FAILED decode "+path) {
			t.Fatalf("an unparsable registry was not a failure naming %s:\n%s", path, output)
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
	writeSkillRegistry(t, home, skillRegistryJSON(map[string]string{"ghostwriter": "file://" + repo}))
	own := filepath.Join(home, ".claude", "skills", "ghostwriter")
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
		filepath.Join(home, ".agents", "skills", "ghostwriter"),
		filepath.Join(skillStoreRoot(home), "ghostwriter"),
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
	runSkillInstall(t, home, ModeApply)

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
	assertLink(t, operator, drop)
}
