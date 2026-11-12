package action

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

type HeadlessPlanner interface {
	Plan(request HeadlessRequest) (HeadlessPlan, error)
}

var planners = map[pfmengine.ID]HeadlessPlanner{}

func RegisterPlanner(id pfmengine.ID, planner HeadlessPlanner) {
	if _, duplicate := planners[id]; duplicate {
		panic(fmt.Sprintf("action: headless planner for engine %q registered twice", id))
	}
	planners[id] = planner
}

func PlannerFor(id pfmengine.ID) (HeadlessPlanner, error) {
	planner, ok := planners[id]
	if !ok {
		if descriptor, err := pfmengine.Lookup(id); err == nil {
			return nil, fmt.Errorf("%s does not support headless chat", descriptor.Short)
		}
		return nil, fmt.Errorf("engine %s: no headless planner registered", id)
	}
	return planner, nil
}

func RegisteredPlanners() []pfmengine.ID {
	ids := make([]pfmengine.ID, 0, len(planners))
	for id := range planners {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	return ids
}

// HeadlessForkRequest is one detached fork of an existing engine session.
// SessionID is always explicit: a shared MCP daemon must never inherit the
// identity of whichever process launched it.
type HeadlessForkRequest struct {
	Engine         pfmengine.ID
	SessionID      string
	Name           string
	CWD            string
	Home           string
	PrimaryAccount int
	Cache1H        bool
	Model          string
	Config         pfmconfig.Config
	Persona        workbench.Persona
	// ForkID fixes a Claude fork's own session id before birth (--session-id
	// beside --resume and --fork-session), as a Herdr seat needs; the plan then
	// carries its launch record. Empty: Claude mints the id at its first write.
	ForkID string
}

// HeadlessFork synthesizes the command for a real Claude or Codex fork.
func HeadlessFork(request HeadlessForkRequest) (HeadlessPlan, error) {
	if request.SessionID == "" || strings.ContainsAny(request.SessionID, "\r\n\x00") {
		return HeadlessPlan{}, errors.New("a fork requires one safe session id")
	}
	if err := validateHeadlessRequest(HeadlessRequest{
		Name: request.Name, CWD: request.CWD, Home: request.Home,
	}); err != nil {
		return HeadlessPlan{}, err
	}
	machine := normalizedMachineConfig(request.Config, request.Home)
	switch request.Engine {
	case pfmengine.Claude:
		if _, found := machine.Account(request.PrimaryAccount); !found {
			return HeadlessPlan{}, fmt.Errorf(
				"requested Claude account %d is not in the configured roster",
				request.PrimaryAccount,
			)
		}
		spawn := ClaudeSpawn{
			Purpose: PurposeResume, Home: request.Home, Account: request.PrimaryAccount,
			Cache1H: &request.Cache1H, SessionID: request.ForkID, Resume: request.SessionID, Fork: true,
			Name: request.Name, Model: request.Persona.ModelOr(request.Model), Machine: machine,
			PromptFile: request.Persona.Prompt, Effort: request.Persona.Effort, Envs: request.Persona.Envs,
		}
		run, err := spawn.ShellCommand()
		if err != nil {
			return HeadlessPlan{}, err
		}
		recipe, err := spawn.SeatRun(request.CWD)
		if err != nil {
			return HeadlessPlan{}, err
		}
		plan := HeadlessPlan{
			Engine: pfmengine.Claude, SeatRun: &recipe,
			Run:                 run,
			Binary:              claudeBinaryWord(machine),
			PromptOnCommandLine: true,
			Warnings:            spawn.warnings(),
		}
		if request.ForkID != "" {
			plan.Record = &fleetdb.Launch{
				SessionID: request.ForkID, Engine: pfmengine.Claude, Account: request.PrimaryAccount,
				Cache1H: request.Cache1H,
			}
		}
		return plan, nil
	case pfmengine.Codex:
		if request.ForkID != "" {
			return HeadlessPlan{}, errors.New("a Codex fork takes no session id fixed before birth")
		}
		account, found := machine.CodexAccountByID(request.PrimaryAccount)
		if !found {
			return HeadlessPlan{}, fmt.Errorf(
				"requested Codex account %d is not in the configured roster",
				request.PrimaryAccount,
			)
		}
		var instructions *string
		if request.Persona.Applies() {
			instructions = &request.Persona.Body
		}
		arguments, err := CodexArgs(request.Persona.ModelOr(request.Model), request.Home,
			request.Persona.Effort, instructions, CodexFork, request.SessionID)
		if err != nil {
			return HeadlessPlan{}, err
		}
		return HeadlessPlan{
			Engine: pfmengine.Codex,
			SeatRun: codexSeatRun(
				machine, request.PrimaryAccount, request.Persona.Envs, request.CWD, arguments...,
			),
			Run: codexCommandWithAccount(
				headlessHygiene,
				machine,
				request.PrimaryAccount,
				request.Persona.Envs,
				arguments...),
			CodexHome:           account.Home,
			Binary:              codexBinaryWord(machine, request.PrimaryAccount),
			PromptOnCommandLine: true,
		}, nil
	default:
		return HeadlessPlan{}, fmt.Errorf("chat branch supports Claude and Codex, not %q", request.Engine)
	}
}
