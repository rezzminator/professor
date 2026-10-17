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
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

// applyWorkbench prepares the persona before either executor door synthesizes a launch.
func applyWorkbench(request *Request) error {
	// A booting row attaches to a seat already launched: nothing relaunches.
	if request.Row.Kind.IsLiveSeat() || request.Row.Kind == compose.Booting {
		return nil
	}
	engine, err := compose.EngineForKindChecked(request.Row.Kind)
	if err != nil {
		return err
	}
	mode := workbench.Resume
	switch request.Row.Kind {
	case compose.NewClaude, compose.NewCodex, compose.NewOpenCode:
		mode = workbench.New
	}
	persona, err := WorkbenchPersona(request.Row.CWD, engine, mode)
	if err != nil {
		return err
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
		return nil
	}
	if request.Row.Kind == compose.ResumeCodex || engine == pfmengine.OpenCode {
		if err := workbench.EnsureMirror(persona.Bench, engine, request.Home); err != nil {
			return err
		}
	}
	if engine == pfmengine.OpenCode {
		path := paths.OpenCodeWorkbenchPlugin(request.Home)
		if err := workbench.StageOpenCodeSeatPlugin(path); err != nil {
			return err
		}
		request.OpenCodePlugin = path
		fleet, err := paths.ComposedHarnessPrompt(request.Home, pfmengine.OpenCode)
		if err != nil {
			if !errors.Is(err, paths.ErrNoSourceRepoMarker) && !errors.Is(err, paths.ErrSourceRepoUnusable) {
				wrapped := fmt.Errorf("resolve the fleet prompt an OpenCode workbench launch replaces: %w", err)
				obs.Logger(context.Background()).Error("OpenCode workbench fleet prompt", obs.FieldErr, wrapped)
				return wrapped
			}
		} else {
			request.OpenCodeFleetPrompt = fleet
		}
	}
	return nil
}

// WorkbenchPersona is workbench.ForLaunch with the persona's effort validated
// through engine's one effort roster and carried lower-cased, so every launch
// door accepts what chat new and reload accept. OpenCode carries no effort.
func WorkbenchPersona(cwd string, engine pfmengine.ID, mode workbench.Mode) (workbench.Persona, error) {
	persona, err := workbench.ForLaunch(cwd, engine, mode)
	if err != nil {
		return workbench.Persona{}, err
	}
	switch engine {
	case pfmengine.Claude:
		persona.Effort, err = ClaudeEffort(persona.Effort)
	case pfmengine.Codex:
		persona.Effort, err = CodexEffort(persona.Effort)
	}
	if err != nil {
		wrapped := fmt.Errorf("workbench %s: %w", persona.Bench.Dir, err)
		obs.Logger(context.Background()).Error("workbench effort", "path", persona.Bench.Dir, obs.FieldErr, wrapped)
		return workbench.Persona{}, wrapped
	}
	return persona, nil
}

func claudePersonaSpawn(
	purpose Purpose, home string, account int, cache1H bool,
	machine pfmconfig.Config, persona workbench.Persona, name string, args ...string,
) ClaudeSpawn {
	return ClaudeSpawn{
		Purpose: purpose, Home: home, Account: account, Cache1H: &cache1H,
		Machine: machine, Name: name, Args: args,
		PromptFile: persona.Prompt, Effort: persona.Effort, Model: persona.Model,
	}
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

func openCodePersonaAssignments(request Request) ([]string, error) {
	hasPersona := request.Persona.Prompt != ""
	var plugins []string
	if hasPersona {
		plugins = append(plugins, (&url.URL{Scheme: "file", Path: request.OpenCodePlugin}).String())
	}
	if request.OpenCodeHerdrPlugin != "" {
		plugins = append(plugins, (&url.URL{Scheme: "file", Path: request.OpenCodeHerdrPlugin}).String())
	}
	if len(plugins) == 0 {
		return nil, nil
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
	if !hasPersona {
		return env, nil
	}
	if request.OpenCodeFleetPrompt != "" {
		env = append(env, "PFM_OPENCODE_FLEET_FILE="+request.OpenCodeFleetPrompt)
	}
	return append(env, "PFM_OPENCODE_SYSTEM_FILE="+request.Persona.Prompt), nil
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
		Env:    launchEnvironment(os.Environ(), launch),
		CWD:    request.Row.CWD,
	}, nil
}
