package agentrole

import (
	"fmt"
	"os"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/codexgen"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

// BasePrompt returns the workbench persona or the engine's fleet prompt.
func BasePrompt(engine pfmengine.ID, cwd, home string) (string, error) {
	persona, err := workbench.ForLaunch(cwd, engine, workbench.Resume)
	if err != nil {
		return "", err
	}
	if persona.Applies() {
		return persona.Body, nil
	}
	if engine != pfmengine.Claude {
		return codexgen.FleetPrompt()
	}
	path, err := action.ProfessorPromptPath(home)
	if err != nil {
		return "", fmt.Errorf("agent role: resolve Claude prompt: %w", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("agent role: read Claude prompt %s: %w", path, err)
	}
	return string(raw), nil
}

// ResolveSeatPrompt composes the resolved role on the explicit harness or cwd's base.
func ResolveSeatPrompt(
	engine pfmengine.ID,
	role, cwd, home, harnessBody string,
) (body, constitution string, err error) {
	constitution, _, err = Resolve(engine, role, cwd, home)
	if err != nil {
		return "", "", err
	}
	base := harnessBody
	if engine == pfmengine.Claude && base == "" {
		base, err = BasePrompt(engine, cwd, home)
		if err != nil {
			return "", "", err
		}
	}
	body, err = ComposeSeatPrompt(engine, role, constitution, base)
	return body, constitution, err
}
