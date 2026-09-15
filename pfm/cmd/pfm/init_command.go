package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"hostops/pfm/internal/atomicfile"
	"hostops/pfm/internal/installer"
	"hostops/pfm/internal/professor"
)

const claudeInstructionsFile = "CLAUDE.md"

var initTemplatePaths = []struct {
	source string
	target string
	skip   string
}{
	{source: "project/CLAUDE.md", target: claudeInstructionsFile},
	{source: "project/settings.json", target: ".claude/settings.json"},
	{source: "project/rumdl-policy.toml", target: ".rumdl.toml"},
	{source: "project/commands", target: ".claude/commands"},
	{source: "project/agents", target: ".claude/agents", skip: "per-project"},
	{source: "project/scripts", target: ".claude/scripts"},
	{source: "project/skills", target: ".claude/skills"},
	{source: "project/epics", target: "docs/epics"},
	{source: "project/codex", target: ".codex"},
	{source: "project/docs-commands", target: "docs/commands"},
	{source: "project/docs-agents", target: "docs/agents"},
}

type initCopy struct {
	template string
	local    string
	source   string
	mode     os.FileMode
}

func runInit(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := newFlagSet(
		initCommand,
		"usage: pfm init [dir] [--force]",
		stderr,
	)
	force := flags.Bool("force", false, "overwrite colliding scaffold files")
	positional, code, ok := parseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(positional) > 1 {
		flags.Usage()
		return 2
	}
	target := "."
	if len(positional) == 1 {
		target = positional[0]
	}
	target, err := filepath.Abs(target)
	if err != nil {
		fmt.Fprintf(stderr, "pfm init: resolve target: %v\n", err)
		return 1
	}
	runtime, err := optionalCommandRuntime(runtimes)
	if err != nil {
		fmt.Fprintf(stderr, "pfm init: config: %v\n", err)
		return 1
	}
	source, err := installer.ReadSourceRepoMarker(runtime.Paths.Home)
	if err != nil {
		fmt.Fprintf(stderr, "pfm init: %v\n", err)
		return 1
	}
	deployed, err := initScaffold(source, target, *force, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "pfm init: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "initialized %s from %s\n", target, source)
	fmt.Fprintf(stdout, "deployed %d project files; baseline: %s\n", deployed, professor.BaselinePath(target))
	fmt.Fprintf(
		stdout,
		"open Claude here and follow %s § Install interview — it fills tokens and deploys per-project agents\n",
		filepath.Join(source, "docs", "SETUP.md"),
	)
	return 0
}

func initScaffold(source, target string, force bool, stdout io.Writer) (int, error) {
	store, err := professor.InspectStore(source)
	if err != nil {
		return 0, err
	}
	plan, err := planInitCopies(store)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(target, 0o700); err != nil {
		return 0, fmt.Errorf("create target %s: %w", target, err)
	}
	baseline := professor.Baseline{
		Version: professor.BaselineVersion,
		Blueprint: professor.BlueprintPin{
			Version: store.Version,
			SHA:     store.SHA,
		},
		Files: make(map[string]professor.FilePin),
	}
	pinnedAt := time.Now().Format(time.DateOnly)
	for _, entry := range plan {
		targetPath := filepath.Join(target, filepath.FromSlash(entry.local))
		if _, err := os.Stat(targetPath); err == nil && !force {
			fmt.Fprintf(stdout, "CONFLICT %s: exists\n", entry.local)
			continue
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return 0, fmt.Errorf("inspect target %s: %w", entry.local, err)
		}
		raw, err := os.ReadFile(entry.source)
		if err != nil {
			return 0, fmt.Errorf("read template %s: %w", entry.template, err)
		}
		raw = addScaffoldMarker(entry.local, entry.template, store.SHA, raw)
		if err := atomicfile.Write(targetPath, raw, entry.mode); err != nil {
			return 0, fmt.Errorf("deploy %s to %s: %w", entry.template, entry.local, err)
		}
		hash, err := professor.HashTemplate(entry.source)
		if err != nil {
			return 0, err
		}
		baseline.Files[entry.local] = professor.FilePin{
			Template:     entry.template,
			TemplateHash: hash,
			PinnedSHA:    store.SHA,
			PinnedAt:     pinnedAt,
		}
	}
	if err := professor.Save(target, baseline); err != nil {
		return 0, err
	}
	return len(baseline.Files), nil
}

func planInitCopies(store professor.Store) ([]initCopy, error) {
	plan := make([]initCopy, 0)
	for _, mapping := range initTemplatePaths {
		sourcePath := filepath.Join(store.Templates, filepath.FromSlash(mapping.source))
		info, err := os.Stat(sourcePath)
		if err != nil {
			return nil, fmt.Errorf("recorded clone template is missing %s: %w", mapping.source, err)
		}
		if !info.IsDir() {
			plan = append(plan, initCopy{
				template: filepath.ToSlash(mapping.source),
				local:    filepath.ToSlash(mapping.target),
				source:   sourcePath,
				mode:     info.Mode().Perm(),
			})
			continue
		}
		err = filepath.WalkDir(sourcePath, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(sourcePath, path)
			if err != nil {
				return err
			}
			if relative == "." {
				return nil
			}
			if mapping.skip != "" &&
				(relative == mapping.skip || strings.HasPrefix(relative, mapping.skip+string(filepath.Separator))) {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink template entry is not allowed: %s", path)
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			plan = append(plan, initCopy{
				template: filepath.ToSlash(filepath.Join(mapping.source, relative)),
				local:    filepath.ToSlash(filepath.Join(mapping.target, relative)),
				source:   path,
				mode:     info.Mode().Perm(),
			})
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("walk template %s: %w", mapping.source, err)
		}
	}
	sort.Slice(plan, func(i, j int) bool { return plan[i].local < plan[j].local })
	return plan, nil
}

func addScaffoldMarker(local, template, sha string, raw []byte) []byte {
	marker := fmt.Sprintf(
		"# pfm-scaffold: %s@%s — this file is YOURS; upstream deltas arrive via pfm update, reviewed and hand-applied\n",
		template,
		sha,
	)
	if local != claudeInstructionsFile && local != "AGENTS.md" && strings.HasSuffix(local, ".md") &&
		strings.HasPrefix(string(raw), "---\n") {
		return append(append([]byte("---\n"), []byte(marker)...), raw[len("---\n"):]...)
	}
	if strings.HasSuffix(local, ".sh") {
		if newline := strings.IndexByte(string(raw), '\n'); newline >= 0 && strings.HasPrefix(string(raw), "#!") {
			marked := make([]byte, 0, len(raw)+len(marker))
			marked = append(marked, raw[:newline+1]...)
			marked = append(marked, marker...)
			marked = append(marked, raw[newline+1:]...)
			return marked
		}
	}
	return raw
}

// discoverSourceRepo finds the clone when install is launched from it. It is
// deliberately filesystem-only: installation never shells out to git.
func discoverSourceRepo() string {
	if value := strings.TrimSpace(os.Getenv("PFM_SOURCE_REPO")); value != "" {
		if absolute, err := filepath.Abs(value); err == nil {
			return absolute
		}
	}
	current, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if isSourceRepo(current) {
			return mainWorktreeOf(current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// mainWorktreeOf maps a linked git worktree of the source clone to the clone
// itself. A worktree carries every file isSourceRepo checks, yet it is deleted
// when its wave merges — recording it would leave the source marker, `pfm
// update`, and the global links pointing at nothing. A linked worktree's .git
// is a FILE ("gitdir: <common>/worktrees/<name>") whose gitdir holds a
// commondir file naming the shared .git; that directory's parent is the main
// checkout. Every other shape — a real .git directory, a submodule's gitdir
// with no commondir, a main checkout that is not a source repo — returns root
// unchanged.
func mainWorktreeOf(root string) string {
	raw, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil {
		return root
	}
	gitdir, found := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir: ")
	if !found {
		return root
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}
	common, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		return root
	}
	commonDir := strings.TrimSpace(string(common))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitdir, commonDir)
	}
	main := filepath.Dir(filepath.Clean(commonDir))
	if !isSourceRepo(main) {
		return root
	}
	return main
}

func isSourceRepo(root string) bool {
	for _, relative := range []string{claudeInstructionsFile, "AGENTS.md", ".claude/settings.json"} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			return false
		}
	}
	return true
}
