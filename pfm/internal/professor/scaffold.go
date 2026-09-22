package professor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// ClaudeInstructionsFile is the project instructions file scaffolded from the blueprint.
const ClaudeInstructionsFile = "CLAUDE.md"

var initTemplatePaths = []struct {
	source string
	target string
	skip   string
}{
	{source: "project/CLAUDE.md", target: ClaudeInstructionsFile},
	{source: "project/settings.json", target: ".claude/settings.json"},
	{source: "project/rumdl-policy.toml", target: ".rumdl.toml"},
	{source: "project/commands", target: ".claude/commands", skip: "per-project"},
	{source: "project/agents", target: ".claude/agents"},
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

// Scaffold deploys the blueprint's mapped project templates and pins exactly the deployed set.
func Scaffold(source, target string, force bool, stdout io.Writer) (count int, err error) {
	trail := obs.NewTrail(context.Background(), "professor", "requested")
	defer func() { trail.End(err) }()
	store, err := InspectStore(source)
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
	baseline := Baseline{
		Version: BaselineVersion,
		Blueprint: BlueprintPin{
			Version: store.Version,
			SHA:     store.SHA,
		},
		Files: make(map[string]FilePin),
	}
	pinnedAt := clock.Real.Now().Format(time.DateOnly)
	for _, entry := range plan {
		targetPath := filepath.Join(target, filepath.FromSlash(entry.local))
		if _, err := os.Stat(targetPath); err == nil && !force {
			fmt.Fprintf(stdout, "CONFLICT %s: exists\n", entry.local)
			continue
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return persistScaffoldFailure(target, baseline, fmt.Errorf("inspect target %s: %w", entry.local, err))
		}
		raw, err := os.ReadFile(entry.source)
		if err != nil {
			return persistScaffoldFailure(target, baseline, fmt.Errorf("read template %s: %w", entry.template, err))
		}
		raw = addScaffoldMarker(entry.local, entry.template, store.SHA, raw)
		if err := atomicfile.Write(targetPath, raw, entry.mode); err != nil {
			return persistScaffoldFailure(
				target,
				baseline,
				fmt.Errorf("deploy %s to %s: %w", entry.template, entry.local, err),
			)
		}
		hash, err := HashTemplate(entry.source)
		if err != nil {
			return persistScaffoldFailure(target, baseline, err)
		}
		baseline.Files[entry.local] = FilePin{
			Template: entry.template, TemplateHash: hash, PinnedSHA: store.SHA, PinnedAt: pinnedAt,
		}
	}
	if err := Save(target, baseline); err != nil {
		return 0, err
	}
	trail.Reach("scaffolded", "files pinned")
	return len(baseline.Files), nil
}

// persistScaffoldFailure saves the pins for every entry a failed Scaffold
// pass already wrote durably to disk before returning cause (L3-F8): without
// this, a per-entry failure orphaned earlier entries from the baseline
// forever — a retry (`pfm init --force`) would hit `CONFLICT … exists` on
// them and they would never be pinned, so `pfm update check` reported them
// NEW forever with no hint they were pfm's own output. cause is always what
// is returned; a Save failure here is reported alongside it, never in place
// of it.
func persistScaffoldFailure(target string, baseline Baseline, cause error) (int, error) {
	if len(baseline.Files) == 0 {
		return 0, cause
	}
	if err := Save(target, baseline); err != nil {
		return 0, fmt.Errorf("%w (also failed to persist pins for already-written files: %v)", cause, err)
	}
	return 0, cause
}

func planInitCopies(store Store) ([]initCopy, error) {
	plan := make([]initCopy, 0)
	for _, mapping := range initTemplatePaths {
		sourcePath := filepath.Join(store.Templates, filepath.FromSlash(mapping.source))
		info, err := os.Stat(sourcePath)
		if err != nil {
			return nil, fmt.Errorf("recorded clone template is missing %s: %w", mapping.source, err)
		}
		if !info.IsDir() {
			plan = append(
				plan,
				initCopy{
					template: filepath.ToSlash(mapping.source),
					local:    filepath.ToSlash(mapping.target),
					source:   sourcePath,
					mode:     info.Mode().Perm(),
				},
			)
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
			plan = append(
				plan,
				initCopy{
					template: filepath.ToSlash(filepath.Join(mapping.source, relative)),
					local:    filepath.ToSlash(filepath.Join(mapping.target, relative)),
					source:   path,
					mode:     info.Mode().Perm(),
				},
			)
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
	if local != ClaudeInstructionsFile && local != "AGENTS.md" && strings.HasSuffix(local, ".md") &&
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
