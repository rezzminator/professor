package workbench

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
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

// Persona carries a validated workbench and the prompt read for this launch. Envs are the layered envs of
// pfm.config.json, machine then project, set on the launch whatever prompt it carries, and ClaudeSettings its
// layered claude.settings, laid over a Claude launch's own --settings; Warnings name a config file the launch
// goes on without and a project key it does not read.
type Persona struct {
	Bench                       Bench
	Prompt, Body, Effort, Model string
	Envs                        map[string]string
	ClaudeSettings              map[string]any
	Warnings                    []string
}

// ForLaunch refuses invalid manifests and disabled new engines before launch.
// pfm.config.json, the machine file and the managed root's (settingsPersona), supplies the prompt where no
// workbench owns cwd, and its envs and Claude settings everywhere, outside a managed root too; a faulty file
// is a warning, never a refusal. A refusal still returns the envs, Claude settings and warnings.
func ForLaunch(cwd, home string, engine pfmengine.ID, mode Mode) (Persona, error) {
	start, root, found, err := launchRoot(cwd)
	if err != nil {
		return Persona{}, err
	}
	if !found {
		return settingsPersona("", home, engine, true), nil
	}
	bench, found, err := nearestFrom(start, root)
	// A nested workbench's prompt wins, so the settings prompt is resolved
	// only where no workbench owns cwd.
	settings := settingsPersona(root, home, engine, err == nil && !found)
	base := Persona{Envs: settings.Envs, ClaudeSettings: settings.ClaudeSettings, Warnings: settings.Warnings}
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
	base.Bench, base.Prompt, base.Body = bench, bench.Prompt, string(body)
	base.Effort, base.Model = bench.Effort, bench.Model
	return base, nil
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

// settingsPersona is the scope keys of pfm.config.json as a persona: the machine file's, then the
// project's at root ("" outside a managed root) over them, envs by name, claude.settings by key
// (claudelaunch.LayerSettings), the project's harnessPrompt replacing the machine's; withPrompt false
// checks the harnessPrompt shape but resolves no prompt file. A faulty file is logged and named in
// Warnings, and the launch goes on without it; a project key the project scope does not read is a
// warning too.
func settingsPersona(root, home string, engine pfmengine.ID, withPrompt bool) Persona {
	var persona Persona
	fault := func(err error) {
		obs.Logger(context.Background()).Error("pfm config scope", "root", root, obs.FieldErr, err)
		persona.Warnings = append(persona.Warnings, err.Error()+"; launching without that file")
	}
	var machine, project pfmconfig.Scope
	machinePath, err := pfmconfig.MachinePath(home)
	switch {
	case errors.Is(err, paths.ErrNoSourceRepoMarker), err == nil && machinePath == "":
		// No source clone recorded, or a runtime loaded without a file: there is no machine file to read.
	case err != nil:
		fault(err)
	default:
		if machine, err = pfmconfig.ReadMachineScope(machinePath); err != nil {
			fault(err)
		}
	}
	if root != "" {
		scope, warnings, err := pfmconfig.ReadProjectScope(root, machinePath)
		for _, warning := range warnings {
			obs.Logger(context.Background()).Warn("pfm config scope", "path", root, "warning", warning)
		}
		persona.Warnings = append(persona.Warnings, warnings...)
		if err != nil {
			fault(err)
		} else {
			project = scope
		}
	}
	// The project's prompt wins; a prompt that cannot be read drops its whole file (envs and
	// claude.settings with it, as the warning says), so the machine's prompt applies.
	for _, scope := range []*pfmconfig.Scope{&project, &machine} {
		if scope.HarnessPrompt == nil || !withPrompt {
			continue
		}
		prompt := *scope.HarnessPrompt
		path, body, err := settingsPromptFile(prompt.Dir, prompt.Relative)
		if err == nil && prompt.Key == "extend" {
			path, body, err = composeExtend(prompt.Dir, home, engine, body)
		}
		if err != nil {
			fault(fmt.Errorf("config %s: harnessPrompt.%s: %w", scope.Path, prompt.Key, err))
			*scope = pfmconfig.Scope{}
			continue
		}
		persona.Prompt, persona.Body = path, body
		break
	}
	if machine.Envs != nil || project.Envs != nil {
		persona.Envs = make(map[string]string, len(machine.Envs)+len(project.Envs))
		maps.Copy(persona.Envs, machine.Envs)
		maps.Copy(persona.Envs, project.Envs)
	}
	if persona.ClaudeSettings, err = claudelaunch.LayerSettings(
		machine.ClaudeSettings, project.ClaudeSettings,
	); err != nil {
		fault(fmt.Errorf("layer claude.settings: %w", err))
	}
	return persona
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
