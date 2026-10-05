package workbench

import (
	"context"
	"fmt"
	"os"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Mode distinguishes a new launch from returning to an existing chat.
type Mode int

const (
	New Mode = iota
	Resume
)

// Persona carries a validated workbench and the prompt read for this launch.
type Persona struct {
	Bench                       Bench
	Prompt, Body, Effort, Model string
}

// ForLaunch refuses invalid manifests and disabled new engines before launch.
func ForLaunch(cwd string, engine pfmengine.ID, mode Mode) (Persona, error) {
	bench, found, err := Nearest(cwd)
	if err != nil || !found {
		return Persona{}, err
	}
	if bench.Err != nil {
		return Persona{}, bench.Err
	}
	if !bench.Enables(engine) {
		if mode == Resume {
			return Persona{}, nil
		}
		word := pfmengine.MustLookup(engine).LongName
		err := fmt.Errorf("workbench %s does not enable %s: add %q to %q in %s",
			bench.Dir, word, word, "engines", paths.WorkbenchManifest(bench.Dir))
		obs.Logger(context.Background()).
			Error("workbench launch", "path", bench.Dir, obs.FieldEngine, string(engine), obs.FieldErr, err)
		return Persona{}, err
	}
	body, err := os.ReadFile(bench.Prompt)
	if err != nil {
		wrapped := fmt.Errorf("read workbench prompt %s: %w", bench.Prompt, err)
		obs.Logger(context.Background()).Error("workbench launch", "path", bench.Prompt, obs.FieldErr, wrapped)
		return Persona{}, wrapped
	}
	return Persona{
		Bench:  bench,
		Prompt: bench.Prompt,
		Body:   string(body),
		Effort: bench.Effort,
		Model:  bench.Model,
	}, nil
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
