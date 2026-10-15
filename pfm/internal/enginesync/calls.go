package enginesync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// The flags more than one engine-sync verb takes.
const (
	flagHome            = "--home"
	flagJSON            = "--json"
	flagSourceRepo      = "--source-repo"
	flagClaudeConfigDir = "--claude-config-dir"
	flagCodexHome       = "--codex-home"
)

// CapabilityGptAgents is the capability name pfm declares when claude.gptAgents
// is true: the agents whose frontmatter requires it are linked and emitted.
// engine-sync gives names no meaning; the name is pfm's and the templates'.
const CapabilityGptAgents = "gpt-agents"

const flagCapability = "--capability"

// HostCapabilities is the capability set pfm declares for the host's config:
// the Runner field every agent-rendering call passes as --capability flags.
func HostCapabilities(gptAgents bool) []string {
	if gptAgents {
		return []string{CapabilityGptAgents}
	}
	return nil
}

// The two engines a mirror call names: engine-sync spells each by its registry long name.
var (
	EngineCodex    = pfmengine.MustLookup(pfmengine.Codex).LongName
	EngineOpenCode = pfmengine.MustLookup(pfmengine.OpenCode).LongName
)

// The two mirror actions a workbench runs.
const (
	ActionBuild = "build"
	ActionCheck = "check"
)

// The verbs of a global event, the installer's own transcript verbs.
const (
	VerbChange   = "change"
	VerbRetire   = "retire"
	VerbOK       = "ok"
	VerbSkip     = "skip"
	VerbConflict = "conflict"
)

// The levels of a machine doctor row.
const (
	LevelOK   = "ok"
	LevelWarn = "warn"
	LevelFail = "fail"
)

// MirrorResult is one engine's build or check report: problems holds every
// problem line, rebuildable the multiset of them a build would repair.
type MirrorResult struct {
	OK          bool     `json:"ok"`
	Problems    []string `json:"problems"`
	Rebuildable []string `json:"rebuildable"`
	Warnings    []string `json:"warnings"`
	Wrote       int      `json:"wrote"`
	Unchanged   int      `json:"unchanged"`
	Deleted     int      `json:"deleted"`
	Error       string   `json:"error"`
}

// Mirror runs `engine-sync {engine} {action} {repo} --home {home} --json`.
// A finding is a result with OK false; an error is a run that could not
// report one (no binary, a usage error, a run failure, a reply that is not
// the interface's shape).
func (runner Runner) Mirror(ctx context.Context, engine, action, repo string) (MirrorResult, error) {
	var result MirrorResult
	switch engine {
	case EngineCodex, EngineOpenCode:
	default:
		return result, fmt.Errorf("engine-sync mirror: unknown engine %q", engine)
	}
	switch action {
	case ActionBuild, ActionCheck:
	default:
		return result, fmt.Errorf("engine-sync mirror: unknown action %q", action)
	}
	home, err := runner.home()
	if err != nil {
		return result, err
	}
	args := append([]string{engine, action, repo, flagHome, home}, runner.capabilityArgs()...)
	args = append(args, flagJSON)
	if err := runner.runJSON(ctx, args, &result, func() bool { return result.OK }); err != nil {
		return result, err
	}
	command := "engine-sync " + strings.Join(args[:3], " ")
	if result.Error != "" {
		return result, fmt.Errorf("%s: %s", command, result.Error)
	}
	// ok, problems and rebuildable must agree, or a failing mirror would read
	// as one with nothing to repair.
	if result.OK != (len(result.Problems) == 0) {
		if result.OK {
			return result, fmt.Errorf("%s: reply passes with %d problem(s)", command, len(result.Problems))
		}
		return result, fmt.Errorf("%s: reply fails naming no problem", command)
	}
	unclaimed := make(map[string]int, len(result.Problems))
	for _, problem := range result.Problems {
		unclaimed[problem]++
	}
	for _, line := range result.Rebuildable {
		if unclaimed[line] == 0 {
			return result, fmt.Errorf("%s: reply's rebuildable %q is not among its problems", command, line)
		}
		unclaimed[line]--
	}
	return result, nil
}

// GlobalOptions are the installer's Codex steps for one run. An empty
// CodexHomes is the explicit empty roster (--no-codex-home): pfm always names
// its roster, never leaving engine-sync to read the global file's targets for it.
type GlobalOptions struct {
	// Adopt passes --adopt: an unowned regular file at a declared agent's path
	// is moved aside into engine-sync's adopted store and replaced.
	Adopt            bool
	SourceRepo       string
	ClaudeConfigDirs []string
	CodexHomes       []string
	// SourceHome is the home whose .claude/commands the global commands
	// compile from; "" is --home.
	SourceHome string
	Plan       bool
	Uninstall  bool
}

// Event is one installer transcript line engine-sync did, or under --plan
// would do. Path is set on a retire only.
type Event struct {
	Verb    string `json:"verb"`
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
}

// GlobalResult is `engine-sync global --json`'s reply. OK false with no
// Error is a conflict, named among the events.
type GlobalResult struct {
	OK     bool    `json:"ok"`
	Events []Event `json:"events"`
	Error  string  `json:"error"`
}

// GlobalArgs is the argv after the binary for options, every path explicit.
func (runner Runner) GlobalArgs(options GlobalOptions) ([]string, error) {
	home, err := runner.home()
	if err != nil {
		return nil, err
	}
	args := []string{"global", flagHome, home}
	if options.SourceRepo != "" {
		args = append(args, flagSourceRepo, options.SourceRepo)
	}
	for _, dir := range options.ClaudeConfigDirs {
		args = append(args, flagClaudeConfigDir, dir)
	}
	if len(options.CodexHomes) == 0 {
		args = append(args, "--no-codex-home")
	}
	for _, codexHome := range options.CodexHomes {
		args = append(args, flagCodexHome, codexHome)
	}
	if options.SourceHome != "" {
		args = append(args, "--source-home", options.SourceHome)
	}
	if options.Plan {
		args = append(args, "--plan")
	}
	if options.Uninstall {
		args = append(args, "--uninstall")
	}
	if options.Adopt {
		args = append(args, "--adopt")
	}
	args = append(args, runner.capabilityArgs()...)
	return append(args, flagJSON), nil
}

// Global runs the installer's Codex steps. The events are returned whatever
// the outcome, so a caller renders what ran before the step that stopped it.
func (runner Runner) Global(ctx context.Context, options GlobalOptions) (GlobalResult, error) {
	var result GlobalResult
	args, err := runner.GlobalArgs(options)
	if err != nil {
		return result, err
	}
	if err := runner.runJSON(ctx, args, &result, func() bool { return result.OK }); err != nil {
		return result, err
	}
	for index, event := range result.Events {
		switch event.Verb {
		case VerbChange, VerbOK, VerbSkip, VerbConflict:
			if event.Path != "" {
				return result, fmt.Errorf(
					"engine-sync global: event %d (%s) carries a path, set on retire only",
					index,
					event.Verb,
				)
			}
		case VerbRetire:
			if event.Path == "" {
				return result, fmt.Errorf("engine-sync global: retire event %d names no path: %q", index, event.Message)
			}
		default:
			return result, fmt.Errorf("engine-sync global: event %d has unknown verb %q", index, event.Verb)
		}
		if event.Message == "" {
			return result, fmt.Errorf("engine-sync global: event %d (%s) has no message", index, event.Verb)
		}
	}
	if result.Error != "" {
		return result, fmt.Errorf("engine-sync global: %s", result.Error)
	}
	return result, nil
}

// MachineDoctorOptions classify the Claude agents registry and every Codex
// agents registry against the source clone. An empty SourceRepo is the
// no-clone state.
type MachineDoctorOptions struct {
	SourceRepo      string
	ClaudeConfigDir string
	CodexHomes      []string
	ClaudeAbsent    bool
}

// Row is one machine doctor row.
type Row struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

type machineDoctorReply struct {
	OK    bool   `json:"ok"`
	Rows  []Row  `json:"rows"`
	Error string `json:"error"`
}

// MachineDoctor runs `engine-sync doctor --machine --json` and returns its
// rows, each validated: an unknown level, an empty message, or an ok that
// disagrees with the rows is an error, never a row read as healthy.
func (runner Runner) MachineDoctor(ctx context.Context, options MachineDoctorOptions) ([]Row, error) {
	home, err := runner.home()
	if err != nil {
		return nil, err
	}
	args := []string{"doctor", "--machine", flagHome, home}
	if options.SourceRepo != "" {
		args = append(args, flagSourceRepo, options.SourceRepo)
	}
	if options.ClaudeConfigDir != "" {
		args = append(args, flagClaudeConfigDir, options.ClaudeConfigDir)
	}
	for _, codexHome := range options.CodexHomes {
		args = append(args, flagCodexHome, codexHome)
	}
	if options.ClaudeAbsent {
		args = append(args, "--claude-absent")
	}
	args = append(args, runner.capabilityArgs()...)
	args = append(args, flagJSON)
	var reply machineDoctorReply
	if err := runner.runJSON(ctx, args, &reply, func() bool { return reply.OK }); err != nil {
		return nil, err
	}
	if reply.Error != "" {
		return nil, fmt.Errorf("engine-sync doctor --machine: %s", reply.Error)
	}
	failing := false
	for index, row := range reply.Rows {
		switch row.Level {
		case LevelOK, LevelWarn:
		case LevelFail:
			failing = true
		default:
			return nil, fmt.Errorf("engine-sync doctor --machine: row %d has unknown level %q", index, row.Level)
		}
		if row.Message == "" {
			return nil, fmt.Errorf("engine-sync doctor --machine: row %d has no message", index)
		}
	}
	if failing == reply.OK {
		return nil, fmt.Errorf("engine-sync doctor --machine: ok=%t disagrees with its rows", reply.OK)
	}
	return reply.Rows, nil
}

// runJSON runs args and decodes the one JSON object a 0 or 1 exit prints
// into reply. ok reads the decoded object's ok field, which must agree with
// the exit code. Any other exit — a usage error, a crash — is an error
// carrying the binary's stderr.
func (runner Runner) runJSON(ctx context.Context, args []string, reply any, ok func() bool) error {
	result, err := runner.run(ctx, args)
	if err != nil {
		return err
	}
	command := "engine-sync " + strings.Join(args, " ")
	if result.ExitCode != 0 && result.ExitCode != 1 {
		return fmt.Errorf("%s: exit %d: %s", command, result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	decoder := json.NewDecoder(bytes.NewReader(result.Stdout))
	if err := decoder.Decode(reply); err != nil {
		return fmt.Errorf("%s: exit %d: decode reply: %w (stderr: %s)",
			command, result.ExitCode, err, strings.TrimSpace(string(result.Stderr)))
	}
	if decoder.More() {
		return fmt.Errorf("%s: reply holds more than one JSON object", command)
	}
	if ok() != (result.ExitCode == 0) {
		return fmt.Errorf("%s: exit %d disagrees with its reply's ok=%t", command, result.ExitCode, ok())
	}
	return nil
}

// capabilityArgs is one --capability flag per name the host declares.
func (runner Runner) capabilityArgs() []string {
	return capabilityFlags(runner.Capabilities)
}

func capabilityFlags(capabilities []string) []string {
	var flags []string
	for _, name := range capabilities {
		flags = append(flags, flagCapability, name)
	}
	return flags
}

// forwardedCapabilityVerbs are the forwarded verbs that render or check agents,
// per engine: only these accept --capability.
var forwardedCapabilityVerbs = map[string][]string{
	EngineCodex:    {"build", "check", "agents"},
	EngineOpenCode: {"build", "check", "doctor"},
}

// CapabilityArgs adds the host's --capability flags to a forwarded
// `engine-sync {engine} {verb} …` argument list (args after the engine) when
// the verb renders or checks agents, right after the verb, so a trailing
// value flag such as --home never takes one as its value; any other verb is
// returned as given.
func CapabilityArgs(engine string, args, capabilities []string) []string {
	if len(capabilities) == 0 || len(args) == 0 {
		return args
	}
	for _, verb := range forwardedCapabilityVerbs[engine] {
		if args[0] == verb {
			forwarded := append([]string{verb}, capabilityFlags(capabilities)...)
			return append(forwarded, args[1:]...)
		}
	}
	return args
}

// SourceArgs supplies a recorded clone before the forwarding flag sentinel.
func SourceArgs(args []string, source string) []string {
	return insertFlags(args, "--source-repo", source)
}

func insertFlags(args []string, flags ...string) []string {
	split := len(args)
	for index, value := range args {
		if value == "--" {
			split = index
			break
		}
	}
	forwarded := append([]string(nil), args[:split]...)
	forwarded = append(forwarded, flags...)
	return append(forwarded, args[split:]...)
}
