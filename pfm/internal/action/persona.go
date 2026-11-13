package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

// applyWorkbench prepares the persona before either executor door synthesizes
// a launch, and returns what the launch dropped of it (LaunchPersona,
// PersonaMirror): the chat opens anyway.
func applyWorkbench(request *Request, engineSync deps.Runner) ([]string, error) {
	// A booting row attaches to a seat already launched: nothing relaunches.
	if request.Row.Kind.IsLiveSeat() || request.Row.Kind == compose.Booting {
		return nil, nil
	}
	engine, err := compose.EngineForKindChecked(request.Row.Kind)
	if err != nil {
		return nil, err
	}
	mode := workbench.Resume
	switch request.Row.Kind {
	case compose.NewClaude, compose.NewCodex, compose.NewOpenCode:
		mode = workbench.New
	}
	persona, warnings, err := LaunchPersona(request.Row.CWD, request.Home, engine, mode)
	if err != nil {
		return nil, err
	}
	request.Persona = persona
	request.OpenCodePlugin = ""
	request.OpenCodeHerdrPlugin = ""
	request.OpenCodeFleetPrompt = ""
	if engine == pfmengine.OpenCode {
		// The Herdr reporter is optional: a staging failure is logged and the
		// chat launches without it, never fails.
		path := paths.OpenCodeHerdrPlugin(request.Home)
		if err := workbench.StageOpenCodeHerdrPlugin(
			path,
			filepath.Join(request.Home, ".local", "bin", "pfm"),
		); err != nil {
			obs.Logger(context.Background()).Error(
				"OpenCode chat launches without the Herdr reporter plugin",
				"path", path, obs.FieldErr, err,
			)
		} else {
			request.OpenCodeHerdrPlugin = path
		}
	}
	if !persona.Applies() {
		return warnings, nil
	}
	// A new Codex chat runs the user's own cx, whose launcher builds the mirror.
	if request.Row.Kind == compose.ResumeCodex || engine == pfmengine.OpenCode {
		warnings = append(warnings, PersonaMirror(persona, engine, request.Home, engineSync)...)
	}
	if engine == pfmengine.OpenCode {
		if err := attachOpenCodeSeatPlugin(request); err != nil {
			return nil, err
		}
		fleet, err := paths.GeneratedHarnessPrompt(request.Home, pfmengine.OpenCode)
		if err != nil {
			if !errors.Is(err, paths.ErrNoSourceRepoMarker) && !errors.Is(err, paths.ErrSourceRepoUnusable) {
				wrapped := fmt.Errorf("resolve the fleet prompt an OpenCode workbench launch replaces: %w", err)
				obs.Logger(context.Background()).Error("OpenCode workbench fleet prompt", obs.FieldErr, wrapped)
				return nil, wrapped
			}
		} else {
			request.OpenCodeFleetPrompt = fleet
		}
	}
	return warnings, nil
}

// LaunchPersona is every launch door's workbench persona read. A persona that
// cannot resolve never refuses the launch: an unknown effort word launches the
// persona on the engine's default effort, any other fault launches without the
// persona, each named in the returned warnings after the settings file's own
// (workbench.Persona.Warnings); the settings envs ride every outcome. A
// workbench that does not enable the engine stays the refusal of a new
// launch, naming its fix.
func LaunchPersona(cwd, home string, engine pfmengine.ID, mode workbench.Mode) (workbench.Persona, []string, error) {
	raw, err := workbench.ForLaunch(cwd, home, engine, mode)
	if err == nil {
		persona, effortErr := personaEffort(raw, engine)
		if effortErr == nil {
			return persona, persona.Warnings, nil
		}
		raw.Effort = ""
		return raw, append(raw.Warnings, effortErr.Error()+"; launching the persona on the default effort"), nil
	}
	if bench, found, benchErr := workbench.Nearest(cwd); benchErr == nil && found && bench.Err == nil &&
		!bench.Enables(engine) {
		return workbench.Persona{}, nil, err
	}
	return workbench.Persona{Envs: raw.Envs}, append(raw.Warnings,
		"workbench persona not applied ("+err.Error()+"); launching without it",
	), nil
}

// PersonaMirror builds the persona's engine mirror before a launch. A build
// that fails launches on the mirror as it stands, named in the returned warning.
// engineSync runs the engine-sync child; nil is the real process table.
func PersonaMirror(persona workbench.Persona, engine pfmengine.ID, home string, engineSync deps.Runner) []string {
	if !persona.Applies() {
		return nil
	}
	if err := workbench.EnsureMirror(persona.Bench, engine, home, engineSync); err != nil {
		return []string{err.Error() + "; launching on the mirror as it stands"}
	}
	return nil
}

// WorkbenchPersona is workbench.ForLaunch with the persona's effort validated
// through engine's one effort roster and carried lower-cased, so every launch
// door accepts what chat new and reload accept.
func WorkbenchPersona(cwd, home string, engine pfmengine.ID, mode workbench.Mode) (workbench.Persona, error) {
	persona, err := workbench.ForLaunch(cwd, home, engine, mode)
	if err != nil {
		return workbench.Persona{}, err
	}
	return personaEffort(persona, engine)
}

// personaEffort is persona with its effort word validated through engine's
// one effort roster and carried lower-cased.
func personaEffort(persona workbench.Persona, engine pfmengine.ID) (workbench.Persona, error) {
	var err error
	switch engine {
	case pfmengine.Claude:
		persona.Effort, err = ClaudeEffort(persona.Effort)
	case pfmengine.Codex:
		persona.Effort, err = CodexEffort(persona.Effort)
	case pfmengine.OpenCode:
		persona.Effort, err = OpenCodeEffort(persona.Effort)
	}
	if err != nil {
		wrapped := fmt.Errorf("workbench %s: %w", persona.Bench.Dir, err)
		obs.Logger(context.Background()).Error("workbench effort", "path", persona.Bench.Dir, obs.FieldErr, wrapped)
		return workbench.Persona{}, wrapped
	}
	return persona, nil
}

// openCodePersonaEnv is the environment an OpenCode launch adds: the plugin
// list of OPENCODE_CONFIG_CONTENT — the workbench seat plugin for a persona,
// the Herdr reporter plugin for every chat — and, for a persona, its system
// and fleet prompt files.
func openCodePersonaEnv(request Request) (string, error) {
	assignments, err := openCodePersonaAssignments(request)
	if err != nil {
		return "", err
	}
	var command strings.Builder
	writeAssignments(&command, assignments)
	return command.String(), nil
}

func attachOpenCodeSeatPlugin(request *Request) error {
	path := paths.OpenCodeWorkbenchPlugin(request.Home)
	if err := workbench.StageOpenCodeSeatPlugin(path); err != nil {
		return err
	}
	request.OpenCodePlugin = path
	return nil
}

// openCodePersonaAssignments is openCodePersonaEnv's assignments behind the
// adopter settings' envs (claudelaunch.WithEnvs).
func openCodePersonaAssignments(request Request) ([]string, error) {
	hasPersona := request.Persona.Prompt != ""
	var plugins []string
	if request.OpenCodePlugin != "" {
		plugins = append(plugins, (&url.URL{Scheme: "file", Path: request.OpenCodePlugin}).String())
	}
	if request.OpenCodeHerdrPlugin != "" {
		plugins = append(plugins, (&url.URL{Scheme: "file", Path: request.OpenCodeHerdrPlugin}).String())
	}
	effort, err := OpenCodeEffort(request.Persona.Effort)
	if err != nil {
		return nil, err
	}
	model := request.Persona.Model
	if model != "" {
		provider, name, found := strings.Cut(model, "/")
		if !found || provider == "" || name == "" || hasNUL(model) {
			return nil, fmt.Errorf("OpenCode model %q must be provider/model", model)
		}
	}
	if len(plugins) == 0 && model == "" && effort == "" && request.OpenCodePromptChannel == "" {
		return claudelaunch.WithEnvs(request.Persona.Envs, nil), nil
	}
	if (model != "" || effort != "" || request.OpenCodePromptChannel != "") && request.OpenCodePlugin == "" {
		return nil, errors.New("OpenCode model, effort and prompt channel require the native seat plugin")
	}
	config := map[string]any{"plugin": plugins}
	if hasPersona {
		config["instructions"] = []string{request.Persona.Prompt}
	}
	body, err := json.Marshal(config)
	if err != nil {
		obs.Logger(context.Background()).Error("OpenCode launch config", obs.FieldErr, err)
		return nil, err
	}
	env := []string{"OPENCODE_CONFIG_CONTENT=" + string(body)}
	for _, pair := range [][2]string{
		{"PFM_OPENCODE_MODEL", model},
		{"PFM_OPENCODE_VARIANT", effort},
		{"PFM_OPENCODE_POLICY", request.OpenCodePromptChannel},
	} {
		if pair[1] != "" {
			env = append(env, pair[0]+"="+pair[1])
		}
	}
	if !hasPersona {
		return claudelaunch.WithEnvs(request.Persona.Envs, env), nil
	}
	if request.OpenCodeFleetPrompt != "" {
		env = append(env, "PFM_OPENCODE_FLEET_FILE="+request.OpenCodeFleetPrompt)
	}
	env = append(env, "PFM_OPENCODE_SYSTEM_FILE="+request.Persona.Prompt)
	return claudelaunch.WithEnvs(request.Persona.Envs, env), nil
}

func openCodeSeatRun(request Request, machine pfmconfig.Config, resume bool) (*fleetdb.SeatRun, error) {
	assignments, err := openCodePersonaAssignments(request)
	if err != nil {
		return nil, err
	}
	args := []string{}
	if resume {
		args = append(args, "--session", request.Row.ID)
	}
	args = append(args, request.Row.CWD)
	if request.Prompt != "" {
		args = append(args, "--prompt", request.Prompt)
	}
	launch := claudelaunch.Launch{Env: assignments, Unset: opencodeHygieneNames}
	return &fleetdb.SeatRun{
		Binary: binaryWord(machine.OpenCode.Binary, pfmengine.MustLookup(pfmengine.OpenCode).Binary, false),
		Args:   args,
		Env:    claudelaunch.RunEnvironment(os.Environ(), launch),
		CWD:    request.Row.CWD,
	}, nil
}
