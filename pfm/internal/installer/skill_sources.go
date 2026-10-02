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
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/codexgen"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// skillSourcesRelative is the host-global registry of skills fetched from
// their own public repositories — pfm install is its one owner: it fetches
// each into the store (skillStoreRoot) and links it into every Claude
// account's skills/ and into ~/.agents/skills/ (read by Codex and OpenCode).
const skillSourcesRelative = "templates/global/skills/sources.json"

const (
	// skillGitTimeout bounds every git call a skill fetch makes.
	skillGitTimeout = 60 * time.Second
	// skillGitWaitDelay bounds the wait, past skillGitTimeout, for a git
	// grandchild still holding the output pipes.
	skillGitWaitDelay = 2 * time.Second
)

var (
	skillSourceName       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	unresolvedPlaceholder = regexp.MustCompile(`\{[A-Z][A-Z0-9_]*\}`)
	// skillGitRepoVars select a repository or its storage: a skill git call
	// drops them, so no inherited GIT_DIR or GIT_WORK_TREE steers it into
	// another repository, while every other GIT_* (a CA bundle, a proxy, an
	// ssh command) passes through.
	skillGitRepoVars = map[string]bool{
		"GIT_DIR": true, "GIT_WORK_TREE": true, "GIT_COMMON_DIR": true, "GIT_INDEX_FILE": true,
		"GIT_OBJECT_DIRECTORY": true, "GIT_ALTERNATE_OBJECT_DIRECTORIES": true, "GIT_NAMESPACE": true,
		"GIT_IMPLICIT_WORK_TREE": true, "GIT_PREFIX": true, "GIT_SHALLOW_FILE": true, "GIT_GRAFT_FILE": true,
		"GIT_CEILING_DIRECTORIES": true,
	}
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
	if registry.SourceFetched == nil {
		return nil, false, fmt.Errorf("decode %s: no source_fetched object", path)
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

// wireSourceFetchedSkills fetches registered skills into managed storage and links
// them into the store skills registry and ~/.agents/skills.
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
	unlock, busy, err := installer.lockSkillStore(storeRoot)
	if err != nil {
		installer.skip("SKILL-SOURCES-FAILED " + err.Error())
		return nil
	}
	if busy {
		installer.skip(
			"SKILL-SOURCES-BUSY another pfm install holds " + storeRoot + "; source-fetched skills left as they are",
		)
		return nil
	}
	defer unlock()
	rootExists, err := checkSkillStoreRoot(storeRoot)
	if err != nil {
		installer.skip("SKILL-SOURCES-FAILED " + err.Error() + " (remove it, then run pfm install --yes)")
		return nil
	}
	installer.say("source-fetched skills -> %s", storeRoot)
	active := make(map[string]bool, len(sources))
	fetch := false
	for _, source := range sources {
		if !strings.HasPrefix(source.Problem, "SKILL-SOURCE-CLASH ") {
			active[source.Name] = true
		}
		fetch = fetch || source.Problem == ""
	}
	if !rootExists && fetch && installer.apply && !installer.options.SkillSourcesOffline {
		if err := installer.change("create "+storeRoot, func() error {
			return os.Mkdir(storeRoot, 0o755)
		}); err != nil {
			return err
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
		store := filepath.Join(storeRoot, source.Name)
		linkable, err := installer.fetchSkillSource(source, store)
		if err != nil {
			return err
		}
		if !linkable {
			continue
		}
		for _, target := range installer.skillSourceLinkTargets(source.Name) {
			if err := installer.wireSkillSourceLink(store, target, storeRoot); err != nil {
				return err
			}
		}
	}
	installer.say("")
	return nil
}

// checkSkillStoreRoot refuses a store root that exists as anything but a real
// directory: an operator's link there is never read or emptied through.
func checkSkillStoreRoot(storeRoot string) (exists bool, err error) {
	info, err := os.Lstat(storeRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect skill store root %s: %w", storeRoot, err)
	}
	if !info.IsDir() {
		return false, fmt.Errorf("refuse skill store root %s: not a real directory", storeRoot)
	}
	return true, nil
}

// lockSkillStore takes the skill store lock (apply only): a non-blocking
// exclusive flock on the store root's parent, the managed root, so the root's
// own creation happens under it. An absent parent is left unlocked: pfm has
// staged nothing there, and the root's os.Mkdir fails beneath it. busy=true
// means another install holds it; unlock releases it.
func (installer *engine) lockSkillStore(storeRoot string) (unlock func(), busy bool, err error) {
	if !installer.apply {
		return func() {}, false, nil
	}
	parent := filepath.Dir(storeRoot)
	root, err := os.Open(parent)
	if errors.Is(err, fs.ErrNotExist) {
		return func() {}, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("open skill store lock %s: %w", parent, err)
	}
	if err := syscall.Flock(int(root.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if closeErr := root.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("lock skill store %s: %w", parent, err)
	}
	return func() {
		if err := root.Close(); err != nil {
			installer.skip("unlock skill store " + parent + ": " + err.Error())
		}
	}, false, nil
}

func (installer *engine) skillSourceLinkTargets(name string) []string {
	targets := installer.skillSourceLinkDirs()
	for index, dir := range targets {
		targets[index] = filepath.Join(dir, name)
	}
	return targets
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
	return installer.change(
		codexgen.DescribeGlobalLinkState(state, target, store, found),
		func() error {
			return codexgen.ApplyGlobalLink(target, store, state)
		},
	)
}

// fetchSkillSource brings the store to the registry repo's default-branch
// head without running git inside it. A fresh shallow clone is swapped into
// place and its sibling commit record rewritten. A dry run reads no remote.
// Each fetch failure reports the usable tree left at the store.
func (installer *engine) fetchSkillSource(source skillSource, store string) (linkable bool, err error) {
	info, err := os.Lstat(store)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		installer.skip("SKILL-FETCH-FAILED " + source.Name + ": inspect store: " + err.Error())
		return false, nil
	}
	if exists && !info.IsDir() {
		installer.skip("SKILL-FETCH-FAILED " + source.Name + ": store " + store +
			" is not a directory; preserved (remove it, then run pfm install --yes)")
		return false, nil
	}
	if installer.options.SkillSourcesOffline {
		state := "no store copy; nothing linked"
		if exists {
			state = "the store copy is still linked"
		}
		installer.skip(
			"SKILL-FETCH-SKIPPED " + source.Name + ": " + paths.EnvSkillSourcesOffline + "=1 (" + state + ")",
		)
		return installer.linkableStore(source.Name, store), nil
	}
	if !installer.apply {
		if exists {

			installer.say("check   %s against %s (a dry run reads no remote; the apply replaces it if the head moved)",
				store, source.Repo)
			return installer.linkableStore(source.Name, store), nil
		}
		return true, installer.change(
			"fetch "+source.Repo+" -> "+store,
			func() error { return nil },
		)
	}
	keeping := ""
	if exists {
		keeping = " (keeping " + store + ")"
	}
	fail := func(reason string) (bool, error) {
		installer.skip("SKILL-FETCH-FAILED " + source.Name + ": " + reason + keeping)
		return installer.linkableStore(source.Name, store), nil
	}
	head, err := installer.runSkillGit("ls-remote", "--", source.Repo, "HEAD")
	commit, _, _ := strings.Cut(head, "\t")
	if err == nil && commit == "" {
		err = fmt.Errorf("git ls-remote %s: no HEAD", source.Repo)
	}
	if err != nil {
		return fail(err.Error())
	}
	record := skillCommitPath(store)
	recorded, err := os.ReadFile(record)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		installer.skip("unreadable skill commit record " + record + ": " + err.Error() + "; re-cloning " + source.Name)
	}
	was := strings.TrimSpace(string(recorded))
	if was == commit && checkSkillFile(store) == nil {
		installer.ok(store + " at " + shortCommit(commit))
		return true, nil
	}
	staging, err := os.MkdirTemp(filepath.Dir(store), "."+source.Name+".fetch-")
	if err != nil {
		return fail("create a staging directory: " + err.Error())
	}
	trash := staging + ".old"
	defer func() {
		if removeErr := os.RemoveAll(staging); removeErr != nil {
			installer.skip("leave skill staging directory " + staging + ": " + removeErr.Error())
		}
	}()
	if _, err := installer.runSkillGit("clone", "--depth", "1", "--quiet", "--", source.Repo, staging); err != nil {
		return fail(err.Error())
	}
	if err := checkSkillFile(staging); errors.Is(err, fs.ErrNotExist) {
		installer.skip(
			"SKILL-SOURCE-MISSING " + source.Name + " (" + source.Repo + " has no root SKILL.md" + keeping + ")",
		)
		return installer.linkableStore(source.Name, store), nil
	} else if err != nil {
		return fail(source.Repo + ": " + err.Error())
	}
	cloned, err := installer.runSkillGit("--git-dir", filepath.Join(staging, ".git"), "rev-parse", "HEAD")
	if err != nil {
		return fail(err.Error())
	}
	clone, err := os.Stat(staging)
	if err != nil {
		return fail("inspect the fresh clone: " + err.Error())
	}
	message := "fetch " + source.Repo + " -> " + store
	if exists {
		if was == "" {
			was = "unrecorded"
		}
		message = "update " + store + " " + shortCommit(was) + " -> " + shortCommit(cloned) + " from " + source.Repo
	}
	err = installer.change(message, func() error {
		if err := swapSkillStore(staging, trash, store, exists); err != nil {
			return err
		}
		return atomicfile.Write(record, []byte(cloned+"\n"), 0o644)
	})
	current, statErr := os.Lstat(store)
	if removeErr := os.RemoveAll(trash); removeErr != nil {
		installer.skip("leave the old skill store copy " + trash + ": " + removeErr.Error())
	}
	if err != nil && statErr == nil && os.SameFile(current, clone) {
		installer.skip("SKILL-FETCH-FAILED " + source.Name + ": " + err.Error() + " (" + store +
			" holds the new clone at " + shortCommit(cloned) + ")")
		return installer.linkableStore(source.Name, store), nil
	}
	if err != nil {
		return fail(err.Error())
	}
	return true, nil
}

// swapSkillStore moves the old store aside to trash and the fresh clone into
// place; a failed move puts the old store back. The caller removes trash.
func swapSkillStore(staging, trash, store string, exists bool) error {
	if !exists {
		return os.Rename(staging, store)
	}
	if err := os.Rename(store, trash); err != nil {
		return fmt.Errorf("move %s aside: %w", store, err)
	}
	if err := os.Rename(staging, store); err != nil {
		if restoreErr := os.Rename(trash, store); restoreErr != nil {
			return errors.Join(fmt.Errorf("move %s into place: %w", staging, err),
				fmt.Errorf("restore %s from %s: %w", store, trash, restoreErr))
		}
		return fmt.Errorf("move %s into place (old store restored): %w", staging, err)
	}
	return nil
}

// skillCommitPath is the pfm-owned record of the commit store was cloned at:
// a sibling in the store root, where no fetched tree reaches.
func skillCommitPath(store string) string {
	return filepath.Join(filepath.Dir(store), "."+filepath.Base(store)+".commit")
}

// errSkillFileUnusable marks a root SKILL.md that exists but is no regular file
// inside its tree.
var errSkillFileUnusable = errors.New("not a regular file inside its skill tree")

// checkSkillFile is the one inspection pfm makes of a fetched tree: dir's root
// SKILL.md must be a regular file, or a link resolving to one inside dir. Its
// three failures read apart: absent wraps fs.ErrNotExist; a directory, a
// dangling or looping link or a link out of dir wraps errSkillFileUnusable;
// any other error is a path pfm could not inspect.
func checkSkillFile(dir string) error {
	path := filepath.Join(dir, "SKILL.md")
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if info.Mode().Type() == fs.ModeSymlink {
		root, err := filepath.EvalSymlinks(dir)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", dir, err)
		}
		target, err := filepath.EvalSymlinks(path)
		var errno syscall.Errno
		if err != nil && errors.As(err, &errno) && errno != syscall.ENOENT {
			return fmt.Errorf("inspect %s: %w", path, err)
		} else if err != nil {
			return fmt.Errorf("%s: %v: %w", path, err, errSkillFileUnusable)
		}
		if !withinGlobalSource(target, root) {
			return fmt.Errorf("%s -> %s leaves %s: %w", path, target, root, errSkillFileUnusable)
		}
		if info, err = os.Lstat(target); err != nil {
			return fmt.Errorf("inspect %s: %w", target, err)
		}
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s (mode %s): %w", path, info.Mode().Type(), errSkillFileUnusable)
	}
	return nil
}

// linkableStore reports whether store holds a usable root SKILL.md; an
// existing store without one is one named skip telling absent
// (SKILL-SOURCE-MISSING), unusable (SKILL-SOURCE-INVALID) and uninspectable
// (SKILL-FETCH-FAILED) apart.
func (installer *engine) linkableStore(name, store string) bool {
	err := checkSkillFile(store)
	if err == nil {
		return true
	}
	if _, storeErr := os.Lstat(store); storeErr != nil {
		return false
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		installer.skip("SKILL-SOURCE-MISSING " + name + " (" + err.Error() + ")")
	case errors.Is(err, errSkillFileUnusable):
		installer.skip("SKILL-SOURCE-INVALID " + name + ": " + err.Error())
	default:
		installer.skip("SKILL-FETCH-FAILED " + name + ": inspect store: " + err.Error())
	}
	return false
}

// runSkillGit runs one git call outside every repository — its working
// directory is the store root's parent, and runSkillGitWith stops discovery
// there — under skillGitTimeout, returning trimmed stdout.
func (installer *engine) runSkillGit(args ...string) (string, error) {
	git, err := deps.Resolve("git")
	if err != nil {
		return "", fmt.Errorf("resolve git: %w", err)
	}
	dir := filepath.Dir(skillStoreRoot(installer.options.Home))
	return runSkillGitWith(installer.processRunner(), skillGitTimeout, skillGitWaitDelay, git, dir, args...)
}

// runSkillGitWith runs git in dir without the inherited variables that select
// a repository (skillGitRepoVars), with discovery stopped above dir
// (GIT_CEILING_DIRECTORIES) and credential prompts disabled — the overrides
// appended last, so each wins over an inherited value. WaitDelay bounds the
// wait for a grandchild (git-remote-https) still holding the output pipes
// after the timeout kills git; every failure carries git's stderr tail.
func runSkillGitWith(
	runner deps.Runner,
	timeout, waitDelay time.Duration,
	git, dir string,
	args ...string,
) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	env := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		if name, _, _ := strings.Cut(entry, "="); !skillGitRepoVars[name] {
			env = append(env, entry)
		}
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "SSH_ASKPASS=",
		"GIT_CEILING_DIRECTORIES="+filepath.Dir(dir))
	result, err := runner.Run(ctx, append([]string{git}, args...), deps.RunOptions{
		Dir: dir, Env: env, WaitDelay: waitDelay,
	})
	tail := strings.TrimSpace(string(result.Stderr))
	if lines := strings.Split(tail, "\n"); len(lines) > 3 {
		tail = strings.Join(lines[len(lines)-3:], "\n")
	}
	command := "git " + strings.Join(args, " ")
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "", fmt.Errorf("%s timed out after %s: %s", command, timeout, tail)
	case err != nil:
		return "", fmt.Errorf("%s: %w: %s", command, err, tail)
	case result.ExitCode != 0:
		return "", fmt.Errorf("%s: exit %d: %s", command, result.ExitCode, tail)
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// skillSourceLinkDirs names the store skills registry and ~/.agents/skills.
func (installer *engine) skillSourceLinkDirs() []string {
	return []string{
		filepath.Join(installer.options.ConfigDir, "skills"),
		filepath.Join(installer.options.Home, ".agents", "skills"),
	}
}

// retireSkillSources removes every link resolving into the skill store whose
// store name is not in active, then every store entry not in active (a
// leftover staging directory included), keeping an active store's commit
// record. Uninstall passes an empty set.
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
	kept := make(map[string]bool, 2*len(active))
	for name := range active {
		kept[name] = true
		kept[filepath.Base(skillCommitPath(filepath.Join(storeRoot, name)))] = true
	}
	for _, entry := range entries {
		if kept[entry.Name()] {
			continue
		}
		path := filepath.Join(storeRoot, entry.Name())
		installer.markRemoved(path)
		if err := installer.change(
			"retire "+path+" (unregistered source-fetched skill store)",
			func() error { return os.RemoveAll(path) },
		); err != nil {
			return err
		}
	}
	return nil
}

// unwireSkillSources is uninstall's half: every store link, the stores and
// their commit records. A store root that is not a real directory, or is held
// by an install, is a named skip, and the rest of the uninstall runs on.
func (installer *engine) unwireSkillSources() error {
	storeRoot := skillStoreRoot(installer.options.Home)
	unlock, busy, err := installer.lockSkillStore(storeRoot)
	if err != nil {
		installer.skip("SKILL-SOURCES-FAILED " + err.Error() + "; source-fetched skills left (rerun pfm uninstall)")
		return nil
	}
	if busy {
		installer.skip("SKILL-SOURCES-BUSY another pfm install holds " + storeRoot +
			"; source-fetched skills left (rerun pfm uninstall once it ends)")
		return nil
	}
	defer unlock()
	if _, err := checkSkillStoreRoot(storeRoot); err != nil {
		installer.skip("SKILL-SOURCES-FAILED " + err.Error() + " (remove it, then rerun pfm uninstall)")
		return nil
	}
	if err := installer.retireSkillSources(map[string]bool{}); err != nil {
		return err
	}
	return installer.retireEmptyDir(storeRoot)
}
