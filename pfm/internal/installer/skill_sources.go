package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/codexgen"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// skillSourcesRelative is the host-global registry of skills fetched from
// their own public repositories — pfm install is its one owner: it fetches
// each into the store (skillStoreRoot) and links it into every Claude
// account's skills/ and into ~/.agents/skills/ (read by Codex and OpenCode).
const skillSourcesRelative = "templates/global/skills/sources.json"

// skillGitTimeout bounds every git call a skill fetch makes.
const skillGitTimeout = 60 * time.Second

var (
	skillSourceName       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	unresolvedPlaceholder = regexp.MustCompile(`\{[A-Z][A-Z0-9_]*\}`)
)

type skillSourceRegistry struct {
	Comment       string                       `json:"_comment,omitempty"`
	SourceFetched map[string]skillSourceRecord `json:"source_fetched"`
}

type skillSourceRecord struct {
	Repo         string   `json:"repo"`
	Parameterize []string `json:"parameterize"`
}

// skillSource is one validated registry entry. Problem, when set, is the
// named skip line the entry reports instead of being fetched or linked.
type skillSource struct {
	Name    string
	Repo    string
	Problem string
}

func skillStoreRoot(home string) string {
	return filepath.Join(managedRootForHome(home), "skills")
}

// loadSkillSources reads and validates the registry at
// {sourceRepo}/templates/global/skills/sources.json. present=false is an
// absent file; any other read, placeholder or decode failure is an error
// naming the path — never an empty registry.
func loadSkillSources(sourceRepo, manifestURL string) ([]skillSource, bool, error) {
	path := filepath.Join(sourceRepo, filepath.FromSlash(skillSourcesRelative))
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", path, err)
	}
	content, err = resolveOwnerPlaceholder(content, sourceRepo, manifestURL)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", path, err)
	}
	var registry skillSourceRegistry
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&registry); err != nil {
		return nil, false, fmt.Errorf("decode %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, false, fmt.Errorf("decode %s: trailing content after the registry object", path)
	}
	templates := filepath.Join(sourceRepo, "templates", "global", "skills")
	sources := make([]skillSource, 0, len(registry.SourceFetched))
	for name, record := range registry.SourceFetched {
		source := skillSource{Name: name, Repo: strings.TrimSpace(record.Repo)}
		switch {
		case !skillSourceName.MatchString(name):
			source.Problem = "SKILL-SOURCE-INVALID " + name + ": name must be one segment matching " +
				skillSourceName.String()
		case source.Repo == "":
			source.Problem = "SKILL-SOURCE-INVALID " + name + ": repo is empty in " + path
		case unresolvedPlaceholder.MatchString(source.Repo):
			source.Problem = "SKILL-SOURCE-UNRESOLVED " + name + ": repo " + source.Repo +
				" carries unresolved placeholder " + unresolvedPlaceholder.FindString(source.Repo)
		case len(record.Parameterize) != 0:
			source.Problem = "SKILL-SOURCE-UNSUPPORTED " + name + ": parameterize " +
				strings.Join(record.Parameterize, ",") + " is not applied by pfm install"
		}
		if source.Problem == "" {
			clash := filepath.Join(templates, name)
			if info, statErr := os.Stat(clash); statErr == nil && info.IsDir() {
				source.Problem = "SKILL-SOURCE-CLASH " + name + ": " + clash +
					" ships a template skill of the same name; the template skill is linked"
			} else if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
				return nil, false, fmt.Errorf("inspect template skill %s: %w", clash, statErr)
			}
		}
		sources = append(sources, source)
	}
	sort.Slice(sources, func(left, right int) bool { return sources[left].Name < sources[right].Name })
	return sources, true, nil
}

// wireSourceFetchedSkills fetches every registered source-fetched skill into
// its store and links it into every Claude account's skills/ and into
// ~/.agents/skills/. A registry that cannot be read is a named
// SKILL-SOURCES-FAILED line (as a theme manifest failure is): the rest of the
// install continues and pfm doctor reports it as a failure. A fetch failure
// is a named skip that keeps an existing store copy linked.
func (installer *engine) wireSourceFetchedSkills(sourceRepo string) error {
	registry := filepath.Join(sourceRepo, filepath.FromSlash(skillSourcesRelative))
	sources, present, err := loadSkillSources(sourceRepo, installer.options.ThemeManifestURL)
	if err != nil {
		installer.skip("SKILL-SOURCES-FAILED " + err.Error())
		return nil
	}
	if !present {
		installer.skip("source-fetched skills registry absent at " + registry + " (0 entries)")
		return nil
	}
	storeRoot := skillStoreRoot(installer.options.Home)
	installer.say("source-fetched skills -> %s", storeRoot)
	active := make(map[string]bool, len(sources))
	for _, source := range sources {
		if !strings.HasPrefix(source.Problem, "SKILL-SOURCE-CLASH ") {
			active[source.Name] = true
		}
	}
	if err := installer.retireSkillSources(active); err != nil {
		return err
	}
	for _, source := range sources {
		if source.Problem != "" {
			installer.skip(source.Problem)
			continue
		}
		if err := installer.wireSourceFetchedSkill(source, storeRoot); err != nil {
			return err
		}
	}
	installer.say("")
	return nil
}

func (installer *engine) wireSourceFetchedSkill(source skillSource, storeRoot string) error {
	store := filepath.Join(storeRoot, source.Name)
	if err := installer.fetchSkillSource(source, store); err != nil {
		return err
	}
	if _, err := os.Lstat(store); errors.Is(err, fs.ErrNotExist) {
		// Nothing to link: the fetch failed (already reported) or, in a dry
		// run, the planned fetch line stands for the links it would enable.
		if installer.apply {
			return nil
		}
	} else if err != nil {
		return fmt.Errorf("inspect skill store %s: %w", store, err)
	} else if _, err := os.Stat(filepath.Join(store, "SKILL.md")); errors.Is(err, fs.ErrNotExist) {
		installer.skip("SKILL-SOURCE-MISSING " + source.Name + " (" + filepath.Join(store, "SKILL.md") + " absent)")
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect %s skill source: %w", source.Name, err)
	}
	for _, target := range installer.skillSourceLinkTargets(source.Name) {
		if err := installer.wireSkillSourceLink(store, target, storeRoot); err != nil {
			return err
		}
	}
	return nil
}

func (installer *engine) skillSourceLinkTargets(name string) []string {
	targets := make([]string, 0, len(installer.claudeConfigDirs())+1)
	for _, config := range installer.claudeConfigDirs() {
		targets = append(targets, filepath.Join(config, "skills", name))
	}
	return append(targets, filepath.Join(installer.options.Home, ".agents", "skills", name))
}

// wireSkillSourceLink is wireGlobalLink's idiom with one stricter rule: a real
// directory at the target is the operator's (a hand-copied skill), never a
// pfm copy to replace — it is a reported conflict and left untouched.
func (installer *engine) wireSkillSourceLink(store, target, storeRoot string) error {
	state, found, err := codexgen.ClassifyGlobalLink(target, store, storeRoot, codexgen.GlobalLinkDir)
	if err != nil {
		return fmt.Errorf("inspect skill link %s: %w", target, err)
	}
	switch state {
	case codexgen.GlobalLinkCorrect:
		installer.ok(target)
		return nil
	case codexgen.GlobalLinkCopy:
		installer.skip(
			"CONFLICT " + target + ": a real directory, not ours; preserved (remove it to link -> " + store + ")",
		)
		return nil
	case codexgen.GlobalLinkConflict:
		installer.skip(codexgen.DescribeGlobalLinkState(state, target, store, found) + "; preserved")
		return nil
	}
	return installer.changePaths(
		codexgen.DescribeGlobalLinkState(state, target, store, found),
		[]string{target},
		func() error {
			return codexgen.ApplyGlobalLink(target, store, state)
		},
	)
}

// fetchSkillSource brings the store to the registry repo's default-branch
// head: an absent store is shallow-cloned beside it and renamed into place; a
// present one is fetched shallowly and hard-reset. A dry run fetches nothing.
// Every fetch failure is one SKILL-FETCH-FAILED skip; the returned error is
// reserved for a failed filesystem write the journal owns.
func (installer *engine) fetchSkillSource(source skillSource, store string) error {
	if installer.options.SkillSourcesOffline {
		installer.skip(
			"SKILL-FETCH-SKIPPED " + source.Name + ": " + paths.EnvSkillSourcesOffline + "=1 (a store copy is still linked)",
		)
		return nil
	}
	info, err := os.Lstat(store)
	if errors.Is(err, fs.ErrNotExist) {
		message := "fetch " + source.Repo + " -> " + store
		if !installer.apply {
			return installer.changePaths(message, []string{store}, func() error { return nil })
		}
		if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
			return fmt.Errorf("create skill store root %s: %w", filepath.Dir(store), err)
		}
		staging, err := os.MkdirTemp(filepath.Dir(store), "."+source.Name+".fetch-")
		if err != nil {
			return fmt.Errorf("create skill staging directory beside %s: %w", store, err)
		}
		if _, err := installer.runSkillGit(
			"",
			"clone",
			"--depth",
			"1",
			"--quiet",
			"--",
			source.Repo,
			staging,
		); err != nil {
			if removeErr := os.RemoveAll(staging); removeErr != nil {
				installer.skip("leave skill staging directory " + staging + ": " + removeErr.Error())
			}
			installer.skip("SKILL-FETCH-FAILED " + source.Name + ": " + err.Error())
			return nil
		}
		err = installer.changePaths(message, []string{store}, func() error { return os.Rename(staging, store) })
		if err != nil {
			if removeErr := os.RemoveAll(staging); removeErr != nil {
				installer.skip("leave skill staging directory " + staging + ": " + removeErr.Error())
			}
		}
		return err
	}
	if err != nil {
		return fmt.Errorf("inspect skill store %s: %w", store, err)
	}
	if !info.IsDir() {
		installer.skip("SKILL-FETCH-FAILED " + source.Name + ": store " + store + " is not a directory")
		return nil
	}
	if !installer.apply {
		installer.say("  plan    fetch %s into %s (dry run: not fetched)", source.Repo, store)
		return nil
	}
	if _, err := installer.runSkillGit(
		store,
		"fetch",
		"--depth",
		"1",
		"--quiet",
		"--",
		source.Repo,
		"HEAD",
	); err != nil {
		installer.skip("SKILL-FETCH-FAILED " + source.Name + ": " + err.Error() + " (keeping " + store + ")")
		return nil
	}
	current, err := installer.runSkillGit(store, "rev-parse", "HEAD")
	if err != nil {
		installer.skip("SKILL-FETCH-FAILED " + source.Name + ": " + err.Error() + " (keeping " + store + ")")
		return nil
	}
	fetched, err := installer.runSkillGit(store, "rev-parse", "FETCH_HEAD")
	if err != nil {
		installer.skip("SKILL-FETCH-FAILED " + source.Name + ": " + err.Error() + " (keeping " + store + ")")
		return nil
	}
	if current == fetched {
		installer.ok(store + " at " + shortCommit(fetched))
		return nil
	}
	message := "update " + store + " " + shortCommit(current) + " -> " + shortCommit(fetched) + " from " + source.Repo
	return installer.changePaths(message, []string{store}, func() error {
		if _, err := installer.runSkillGit(store, "reset", "--hard", "--quiet", "FETCH_HEAD"); err != nil {
			return fmt.Errorf("reset skill store %s: %w", store, err)
		}
		if _, err := installer.runSkillGit(store, "clean", "-ffdxq"); err != nil {
			return fmt.Errorf("clean skill store %s: %w", store, err)
		}
		return nil
	})
}

// runSkillGit runs one git call through the installer's process runner under
// skillGitTimeout with credential prompts disabled, returning trimmed stdout;
// a failure carries git's stderr tail.
func (installer *engine) runSkillGit(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), skillGitTimeout)
	defer cancel()
	git, err := deps.Resolve("git")
	if err != nil {
		return "", fmt.Errorf("resolve git: %w", err)
	}
	result, err := installer.processRunner().Run(ctx, append([]string{git}, args...), deps.RunOptions{
		Dir: dir,
		Env: append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS="),
	})
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "", fmt.Errorf("git %s timed out after %s", args[0], skillGitTimeout)
	}
	if err != nil || result.ExitCode != 0 {
		tail := strings.TrimSpace(string(result.Stderr))
		if lines := strings.Split(tail, "\n"); len(lines) > 3 {
			tail = strings.Join(lines[len(lines)-3:], "\n")
		}
		return "", fmt.Errorf("git %s: exit %d: %v: %s", strings.Join(args, " "), result.ExitCode, err, tail)
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// skillSourceLinkDirs are the registries a source-fetched skill is linked
// into: every Claude account's skills/ and ~/.agents/skills/.
func (installer *engine) skillSourceLinkDirs() []string {
	dirs := make([]string, 0, len(installer.claudeConfigDirs())+1)
	for _, config := range installer.claudeConfigDirs() {
		dirs = append(dirs, filepath.Join(config, "skills"))
	}
	return append(dirs, filepath.Join(installer.options.Home, ".agents", "skills"))
}

// retireSkillSources removes every link resolving into the skill store whose
// store name is not in active, then every store entry not in active (a
// leftover staging directory included). Uninstall passes an empty set.
func (installer *engine) retireSkillSources(active map[string]bool) error {
	storeRoot := skillStoreRoot(installer.options.Home)
	for _, dir := range installer.skillSourceLinkDirs() {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect skill registry %s: %w", dir, err)
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			target, linked := resolvedLink(path)
			if !linked || !withinGlobalSource(target, storeRoot) || target == storeRoot {
				continue
			}
			relative, err := filepath.Rel(storeRoot, target)
			if err != nil {
				return fmt.Errorf("resolve skill link %s -> %s: %w", path, target, err)
			}
			name := strings.SplitN(relative, string(filepath.Separator), 2)[0]
			if active[name] {
				continue
			}
			if err := installer.retire(path, "unregistered source-fetched skill "+name); err != nil {
				return err
			}
		}
	}
	entries, err := os.ReadDir(storeRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect skill store %s: %w", storeRoot, err)
	}
	for _, entry := range entries {
		if active[entry.Name()] {
			continue
		}
		path := filepath.Join(storeRoot, entry.Name())
		installer.markRemoved(path)
		if err := installer.changePaths(
			"retire "+path+" (unregistered source-fetched skill store)",
			[]string{path},
			func() error { return os.RemoveAll(path) },
		); err != nil {
			return err
		}
	}
	return nil
}

// unwireSkillSources is uninstall's half: every store link and the store.
func (installer *engine) unwireSkillSources() error {
	if err := installer.retireSkillSources(map[string]bool{}); err != nil {
		return err
	}
	return installer.retireEmptyDir(skillStoreRoot(installer.options.Home))
}
