package action

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/launchnotice"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

// HeadlessWidth and HeadlessHeight are the geometry a detached chat is born
// with. A headless pane has no client to size it, and tmux's 80x24 default
// truncates the statusline every label lookup reads (chat.sh's cross-socket
// scan, pfm's own crumbs) — a chat nobody can address is not a teammate.
const (
	HeadlessWidth  = 220
	HeadlessHeight = 50
)

// Detached Codex commands share the registry's identity strip.
var headlessHygiene = envStripWords(claudelaunch.Hygiene())

// HeadlessRequest is one detached, named chat to start.
type HeadlessRequest struct {
	Engine          pfmengine.ID
	ResumeSessionID string
	Name            string
	CWD             string
	Prompt          string
	// PromptChannel is already composed for the selected engine: a per-seat
	// system-prompt file path for Claude, or the complete developer
	// instructions value for Codex. Empty preserves the ordinary launch.
	PromptChannel  string
	Home           string
	PrimaryAccount int
	Cache1H        *bool // nil uses the selected account's configured cache
	Config         pfmconfig.Config
	// Model and Effort pin the seat's tier at birth. A seat that inherits
	// whatever the account config holds that day is a seat whose cost and
	// quality nobody chose.
	Model  string
	Effort string
}

// claudeEfforts is the roster claude's own --help states. An unknown value is
// refused here rather than at the engine, where it would surface as a chat
// that died at birth for no stated reason.
var claudeEfforts = map[string]struct{}{
	"low": {}, "medium": {}, "high": {}, "xhigh": {}, "max": {},
}

var codexEfforts = map[string]struct{}{
	"minimal": {}, "low": {}, "medium": {}, "high": {}, "xhigh": {}, "max": {}, "ultra": {},
}

// ClaudeEffort validates effort against claudeEfforts and returns it
// lower-cased, ready for ClaudeSpawn.Effort or a Claude argv. "" is not an
// effort — it passes through unchanged and unvalidated, meaning "let the CLI
// choose". Every caller that carries a Claude effort — PlanClaude here and
// the reload door in internal/reload — validates through this ONE roster, so
// the accepted set can never drift between a fresh launch and a reboot of an
// existing one.
func ClaudeEffort(effort string) (string, error) {
	if effort == "" {
		return "", nil
	}
	lower := strings.ToLower(effort)
	if _, known := claudeEfforts[lower]; !known {
		return "", fmt.Errorf("unknown Claude effort %q (want low, medium, high, xhigh or max)", effort)
	}
	return lower, nil
}

// CodexEffort is ClaudeEffort's Codex twin, validated against codexEfforts —
// a materially different roster (Codex also accepts "minimal" and "ultra"),
// so the two engines each keep their own accepted set even though both route
// through this same pair of functions.
func CodexEffort(effort string) (string, error) {
	if effort == "" {
		return "", nil
	}
	lower := strings.ToLower(effort)
	if _, known := codexEfforts[lower]; !known {
		return "", fmt.Errorf("unknown Codex effort %q (want minimal, low, medium, high, xhigh, max or ultra)", effort)
	}
	return lower, nil
}

// CodexEffortArg renders one already-validated, lower-cased Codex effort as
// the `-c model_reasoning_effort="<value>"` pair Codex's CLI expects — the
// exact two argv words PlanCodex and the reload door both hand to
// codexCommandWithAccount / their own command-line assembly, so the flag's
// spelling lives in one place.
func CodexEffortArg(effort string) []string {
	return []string{"-c", `model_reasoning_effort="` + effort + `"`}
}

// codexModelArgs renders a Codex seat's `--model` pair, a bare roster tier (astra) resolved to its
// full Codex id through pfmengine.CodexModel; no model renders no flag.
func codexModelArgs(model, home string) ([]string, error) {
	resolved, err := pfmengine.CodexModel(model, home)
	if err != nil || resolved == "" {
		return nil, err
	}
	return []string{"--model", resolved}, nil
}

// CodexDeveloperInstructionsArg renders one complete developer-instructions
// override. Both a fresh role seat and its reload use this door so escaping
// cannot drift between the two launch paths.
func CodexDeveloperInstructionsArg(prompt string) []string {
	escaped := strings.ReplaceAll(prompt, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"""`, `\"\"\"`)
	return []string{"-c", "developer_instructions=\"\"\"\n" + escaped + "\"\"\""}
}

// HeadlessPlan is the pure result: the command the tmux session runs, its
// optional launch record, and whether the prompt travelled on the command.
type HeadlessPlan struct {
	Engine  pfmengine.ID
	SeatRun *fleetdb.SeatRun
	Run     string
	Record  *fleetdb.Launch
	// Binary is the unquoted executable word Run launches, stated so the
	// spawn layer can prove it resolves BEFORE a tmux server is created
	// around it — a pane that dies on "command not found" takes the fresh
	// server with it and the engine is never named in the wreckage.
	Binary    string
	CodexHome string
	// PromptOnCommandLine is false when the prompt must be typed into the
	// running TUI instead — Codex is named through its own rename UI, and a
	// prompt on the command line would start a turn before that can happen.
	PromptOnCommandLine bool
	// Warnings are what the person starting the chat must be told: the chat
	// starts anyway. The command that planned it prints them on its stderr.
	Warnings []string
}

// Report prints each warning to the starter's stderr under who's prefix.
func (plan HeadlessPlan) Report(stderr io.Writer, who string) {
	launchnotice.Print(stderr, who, plan.Warnings)
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
			Cache1H: &request.Cache1H, Resume: request.SessionID, Fork: true,
			Name: request.Name, Model: request.Persona.ModelOr(request.Model), Machine: machine,
			PromptFile: request.Persona.Prompt, Effort: request.Persona.Effort,
		}
		run, err := spawn.ShellCommand()
		if err != nil {
			return HeadlessPlan{}, err
		}
		recipe, err := spawn.SeatRun(request.CWD)
		if err != nil {
			return HeadlessPlan{}, err
		}
		return HeadlessPlan{
			Engine: pfmengine.Claude, SeatRun: &recipe,
			Run:                 run,
			Binary:              claudeBinaryWord(machine),
			PromptOnCommandLine: true,
			Warnings:            spawn.warnings(),
		}, nil
	case pfmengine.Codex:
		account, found := machine.CodexAccountByID(request.PrimaryAccount)
		if !found {
			return HeadlessPlan{}, fmt.Errorf(
				"requested Codex account %d is not in the configured roster",
				request.PrimaryAccount,
			)
		}
		arguments := make([]string, 0, 3)
		modelArgs, err := codexModelArgs(request.Persona.ModelOr(request.Model), request.Home)
		if err != nil {
			return HeadlessPlan{}, err
		}
		arguments = append(arguments, modelArgs...)
		if request.Persona.Effort != "" {
			arguments = append(arguments, CodexEffortArg(request.Persona.Effort)...)
		}
		if request.Persona.Applies() {
			arguments = append(arguments, CodexDeveloperInstructionsArg(request.Persona.Body)...)
		}
		arguments = append(arguments, "fork", request.SessionID)
		return HeadlessPlan{
			Engine: pfmengine.Codex, SeatRun: codexSeatRun(machine, request.PrimaryAccount, request.CWD, arguments...),
			Run: codexCommandWithAccount(
				headlessHygiene,
				machine,
				request.PrimaryAccount,
				arguments...),
			CodexHome:           account.Home,
			Binary:              codexBinaryWord(machine, request.PrimaryAccount),
			PromptOnCommandLine: true,
		}, nil
	default:
		return HeadlessPlan{}, fmt.Errorf("chat branch supports Claude and Codex, not %q", request.Engine)
	}
}

// HeadlessRun synthesizes the engine command for a detached chat. It performs
// no I/O and no tmux work: the caller owns the session.
func HeadlessRun(request HeadlessRequest) (HeadlessPlan, error) {
	planner, err := PlannerFor(request.Engine)
	if err != nil {
		return HeadlessPlan{}, err
	}
	return planner.Plan(request)
}

func validateHeadlessRequest(request HeadlessRequest) error {
	if request.Name == "" {
		return errors.New("a headless chat requires a name")
	}
	if strings.ContainsAny(request.Name, "\n\r") {
		return errors.New("a chat name cannot contain newlines")
	}
	if request.CWD == "" {
		return errors.New("a headless chat requires a project directory")
	}
	if hasNUL(request.Name, request.CWD, request.Prompt, request.PromptChannel, request.Home) {
		return errors.New("action values cannot contain NUL")
	}
	return nil
}

// PlanClaude contains the command synthesis used by Claude's planner.
func PlanClaude(request HeadlessRequest) (HeadlessPlan, error) {
	if err := validateHeadlessRequest(request); err != nil {
		return HeadlessPlan{}, err
	}
	machine := normalizedMachineConfig(request.Config, request.Home)
	if _, found := machine.Account(request.PrimaryAccount); !found {
		return HeadlessPlan{}, fmt.Errorf("primary account %d is not in the configured roster", request.PrimaryAccount)
	}
	effort, err := ClaudeEffort(request.Effort)
	if err != nil {
		return HeadlessPlan{}, err
	}
	id, err := claudelaunch.NewSessionID()
	if err != nil {
		return HeadlessPlan{}, fmt.Errorf("new Claude session id: %w", err)
	}
	arguments := []string{}
	if request.Prompt != "" {
		arguments = append(arguments, request.Prompt)
	}
	spawn := ClaudeSpawn{
		Purpose: PurposeInteractive, Account: request.PrimaryAccount,
		Cache1H: request.Cache1H, SessionID: id, Name: request.Name,
		Model: request.Model, Effort: effort, Args: arguments, Home: request.Home,
		Machine: machine, PromptFile: request.PromptChannel,
	}
	run, err := spawn.ShellCommand()
	if err != nil {
		return HeadlessPlan{}, err
	}
	recipe, err := spawn.SeatRun(request.CWD)
	if err != nil {
		return HeadlessPlan{}, err
	}
	return HeadlessPlan{
		Engine: pfmengine.Claude, SeatRun: &recipe,
		Run: run,
		Record: &fleetdb.Launch{
			SessionID: id, Engine: pfmengine.Claude, Account: request.PrimaryAccount,
			Cache1H: resolvedCache1H(machine, request.PrimaryAccount, request.Cache1H),
		},
		Binary:              claudeBinaryWord(machine),
		PromptOnCommandLine: true,
		Warnings:            spawn.warnings(),
	}, nil
}

func resolvedCache1H(machine pfmconfig.Config, account int, choice *bool) bool {
	if choice != nil {
		return *choice
	}
	return machine.EffectiveClaude(account).Cache1H
}

// PlanCodex contains the command synthesis used by Codex's planner.
func PlanCodex(request HeadlessRequest) (HeadlessPlan, error) {
	if err := validateHeadlessRequest(request); err != nil {
		return HeadlessPlan{}, err
	}
	machine := normalizedMachineConfig(request.Config, request.Home)
	account, found := machine.CodexAccountByID(request.PrimaryAccount)
	if !found {
		return HeadlessPlan{}, fmt.Errorf(
			"requested Codex account %d is not in the configured roster",
			request.PrimaryAccount,
		)
	}
	effort, err := CodexEffort(request.Effort)
	if err != nil {
		return HeadlessPlan{}, err
	}
	modelArgs, err := codexModelArgs(request.Model, request.Home)
	if err != nil {
		return HeadlessPlan{}, err
	}
	arguments := append(make([]string, 0, 6), modelArgs...)
	if effort != "" {
		arguments = append(arguments, CodexEffortArg(effort)...)
	}
	if request.PromptChannel != "" {
		arguments = append(arguments, CodexDeveloperInstructionsArg(request.PromptChannel)...)
	}
	if request.ResumeSessionID != "" {
		if strings.ContainsAny(request.ResumeSessionID, "\r\n\x00") {
			return HeadlessPlan{}, errors.New("codex resume requires one safe session id")
		}
		arguments = append(arguments, "resume", request.ResumeSessionID)
	}
	return HeadlessPlan{
		Engine: pfmengine.Codex, SeatRun: codexSeatRun(machine, request.PrimaryAccount, request.CWD, arguments...),
		Run:       codexCommandWithAccount(headlessHygiene, machine, request.PrimaryAccount, arguments...),
		Binary:    codexBinaryWord(machine, request.PrimaryAccount),
		CodexHome: account.Home,
	}, nil
}

// codexSeatRun retains argv and account environment without a shell or the cx launcher.
func codexSeatRun(machine pfmconfig.Config, account int, cwd string, args ...string) *fleetdb.SeatRun {
	env := []string{}
	if selected, found := machine.CodexAccountByID(account); found {
		env = append(env, "CODEX_HOME="+selected.Home)
	}
	launch := claudelaunch.Launch{Env: env, Unset: append(append([]string{}, hygieneNames...), "CODEX_HOME")}
	arguments := append(codexAutonomyArgs(machine.EffectiveCodex(account).Yolo), args...)
	return &fleetdb.SeatRun{
		Binary: codexBinaryWord(machine, account),
		Args:   arguments,
		Env:    launchEnvironment(os.Environ(), launch),
		CWD:    cwd,
	}
}

func codexAutonomyArgs(yolo bool) []string {
	args := []string{pfmengine.CodexNoDaemon}
	if yolo {
		return append(args, "--dangerously-bypass-approvals-and-sandbox")
	}
	return append(args, "--sandbox", "workspace-write")
}

// PlanOpenCode uses the same launch producer as an interactive fresh seat.
func PlanOpenCode(request HeadlessRequest) (HeadlessPlan, error) {
	if err := validateHeadlessRequest(request); err != nil {
		return HeadlessPlan{}, err
	}
	if request.Effort != "" || request.PromptChannel != "" {
		return HeadlessPlan{}, errors.New("OpenCode headless effort and prompt channel are not supported")
	}
	machine := normalizedMachineConfig(request.Config, request.Home)
	launch := Request{
		Row:            compose.Row{Kind: compose.NewOpenCode, CWD: request.CWD},
		FreshSocket:    "headless-plan",
		PrimaryAccount: request.PrimaryAccount,
		Home:           request.Home,
		Config:         machine,
		Prompt:         request.Prompt,
	}
	if err := applyWorkbench(&launch); err != nil {
		return HeadlessPlan{}, err
	}
	plan, err := Synthesize(launch)
	if err != nil {
		return HeadlessPlan{}, err
	}
	if request.Model != "" {
		plan.SeatRun.Args = append(plan.SeatRun.Args, "--model", request.Model)
		plan.Run += " --model " + Quote(request.Model)
	}
	return HeadlessPlan{
		Engine:              pfmengine.OpenCode,
		SeatRun:             plan.SeatRun,
		Run:                 plan.Run,
		Binary:              plan.SeatRun.Binary,
		PromptOnCommandLine: true,
	}, nil
}
