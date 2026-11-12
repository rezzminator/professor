package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/professor"
)

// Mode distinguishes a new launch from returning to an existing chat.
type Mode int

const (
	New Mode = iota
	Resume
)

// Persona carries a validated workbench and the prompt read for this launch.
// Envs are the managed root's settings envs, set on the launch whatever prompt
// it carries; Warnings name a settings file the launch goes on without.
type Persona struct {
	Bench                       Bench
	Prompt, Body, Effort, Model string
	Envs                        map[string]string
	Warnings                    []string
}

// ForLaunch refuses invalid manifests and disabled new engines before launch.
// The managed root's settings file (paths.SettingsFile) supplies the prompt
// where no workbench owns cwd, and its envs everywhere; a faulty settings file
// is a warning, never a refusal. A refusal still returns the envs and warnings.
func ForLaunch(cwd, home string, engine pfmengine.ID, mode Mode) (Persona, error) {
	start, root, found, err := launchRoot(cwd)
	if err != nil || !found {
		return Persona{}, err
	}
	bench, found, err := nearestFrom(start, root)
	// A nested workbench's prompt wins, so the settings prompt is resolved
	// only where no workbench owns cwd.
	settings := settingsPersona(root, home, engine, err == nil && !found)
	base := Persona{Envs: settings.Envs, Warnings: settings.Warnings}
	if err != nil {
		return base, err
	}
	if !found {
		return settings, nil
	}
	if bench.Err != nil {
		return base, bench.Err
	}
	if !bench.Enables(engine) {
		if mode == Resume {
			return base, nil
		}
		word := pfmengine.MustLookup(engine).LongName
		err := fmt.Errorf("workbench %s does not enable %s: add %q to %q in %s",
			bench.Dir, word, word, "engines", paths.WorkbenchManifest(bench.Dir))
		obs.Logger(context.Background()).
			Error("workbench launch", "path", bench.Dir, obs.FieldEngine, string(engine), obs.FieldErr, err)
		return base, err
	}
	body, err := os.ReadFile(bench.Prompt)
	if err != nil {
		wrapped := fmt.Errorf("read workbench prompt %s: %w", bench.Prompt, err)
		obs.Logger(context.Background()).Error("workbench launch", "path", bench.Prompt, obs.FieldErr, wrapped)
		return base, wrapped
	}
	return Persona{
		Bench:    bench,
		Prompt:   bench.Prompt,
		Body:     string(body),
		Effort:   bench.Effort,
		Model:    bench.Model,
		Envs:     settings.Envs,
		Warnings: settings.Warnings,
	}, nil
}

// launchRoot is the managed root above cwd, in a linked worktree the
// worktree's own copy of it, and the directory a bench walk starts from.
func launchRoot(cwd string) (start, root string, found bool, err error) {
	dirs, err := launchDirectories(cwd)
	if err != nil {
		return "", "", false, err
	}
	root, found, err = professor.ResolveProjectRoot(dirs.mapped)
	if err != nil {
		obs.Logger(context.Background()).Error("workbench managed root", "path", dirs.mapped, obs.FieldErr, err)
		return "", "", false, err
	}
	if !found {
		return "", "", false, nil
	}
	start = dirs.mapped
	if dirs.top != "" && insideDirectory(dirs.main, root) {
		relative, err := filepath.Rel(dirs.main, root)
		if err != nil {
			wrapped := fmt.Errorf("place managed root %s in worktree %s: %w", root, dirs.top, err)
			obs.Logger(context.Background()).Error("workbench nearest", "path", dirs.own, obs.FieldErr, wrapped)
			return "", "", false, wrapped
		}
		start, root = dirs.own, filepath.Join(dirs.top, relative)
	}
	return start, root, true, nil
}

// settingsFile is the settings file's one shape: no other key exists.
type settingsFile struct {
	HarnessPrompt *struct {
		Extend  string `json:"extend"`
		Replace string `json:"replace"`
	} `json:"harnessPrompt"`
	Envs json.RawMessage `json:"envs"`
}

// settingsPersona is root's settings file as a persona; a fault is logged and
// named in Warnings, and the launch goes on without the file.
func settingsPersona(root, home string, engine pfmengine.ID, withPrompt bool) Persona {
	persona, err := readRootSettings(root, home, engine, withPrompt)
	if err != nil {
		obs.Logger(context.Background()).Error("pfm settings", "path", paths.Settings(root), obs.FieldErr, err)
		return Persona{Warnings: []string{err.Error() + "; launching without the settings"}}
	}
	return persona
}

// settingsEnvReserved reports a name pfm assigns on some launch door,
// several of them by inheritance (cx, the Herdr seat), where an adopter value
// would land ahead of pfm's.
func settingsEnvReserved(name string) bool {
	return strings.HasPrefix(name, "PFM_") || name == "CODEX_HOME" || name == "CLAUDE_CONFIG_DIR"
}

// readRootSettings reads root's settings file; withPrompt false checks the
// harnessPrompt shape but resolves no prompt file.
func readRootSettings(root, home string, engine pfmengine.ID, withPrompt bool) (Persona, error) {
	path := paths.Settings(root)
	fault := func(err error) (Persona, error) { return Persona{}, fmt.Errorf("%s: %w", path, err) }
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Persona{}, nil
	}
	if err != nil {
		return fault(fmt.Errorf("inspect: %w", err))
	}
	// A FIFO would hang every launch; a link could leave the root.
	if !info.Mode().IsRegular() {
		return fault(errors.New("not a regular file"))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fault(fmt.Errorf("read: %w", err))
	}
	var file settingsFile
	if err := pfmconfig.DecodeStrict(raw, &file, "trailing JSON value"); err != nil {
		return fault(fmt.Errorf("not valid: %w", err))
	}
	var persona Persona
	if len(file.Envs) != 0 && !bytes.Equal(bytes.TrimSpace(file.Envs), []byte("null")) {
		envs, err := pfmconfig.DecodeEnv("envs", file.Envs)
		if err != nil {
			return fault(err)
		}
		for _, name := range slices.Sorted(maps.Keys(envs)) {
			if settingsEnvReserved(name) {
				return fault(fmt.Errorf("envs.%s is set by pfm itself on launch", name))
			}
			if strings.ContainsRune(envs[name], 0) {
				return fault(fmt.Errorf("envs.%s must not contain NUL", name))
			}
		}
		persona.Envs = envs
	}
	if file.HarnessPrompt == nil {
		return persona, nil
	}
	key, relative := "extend", file.HarnessPrompt.Extend
	switch {
	case relative != "" && file.HarnessPrompt.Replace != "":
		return fault(errors.New(`harnessPrompt sets both "extend" and "replace": keep one`))
	case relative == "" && file.HarnessPrompt.Replace == "":
		return fault(errors.New(`harnessPrompt needs "extend" or "replace"`))
	case relative == "":
		key, relative = "replace", file.HarnessPrompt.Replace
	}
	if !withPrompt {
		return persona, nil
	}
	prompt, body, err := settingsPromptFile(root, relative)
	if err != nil {
		return fault(fmt.Errorf("harnessPrompt.%s: %w", key, err))
	}
	if key == "replace" {
		persona.Prompt, persona.Body = prompt, body
		return persona, nil
	}
	persona.Prompt, persona.Body, err = composeExtend(root, home, engine, body)
	if err != nil {
		return fault(fmt.Errorf("harnessPrompt.extend: %w", err))
	}
	return persona, nil
}

// Applies reports whether this launch carries a workbench prompt.
func (persona Persona) Applies() bool { return persona.Prompt != "" }

// EffortOr keeps an explicit effort ahead of the manifest's value.
func (persona Persona) EffortOr(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return persona.Effort
}

// ModelOr keeps an explicit model ahead of the manifest's value.
func (persona Persona) ModelOr(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return persona.Model
}
