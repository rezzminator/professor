package action

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
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
	request.OpenCodeFleetPrompt = ""
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

func claudeCommandForPersona(
	purpose Purpose, home string, account int, cache1H bool,
	machine pfmconfig.Config, persona workbench.Persona, name string, args ...string,
) (string, error) {
	return ClaudeSpawn{
		Purpose: purpose, Home: home, Account: account, Cache1H: &cache1H,
		Machine: machine, Name: name, Args: args,
		PromptFile: persona.Prompt, Effort: persona.Effort, Model: persona.Model,
	}.ShellCommand()
}

func openCodePersonaEnv(request Request) (string, error) {
	if request.Persona.Prompt == "" {
		return "", nil
	}
	pluginURL := (&url.URL{Scheme: "file", Path: request.OpenCodePlugin}).String()
	body, err := json.Marshal(map[string]any{
		"instructions": []string{request.Persona.Prompt},
		"plugin":       []string{pluginURL},
	})
	if err != nil {
		obs.Logger(context.Background()).Error("OpenCode workbench config", obs.FieldErr, err)
		return "", err
	}
	env := " OPENCODE_CONFIG_CONTENT=" + Quote(string(body))
	if request.OpenCodeFleetPrompt != "" {
		env += " PFM_OPENCODE_FLEET_FILE=" + Quote(request.OpenCodeFleetPrompt)
	}
	return env +
		" PFM_OPENCODE_SYSTEM_FILE=" + Quote(request.Persona.Prompt), nil
}
