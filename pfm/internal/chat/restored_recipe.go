package chat

import (
	"errors"
	"io"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/launchnotice"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

// restoredRecipe is the structured resume recipe of a chat no seat holds,
// built for its engine with its managed root's settings envs.
func restoredRecipe(
	row compose.Row,
	id string,
	account int,
	runtime config.Runtime,
	stderr io.Writer,
) (*fleetdb.SeatRun, error) {
	engineID := compose.EngineForKind(row.Kind)
	var recipe *fleetdb.SeatRun
	var envs map[string]string
	if engineID == pfmengine.Claude || engineID == pfmengine.Codex {
		// The managed root's settings envs ride a restore as every launch;
		// OpenCode reads them through action.Synthesize.
		persona, warnings, err := action.LaunchPersona(row.CWD, runtime.Paths.Home, engineID, workbench.Resume)
		if err != nil {
			return nil, err
		}
		launchnotice.Print(stderr, "pfm chat open", warnings)
		envs = persona.Envs
	}
	switch engineID {
	case pfmengine.Claude:
		spawnPlan := action.ClaudeSpawn{
			Purpose: action.PurposeResume,
			Home:    runtime.Paths.Home,
			Account: account,
			Cache1H: &row.C1H,
			Resume:  id,
			Machine: runtime.Config,
			Envs:    envs,
		}
		run, err := spawnPlan.SeatRun(row.CWD)
		if err != nil {
			return nil, err
		}
		recipe = &run
	case pfmengine.Codex:
		plan, err := action.PlanCodex(
			action.HeadlessRequest{
				Engine:          engineID,
				ResumeSessionID: id,
				Name:            row.Name,
				CWD:             row.CWD,
				Home:            runtime.Paths.Home,
				PrimaryAccount:  account,
				Config:          runtime.Config,
				Envs:            envs,
			},
		)
		if err != nil {
			return nil, err
		}
		recipe = plan.SeatRun
	case pfmengine.OpenCode:
		row.Kind = compose.ResumeKindFor(row.Kind)
		plan, err := action.Synthesize(
			action.Request{
				Row:            row,
				Home:           runtime.Paths.Home,
				PrimaryAccount: account,
				Config:         runtime.Config,
				FreshSocket:    "restored",
			},
		)
		if err != nil {
			return nil, err
		}
		recipe = plan.SeatRun
	}
	if recipe == nil {
		return nil, errors.New("restored engine has no structured resume recipe")
	}
	return recipe, nil
}
