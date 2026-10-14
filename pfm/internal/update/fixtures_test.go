package update

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// updateFixtureTemplates holds every fixture tree this test process built:
// each key's builder runs once, into its own directory under root, and every
// caller works on a private copy of it. Building the same git history once
// instead of once per test is most of this package's suite time.
var updateFixtureTemplates struct {
	sync.Mutex
	root    string
	entries map[string]*updateFixtureEntry
}

type updateFixtureEntry struct {
	once   sync.Once
	built  bool
	root   string
	values []string
}

// updateFixtureTemplate returns the template directory for key and the values
// its build returned, building it on first use. build writes into root, which
// outlives the calling test, so it never uses t.TempDir, t.Cleanup or
// t.Setenv. A build that failed fails every later caller of the key.
func updateFixtureTemplate(
	t *testing.T,
	key string,
	build func(t *testing.T, root string) []string,
) (root string, values []string) {
	t.Helper()
	updateFixtureTemplates.Lock()
	if updateFixtureTemplates.root == "" {
		base, err := os.MkdirTemp("", "update-fixtures-")
		if err != nil {
			updateFixtureTemplates.Unlock()
			t.Fatalf("create the fixture template root: %v", err)
		}
		updateFixtureTemplates.root = base
		updateFixtureTemplates.entries = map[string]*updateFixtureEntry{}
	}
	entry := updateFixtureTemplates.entries[key]
	if entry == nil {
		entry = &updateFixtureEntry{
			root: filepath.Join(updateFixtureTemplates.root, fmt.Sprintf("t%d", len(updateFixtureTemplates.entries))),
		}
		updateFixtureTemplates.entries[key] = entry
	}
	updateFixtureTemplates.Unlock()
	entry.once.Do(func() {
		if err := os.Mkdir(entry.root, 0o700); err != nil {
			t.Fatalf("create the %q fixture template: %v", key, err)
		}
		entry.values = build(t, entry.root)
		entry.built = true
	})
	if !entry.built {
		t.Fatalf("the %q fixture template was not built: its build failed in an earlier test", key)
	}
	return entry.root, entry.values
}

func removeUpdateFixtureTemplates() error {
	updateFixtureTemplates.Lock()
	defer updateFixtureTemplates.Unlock()
	if updateFixtureTemplates.root == "" {
		return nil
	}
	return os.RemoveAll(updateFixtureTemplates.root)
}

// copyUpdateFixtureTree copies source's tree into target, keeping each
// directory's and file's permission bits and each symlink's target.
func copyUpdateFixtureTree(t *testing.T, source, target string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch mode := info.Mode(); {
		case mode.IsDir():
			return os.MkdirAll(destination, mode.Perm())
		case mode.IsRegular():
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return testjail.WriteExecutable(destination, body, mode.Perm())
		case mode&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, destination)
		default:
			return fmt.Errorf("%s: unsupported file type %s", path, mode.Type())
		}
	})
	if err != nil {
		t.Fatalf("copy fixture template %s to %s: %v", source, target, err)
	}
}

// relinkUpdateFixtureRemote points a copied repository's origin at its own
// copied remote. The template's url must appear exactly once: a copy left
// pointing at the shared template remote would let one test's push reach
// every later test.
func relinkUpdateFixtureRemote(t *testing.T, repo, from, to string) {
	t.Helper()
	config := filepath.Join(repo, ".git", "config")
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	old := "url = " + from + "\n"
	if count := strings.Count(string(raw), old); count != 1 {
		t.Fatalf("%s holds %q %d times, want once:\n%s", config, old, count, raw)
	}
	relinked := strings.Replace(string(raw), old, "url = "+to+"\n", 1)
	if err := os.WriteFile(config, []byte(relinked), 0o600); err != nil {
		t.Fatal(err)
	}
}

// cloneUpdateGitFixture gives the caller a private copy of a template holding
// a source clone at repo/ and its origin at remote.git/: the clone in a temp
// dir of its own and the remote in another, as the builders lay them out.
func cloneUpdateGitFixture(t *testing.T, template string) string {
	t.Helper()
	repo := t.TempDir()
	remote := filepath.Join(t.TempDir(), "remote.git")
	copyUpdateFixtureTree(t, filepath.Join(template, "repo"), repo)
	copyUpdateFixtureTree(t, filepath.Join(template, "remote.git"), remote)
	relinkUpdateFixtureRemote(t, repo, filepath.Join(template, "remote.git"), remote)
	return repo
}

// TestUpdateGitFixtureCopiesOwnTheirRemote: a fixture copy's origin is its own
// copied remote, so a push from one test (as the baseline-doctor price test
// pushes newer release tags) reaches neither the template nor a later copy.
func TestUpdateGitFixtureCopiesOwnTheirRemote(t *testing.T) {
	t.Parallel()
	first := newUpdateGitFixture(t)
	gitTemp(t, first, "tag", "v0.11.0")
	gitTemp(t, first, "push", "-q", "origin", "v0.11.0")
	second := newUpdateGitFixture(t)
	for repo, want := range map[string]bool{first: true, second: false} {
		command := exec.Command("git", "ls-remote", "--tags", "origin", "refs/tags/v0.11.0")
		command.Dir = repo
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git ls-remote in %s: %v\n%s", repo, err, output)
		}
		if got := strings.Contains(string(output), "refs/tags/v0.11.0"); got != want {
			t.Errorf("origin of %s holds v0.11.0 = %v, want %v: %q", repo, got, want, output)
		}
	}
}

func newUpdateGitFixture(t *testing.T) string {
	return newUpdateGitFixtureWithIgnore(t, nil)
}

func newUpdateGitFixtureWithIgnore(t *testing.T, ignore []byte) string {
	t.Helper()
	key := "update-git"
	if ignore != nil {
		key += "+ignore:" + string(ignore)
	}
	template, _ := updateFixtureTemplate(t, key, func(t *testing.T, root string) []string {
		buildUpdateGitFixture(t, root, ignore)
		return nil
	})
	return cloneUpdateGitFixture(t, template)
}

// buildUpdateGitFixture writes the source clone to root/repo and its origin to
// root/remote.git: v0.9.0 and v0.10.0 pushed, the clone on branch "installed"
// at v0.9.0.
func buildUpdateGitFixture(t *testing.T, root string, ignore []byte) {
	t.Helper()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "init", "-q")
	gitTemp(t, repo, "config", "user.email", "fixture.invalid")
	gitTemp(t, repo, "config", "user.name", "fixture-identity")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ignore != nil {
		if err := os.WriteFile(filepath.Join(repo, ".gitignore"), ignore, 0o600); err != nil {
			t.Fatalf("seed fixture .gitignore: %v", err)
		}
		gitTemp(t, repo, "add", "README.md", ".gitignore")
	} else {
		gitTemp(t, repo, "add", "README.md")
	}
	gitTemp(t, repo, "commit", "-qm", "fixture")
	gitTemp(t, repo, "tag", "v0.9.0")
	if err := os.WriteFile(filepath.Join(repo, "RELEASE"), []byte("next\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "add", "RELEASE")
	gitTemp(t, repo, "commit", "-qm", "fixture next release")
	gitTemp(t, repo, "tag", "v0.10.0")
	remote := filepath.Join(root, "remote.git")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, remote, "init", "--bare", "-q")
	gitTemp(t, repo, "remote", "add", "origin", remote)
	gitTemp(t, repo, "push", "-q", "origin", "HEAD", "--tags")
	gitTemp(t, repo, "checkout", "-qb", "installed", "v0.9.0")
}

// newDetachedUpdateGitFixture is newUpdateGitFixture, except the source
// clone is checked out at v0.9.0 detached rather than on a branch named
// "installed" — the shape a maintainer's clone takes after `git checkout
// v0.9.0` directly.
func newDetachedUpdateGitFixture(t *testing.T) string {
	t.Helper()
	repo := newUpdateGitFixture(t)
	gitTemp(t, repo, "checkout", "-q", "--detach", "v0.9.0")
	return repo
}

// newReleaseNotesUpdateFixture is newUpdateGitFixture, plus a releases/ file
// per name in releaseFiles committed and tagged v0.10.0 (the update target),
// so a real runUpdate can be driven end to end to observe the printed
// release-notes report lines.
func newReleaseNotesUpdateFixture(t *testing.T, releaseFiles []string) string {
	t.Helper()
	key := "release-notes:" + strings.Join(releaseFiles, ",")
	template, _ := updateFixtureTemplate(t, key, func(t *testing.T, root string) []string {
		buildReleaseNotesUpdateFixture(t, root, releaseFiles)
		return nil
	})
	return cloneUpdateGitFixture(t, template)
}

func buildReleaseNotesUpdateFixture(t *testing.T, root string, releaseFiles []string) {
	t.Helper()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "init", "-q")
	gitTemp(t, repo, "config", "user.email", "fixture.invalid")
	gitTemp(t, repo, "config", "user.name", "fixture-identity")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "add", "README.md")
	gitTemp(t, repo, "commit", "-qm", "fixture")
	gitTemp(t, repo, "tag", "v0.9.0")
	// Always commit at least the marker file, even with no release notes
	// between the two tags, so "nothing between" is a real, empty
	// releases/ listing rather than an aborted commit.
	if err := os.WriteFile(filepath.Join(repo, "NEXT"), []byte("next\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "add", "NEXT")
	for _, name := range releaseFiles {
		path := filepath.Join(repo, "releases", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		gitTemp(t, repo, "add", filepath.Join("releases", name))
	}
	gitTemp(t, repo, "commit", "-qm", "fixture release notes")
	gitTemp(t, repo, "tag", "v0.10.0")
	remote := filepath.Join(root, "remote.git")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, remote, "init", "--bare", "-q")
	gitTemp(t, repo, "remote", "add", "origin", remote)
	gitTemp(t, repo, "push", "-q", "origin", "HEAD", "--tags")
	gitTemp(t, repo, "checkout", "-qb", "installed", "v0.9.0")
}

// newUntaggedPreviousReleaseNotesFixture is newReleaseNotesUpdateFixture,
// except the source clone's checked-out branch sits at an UNTAGGED commit
// (no tag reaches it), so `git describe --tags` on previousRef fails and
// releaseNotesForUpdate reports its own error rather than an empty listing.
func newUntaggedPreviousReleaseNotesFixture(t *testing.T) string {
	t.Helper()
	template, _ := updateFixtureTemplate(t, "untagged-previous", func(t *testing.T, root string) []string {
		buildUntaggedPreviousReleaseNotesFixture(t, root)
		return nil
	})
	return cloneUpdateGitFixture(t, template)
}

func buildUntaggedPreviousReleaseNotesFixture(t *testing.T, root string) {
	t.Helper()
	repo := filepath.Join(root, "repo")
	if err := os.Mkdir(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "init", "-q")
	gitTemp(t, repo, "config", "user.email", "fixture.invalid")
	gitTemp(t, repo, "config", "user.name", "fixture-identity")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "add", "README.md")
	gitTemp(t, repo, "commit", "-qm", "fixture untagged base")
	gitTemp(t, repo, "branch", "installed")
	if err := os.WriteFile(filepath.Join(repo, "TAGGED"), []byte("tagged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "add", "TAGGED")
	gitTemp(t, repo, "commit", "-qm", "fixture v0.9.0")
	gitTemp(t, repo, "tag", "v0.9.0")
	if err := os.WriteFile(filepath.Join(repo, "RELEASE"), []byte("next\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, repo, "add", "RELEASE")
	gitTemp(t, repo, "commit", "-qm", "fixture v0.10.0")
	gitTemp(t, repo, "tag", "v0.10.0")
	remote := filepath.Join(root, "remote.git")
	if err := os.MkdirAll(remote, 0o700); err != nil {
		t.Fatal(err)
	}
	gitTemp(t, remote, "init", "--bare", "-q")
	gitTemp(t, repo, "remote", "add", "origin", remote)
	gitTemp(t, repo, "push", "-q", "origin", "HEAD", "--tags")
	gitTemp(t, repo, "checkout", "-q", "installed")
}

// newAdoptAtStoreFixture builds a full planInitCopies-compatible blueprint
// store (every initTemplatePaths source must exist or planInitCopies fails)
// with two commits: old has templates/project/commands/dev.md = "old\n";
// new changes dev.md to "new\n" and adds templates/project/commands/later.md.
func newAdoptAtStoreFixture(t *testing.T) (store, oldSHA, headSHA string) {
	t.Helper()
	template, shas := updateFixtureTemplate(t, "adopt-at-store", buildAdoptAtStoreFixture)
	store = t.TempDir()
	copyUpdateFixtureTree(t, template, store)
	return store, shas[0], shas[1]
}

func buildAdoptAtStoreFixture(t *testing.T, root string) []string {
	t.Helper()
	for relative, content := range map[string]string{
		"VERSION":                                                          "0.65.0\n",
		"templates/project/CLAUDE.md":                                      "# {TOKEN} contract\n",
		"templates/project/settings.json":                                  "{}\n",
		"templates/project/rumdl-policy.toml":                              "# fixture rumdl policy\n[global]\n",
		"templates/project/commands/dev.md":                                "old\n",
		"templates/project/agents/gitter.md":                               "---\nname: gitter\n---\nbody\n",
		"templates/project/scripts/dev.sh":                                 "#!/usr/bin/env bash\nset -euo pipefail\n",
		"templates/project/skills/legal/SKILL.md":                          "---\nname: legal\n---\nbody\n",
		"templates/project/epics/TEMPLATE.md":                              "# Epic\n",
		"templates/project/codex/config.toml":                              "model = \"{TOKEN}\"\n",
		"templates/project/docs-commands/git/references/gitter-history.md": "# Gitter History\n",
		"templates/project/docs-hub/_index.md":                             "# Documentation Hub\n",
	} {
		writeProjectFixtureFile(t, root, relative, content)
	}
	gitTemp(t, root, "init", "-q")
	gitTemp(t, root, "config", "user.email", "fixture.invalid")
	gitTemp(t, root, "config", "user.name", "fixture-identity")
	gitTemp(t, root, "add", ".")
	gitTemp(t, root, "commit", "-qm", "old templates")
	oldSHA := projectGitShortSHA(t, root)

	writeProjectFixtureFile(t, root, "templates/project/commands/dev.md", "new\n")
	writeProjectFixtureFile(t, root, "templates/project/commands/later.md", "later\n")
	gitTemp(t, root, "add", "-A")
	gitTemp(t, root, "commit", "-qm", "new templates")
	return []string{oldSHA, projectGitShortSHA(t, root)}
}

func newScaffoldStoreFixture(t *testing.T) string {
	t.Helper()
	template, _ := updateFixtureTemplate(t, "scaffold-store", buildScaffoldStoreFixture)
	store := t.TempDir()
	copyUpdateFixtureTree(t, template, store)
	return store
}

func buildScaffoldStoreFixture(t *testing.T, root string) []string {
	t.Helper()
	files := map[string]struct {
		content string
		mode    os.FileMode
	}{
		"VERSION":                         {content: "0.65.0\n", mode: 0o600},
		"templates/project/CLAUDE.md":     {content: "# {TOKEN} contract\n", mode: 0o600},
		"templates/project/settings.json": {content: "{}\n", mode: 0o600},
		"templates/project/rumdl-policy.toml": {
			content: "# fixture rumdl policy\n[global]\n",
			mode:    0o600,
		},
		"templates/project/commands/dev.md": {
			content: "---\nname: dev\n---\n{TOKEN}\n",
			mode:    0o600,
		},
		"templates/project/agents/gitter.md": {
			content: "---\nname: gitter\n---\nbody\n",
			mode:    0o600,
		},
		"templates/project/commands/per-project/testing-manual.md": {
			content: "---\nname: testing-manual\n---\nbody\n",
			mode:    0o600,
		},
		"templates/project/scripts/dev.sh": {
			content: "#!/usr/bin/env bash\nset -euo pipefail\n",
			mode:    0o755,
		},
		"templates/project/skills/legal/SKILL.md": {content: "---\nname: legal\n---\nbody\n", mode: 0o600},
		"templates/project/epics/TEMPLATE.md":     {content: "# Epic\n", mode: 0o600},
		"templates/project/codex/config.toml": {
			content: "model = \"{TOKEN}\"\n",
			mode:    0o600,
		},
		"templates/project/docs-commands/git/references/gitter-history.md": {
			content: "# Gitter History\n",
			mode:    0o600,
		},
		"templates/project/docs-hub/_index.md": {content: "# Documentation Hub\n", mode: 0o600},
	}
	for relative, fixture := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := testjail.WriteExecutable(path, []byte(fixture.content), fixture.mode); err != nil {
			t.Fatal(err)
		}
	}
	gitTemp(t, root, "init", "-q")
	gitTemp(t, root, "config", "user.email", "fixture.invalid")
	gitTemp(t, root, "config", "user.name", "fixture-identity")
	gitTemp(t, root, "add", ".")
	gitTemp(t, root, "commit", "-qm", "fixture store")
	return nil
}
