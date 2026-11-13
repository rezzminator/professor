// Package workbench owns the nested sub-project a managed repository holds: its manifest, discovery, which workbench owns a directory, and the persona a launch there carries.
package workbench

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gitroot"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Bench retains its identity even when its manifest is invalid.
type Bench struct {
	Dir, Root, Project, Title, Prefix, Key string
	Prompt                                 string // Absolute prompt path.
	Engines                                []pfmengine.ID
	Effort, Model                          string
	Err                                    error
}

type manifest struct {
	Prompt  string   `json:"prompt"`
	Engines []string `json:"engines"`
	Title   *string  `json:"title"`
	Name    *string  `json:"name"`
	Effort  string   `json:"effort"`
	Model   string   `json:"model"`
}

// LoadBench reads and validates one manifest without caching its prompt or metadata.
func LoadBench(dir, root string) Bench {
	bench := Bench{
		Dir: filepath.Clean(dir), Root: filepath.Clean(root),
		Title: filepath.Base(dir),
	}
	_, bench.Project = gitroot.Project(bench.Root)
	bench.Key = bench.Project + " › " + bench.Title
	path := paths.WorkbenchManifest(bench.Dir)
	fault := func(err error) Bench {
		bench.Err = fmt.Errorf("%s: %w", path, err)
		obs.Logger(context.Background()).Error("workbench manifest", "path", path, obs.FieldErr, bench.Err)
		return bench
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fault(fmt.Errorf("read: %w", err))
	}
	var config manifest
	if err := pfmconfig.DecodeStrict(raw, &config, "trailing JSON value"); err != nil {
		return fault(fmt.Errorf("not valid: %w", err))
	}
	if config.Title != nil && *config.Title != "" {
		bench.Title = *config.Title
		bench.Key = bench.Project + " › " + bench.Title
	}
	if config.Prompt == "" {
		return fault(errors.New(`"prompt" is required`))
	}
	promptDir := filepath.Dir(path)
	prompt := filepath.Join(promptDir, config.Prompt)
	if filepath.IsAbs(config.Prompt) || !insideDirectory(promptDir, prompt) ||
		!insideDirectory(paths.PhysicalPath(promptDir), paths.PhysicalPath(prompt)) {
		return fault(fmt.Errorf(`"prompt" must name a file inside .professor/ (got %q)`, config.Prompt))
	}
	info, err := os.Stat(prompt)
	if errors.Is(err, fs.ErrNotExist) {
		return fault(fmt.Errorf("prompt file %s is missing", prompt))
	}
	if err != nil {
		return fault(fmt.Errorf("prompt file %s: %w", prompt, err))
	}
	if info.IsDir() {
		return fault(fmt.Errorf("prompt file %s is a directory", prompt))
	}
	if !info.Mode().IsRegular() {
		return fault(fmt.Errorf("prompt file %s is not a regular file", prompt))
	}
	if info.Size() == 0 {
		return fault(fmt.Errorf("prompt file %s is empty", prompt))
	}
	bench.Prompt = prompt
	words := config.Engines
	if words == nil {
		words = []string{pfmengine.MustLookup(pfmengine.Claude).LongName}
	}
	if len(words) == 0 {
		return fault(
			fmt.Errorf(`"engines" is empty; omit it for [%q]`, pfmengine.MustLookup(pfmengine.Claude).LongName),
		)
	}
	seen := make(map[pfmengine.ID]bool)
	for _, word := range words {
		var id pfmengine.ID
		for _, candidate := range []pfmengine.ID{pfmengine.Claude, pfmengine.Codex, pfmengine.OpenCode} {
			if word == pfmengine.MustLookup(candidate).LongName {
				id = candidate
				break
			}
		}
		if id == "" {
			return fault(fmt.Errorf(`"engines" names unknown engine %q; allowed: claude, codex, opencode`, word))
		}
		if seen[id] {
			return fault(fmt.Errorf(`"engines" lists %q twice`, word))
		}
		seen[id] = true
		bench.Engines = append(bench.Engines, id)
	}
	bench.Prefix = naming.WorkbenchPrefix(bench.Title)
	if config.Name != nil {
		if *config.Name == "" || strings.ContainsRune(*config.Name, ':') ||
			strings.IndexFunc(*config.Name, unicode.IsSpace) >= 0 {
			return fault(fmt.Errorf(`"name" must be one word without ":" (got %q)`, *config.Name))
		}
		bench.Prefix = *config.Name
	}
	bench.Effort, bench.Model = config.Effort, config.Model
	return bench
}

// Enables reports whether an engine occurs in the manifest's ordered list.
func (bench Bench) Enables(engine pfmengine.ID) bool {
	for _, enabled := range bench.Engines {
		if engine == enabled {
			return true
		}
	}
	return false
}

// settingsPromptFile is the adopter's prompt file: relative to root, inside
// it with links resolved, a non-empty regular file.
func settingsPromptFile(root, relative string) (path, body string, err error) {
	path = filepath.Join(root, relative)
	if filepath.IsAbs(relative) || !insideDirectory(root, path) {
		return "", "", fmt.Errorf("%q must be a path inside %s, relative to it", relative, root)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve %s: %w", root, err)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", "", fmt.Errorf("resolve %s: %w", path, err)
	}
	if !insideDirectory(resolvedRoot, resolved) {
		return "", "", fmt.Errorf("%s resolves to %s, outside %s", path, resolved, root)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", "", fmt.Errorf("inspect %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("%s is not a regular file", path)
	}
	raw, err := os.ReadFile(resolved)
	if err != nil {
		return "", "", fmt.Errorf("read %s: %w", path, err)
	}
	if len(raw) == 0 {
		return "", "", fmt.Errorf("%s is empty", path)
	}
	return path, string(raw), nil
}

// composeExtend writes engine's fleet prompt, one blank line, then the
// adopter's body to paths.SettingsPrompt, rewritten only when it differs.
func composeExtend(root, home string, engine pfmengine.ID, body string) (path, composed string, err error) {
	fleetPath, err := paths.GeneratedHarnessPrompt(home, engine)
	if err != nil {
		return "", "", err
	}
	fleet, err := os.ReadFile(fleetPath)
	if err != nil {
		return "", "", fmt.Errorf("read the fleet prompt: %w", err)
	}
	composed = strings.TrimRight(string(fleet), "\n") + "\n\n" + body
	path = paths.SettingsPrompt(home, root, engine)
	if current, err := os.ReadFile(path); err == nil && string(current) == composed {
		return path, composed, nil
	}
	if err := atomicfile.Write(path, []byte(composed), 0o600); err != nil {
		return "", "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, composed, nil
}

func insideDirectory(root, dir string) bool {
	relative, err := filepath.Rel(root, dir)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// readDir is swapped by tests to provoke errors a root-run fence cannot.
var readDir = os.ReadDir

// skippedUnreadable: a permission denial below the root holds no bench of this user, so it is no fault.
func skippedUnreadable(root, dir string, err error) bool {
	if dir == root || !errors.Is(err, fs.ErrPermission) {
		return false
	}
	obs.Logger(context.Background()).
		Info("workbench discovery skipped unreadable directory", "path", dir, obs.FieldErr, err)
	return true
}
