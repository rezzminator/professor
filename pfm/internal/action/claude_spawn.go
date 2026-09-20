package action

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/deps"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/obs"
)

// Purpose states WHY a Claude process is being started, and it is the only
// input the door reads to decide what prompt material the launch carries. A
// new spawn site states its purpose and inherits the fleet's policy; it never
// assembles a prompt decision of its own. That is the whole point of this
// file: before it there were four independent constructors, and a chat's
// system prompt depended on which one happened to start it.
type Purpose int

const (
	// PurposeInteractive starts a chat a human types into.
	PurposeInteractive Purpose = iota
	// PurposeResume re-enters an existing transcript. It carries the same
	// prompt material as a fresh chat — a reloaded or resumed seat that
	// silently reverted to the CLI's own prompt is the defect this door
	// exists to close.
	PurposeResume
	// PurposeProbe is a one-token non-conversational exchange (a credential
	// refresh ACK). The configured prompt is irrelevant to the answer and a
	// full prompt is pure cost, so a probe always takes the CLI's built-in
	// minimal prompt.
	PurposeProbe
	// PurposeQuery is a non-conversational subcommand — `agents --json`,
	// `--version`, the agent view. No model turn happens, so no prompt
	// material is attached at all; hygiene is the entire policy.
	PurposeQuery
)

// String names the purpose for error text and the doctor's spawn audit.
func (purpose Purpose) String() string {
	switch purpose {
	case PurposeInteractive:
		return "interactive"
	case PurposeResume:
		return "resume"
	case PurposeProbe:
		return "probe"
	case PurposeQuery:
		return "query"
	default:
		return fmt.Sprintf("purpose(%d)", int(purpose))
	}
}

func (purpose Purpose) known() bool {
	switch purpose {
	case PurposeInteractive, PurposeResume, PurposeProbe, PurposeQuery:
		return true
	default:
		return false
	}
}

// ClaudeSpawn is THE door: every Claude process the fleet starts is described
// by one of these and rendered by one of its two renderers. It owns three
// decisions that used to be copied per call site — the inherited-environment
// strip, the autonomy posture, and the system-prompt injection — so a spawn
// site that forgets one is not possible to write.
//
// Machine and Account carry the policy: the config dir, the autonomy posture
// (EffectiveClaude(Account).PermissionMode) and the prompt choice
// (EffectiveClaude(Account).SystemPrompt) all resolve from them. A caller that
// holds only a config directory states it as a one-account Machine rather than
// handing the door a bare path — a bare path carries no policy, and a door
// that accepted one would be back to guessing.
type ClaudeSpawn struct {
	Purpose Purpose
	Account int
	Cache1H bool
	// Model, when set, appends `--model <Model>` after Args.
	Model string
	// Effort, when set, appends `--effort <Effort>` after Model. The caller
	// validates it against ClaudeEffort's roster before setting it — this
	// door only carries the value, exactly as it only carries Model.
	Effort string
	// Args are the caller's own argv words, shell-quoted one by one in
	// ShellCommand and passed through verbatim by Command.
	Args []string
	// Home is the managed root the staged professor prompt lives under.
	Home string
	// Machine is the resolved machine config. Callers that normalize it
	// (Synthesize does) normalize BEFORE building the spawn: the door never
	// substitutes defaults for a config a caller deliberately assembled.
	Machine pfmconfig.Config
	// Runner is the process seam used by direct Claude launches. Nil selects
	// the real runner; tests can script argv, environment, and lifecycle.
	Runner deps.Runner

	// strip widens the hygiene list for the headless routes, which must also
	// drop CODEX_THREAD_ID. nil means the fleet-wide hygieneNames.
	strip []string
	// binary and quoteBinary override the config-derived executable word. Only
	// the managed launcher uses them: it has already resolved the real Claude
	// binary behind its own shim and must spawn exactly that file.
	binary      string
	quoteBinary bool
	// explicitConfigDir is the managed launcher's own CLAUDE_CONFIG_DIR, read
	// from the environment rather than from an account row. Every other caller
	// leaves it empty and the account decides.
	explicitConfigDir string
	// noAutonomy suppresses the autonomy flags for the argv-preserving
	// launcher, whose native action caller already carries them —
	// emitting them here would duplicate them in argv.
	noAutonomy bool
}

// ShellCommand renders the spawn as one POSIX shell command string: the
// hygiene `env -u …` prefix, the account's environment assignments, the
// binary word, and the argv. It is what every tmux-borne launch runs.
func (spawn ClaudeSpawn) ShellCommand() (string, error) {
	if err := spawn.validate(); err != nil {
		return "", err
	}
	prefs := spawn.Machine.EffectiveClaude(spawn.Account)
	var command strings.Builder
	command.WriteString(envStripWords(spawn.stripNames()))
	if directory := spawn.configDir(); directory != "" {
		command.WriteString(" CLAUDE_CONFIG_DIR=")
		command.WriteString(Quote(directory))
	}
	command.WriteByte(' ')
	command.WriteString(spawn.cacheAssignment())
	if spawn.leanEnvironment(prefs) {
		command.WriteString(" CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=1")
	}
	if prefs.NativeCursor {
		command.WriteString(" " + nativeCursorEnv)
	}
	writeAssignments(&command, pfmengine.MustLookup(pfmengine.Claude).LaunchEnv)
	writeAssignments(&command, prefs.SubagentEnv())
	command.WriteByte(' ')
	value, quote := spawn.binaryWord(prefs)
	command.WriteString(binaryWord(value, pfmengine.MustLookup(pfmengine.Claude).Binary, quote))
	for _, argument := range spawn.Args {
		command.WriteByte(' ')
		command.WriteString(Quote(argument))
	}
	for _, argument := range pfmengine.LaunchArgsWithSettings(
		pfmengine.Claude, spawn.Args, pfmengine.ClaudeSettingsPayload(prefs.Theme),
	) {
		command.WriteByte(' ')
		command.WriteString(Quote(argument))
	}
	if spawn.Model != "" {
		command.WriteString(" --model ")
		command.WriteString(Quote(spawn.Model))
	}
	if spawn.Effort != "" {
		command.WriteString(" --effort ")
		command.WriteString(Quote(spawn.Effort))
	}
	if file := spawn.promptFile(prefs); file != "" {
		command.WriteString(" --system-prompt-file ")
		command.WriteString(Quote(file))
	}
	if spawn.autonomy(prefs) {
		command.WriteByte(' ')
		command.WriteString(autonomyFlags)
	}
	return command.String(), nil
}

// Command renders the same spawn for direct execution: the caller's own
// environment minus the hygiene strip, plus the account's assignments, and the
// argv the shell form would have produced. Stdout, Stderr and Dir belong to
// the caller.
// ProcessCommand is the small command surface used by the direct Claude
// callers. It keeps their stdio and directory controls while making process
// creation cross the shared deps.Runner seam.
type ProcessCommand struct {
	ctx    context.Context
	runner deps.Runner
	Path   string
	Args   []string
	Env    []string
	Dir    string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func (spawn ClaudeSpawn) Command(ctx context.Context) (*ProcessCommand, error) {
	if err := spawn.validate(); err != nil {
		return nil, err
	}
	prefs := spawn.Machine.EffectiveClaude(spawn.Account)
	value, _ := spawn.binaryWord(prefs)
	if value == "" {
		value = pfmengine.MustLookup(pfmengine.Claude).Binary
	}
	path := deps.Executable(value)
	return &ProcessCommand{
		ctx:    ctx,
		runner: spawn.runner(),
		Path:   path,
		Args:   append([]string{path}, spawn.argv(prefs)...),
		Env:    spawn.Environment(os.Environ()),
	}, nil
}

func (spawn ClaudeSpawn) runner() deps.Runner {
	if spawn.Runner != nil {
		return spawn.Runner
	}
	return obs.Runner(deps.RealRunner{})
}

func (command *ProcessCommand) start(stdout, stderr io.Writer) (deps.Process, error) {
	return command.runner.Start(command.ctx, command.Args, deps.StartOptions{
		Env: command.Env, Dir: command.Dir, Stdin: command.Stdin,
		Stdout: stdout, Stderr: stderr,
	})
}

func (command *ProcessCommand) Run() error {
	process, err := command.start(command.Stdout, command.Stderr)
	if err != nil {
		return err
	}
	return process.Wait()
}

func (command *ProcessCommand) Output() ([]byte, error) {
	if command.Stdout != nil {
		return nil, errors.New("action: ProcessCommand.Output called with Stdout already set")
	}
	var output bytes.Buffer
	process, err := command.start(&output, command.Stderr)
	if err != nil {
		return nil, err
	}
	if err := process.Wait(); err != nil {
		return output.Bytes(), err
	}
	return output.Bytes(), nil
}

// Environment applies the door's strip and assignments to one environment
// slice. It is exported so a caller that must build its own exec.Cmd (a probe
// that needs a different stdio wiring, a test) still gets the fleet's one
// hygiene list rather than writing a fourth copy.
func (spawn ClaudeSpawn) Environment(environ []string) []string {
	prefs := spawn.Machine.EffectiveClaude(spawn.Account)
	dropped := make(map[string]struct{}, len(spawn.stripNames()))
	for _, name := range spawn.stripNames() {
		dropped[name] = struct{}{}
	}
	result := make([]string, 0, len(environ)+3)
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		if _, strip := dropped[name]; strip {
			continue
		}
		result = append(result, entry)
	}
	if directory := spawn.configDir(); directory != "" {
		result = append(result, "CLAUDE_CONFIG_DIR="+directory)
	}
	result = append(result, spawn.cacheAssignment())
	if spawn.leanEnvironment(prefs) {
		result = append(result, "CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=1")
	}
	if prefs.NativeCursor {
		result = append(result, nativeCursorEnv)
	}
	result = append(result, pfmengine.MustLookup(pfmengine.Claude).LaunchEnv...)
	// Last duplicate wins at exec, so the door's caps land after anything the
	// caller's own environment already carried.
	return append(result, prefs.SubagentEnv()...)
}

// writeAssignments appends one NAME=value list to a shell command, each value
// quoted. Both renderers' assignment lists go through it so a second list can
// never be spelled a second way.
func writeAssignments(command *strings.Builder, assignments []string) {
	for _, assignment := range assignments {
		name, value, _ := strings.Cut(assignment, "=")
		command.WriteByte(' ')
		command.WriteString(name)
		command.WriteByte('=')
		command.WriteString(Quote(value))
	}
}

// argv is the executable's argument list — the unquoted twin of the tail
// ShellCommand builds, in the same order.
func (spawn ClaudeSpawn) argv(prefs pfmconfig.ClaudePrefs) []string {
	argv := make([]string, 0, len(spawn.Args)+8)
	argv = append(argv, spawn.Args...)
	argv = append(argv, pfmengine.LaunchArgsWithSettings(
		pfmengine.Claude, spawn.Args, pfmengine.ClaudeSettingsPayload(prefs.Theme),
	)...)
	if spawn.Model != "" {
		argv = append(argv, "--model", spawn.Model)
	}
	if spawn.Effort != "" {
		argv = append(argv, "--effort", spawn.Effort)
	}
	if file := spawn.promptFile(prefs); file != "" {
		argv = append(argv, "--system-prompt-file", file)
	}
	if spawn.autonomy(prefs) {
		argv = append(argv, "--allow-dangerously-skip-permissions", "--dangerously-skip-permissions")
	}
	return argv
}

func (spawn ClaudeSpawn) validate() error {
	if !spawn.Purpose.known() {
		// An unrecognised purpose must never fall through to "production
		// prompt, no flags": that verdict is indistinguishable from a
		// deliberate production launch, and the caller would never learn its
		// spawn was unclassified.
		return fmt.Errorf("claude spawn: unknown purpose %s", spawn.Purpose)
	}
	values := append([]string{spawn.Home, spawn.Model, spawn.Effort, spawn.binary}, spawn.Args...)
	if hasNUL(values...) {
		return errors.New("claude spawn values cannot contain NUL")
	}
	return nil
}

func (spawn ClaudeSpawn) stripNames() []string {
	if spawn.strip != nil {
		return spawn.strip
	}
	return hygieneNames
}

// configDir is the account's explicit CLAUDE_CONFIG_DIR, or "" for the
// implicit account — hygiene already unset the inherited one.
func (spawn ClaudeSpawn) configDir() string {
	if spawn.explicitConfigDir != "" {
		return spawn.explicitConfigDir
	}
	if selected, found := spawn.Machine.Account(spawn.Account); found && !selected.Implicit {
		return selected.ConfigDir
	}
	return ""
}

func (spawn ClaudeSpawn) cacheAssignment() string {
	if spawn.Cache1H {
		return "ENABLE_PROMPT_CACHING_1H=1"
	}
	return "FORCE_PROMPT_CACHING_5M=1"
}

// nativeCursorEnv is the assignment claude.nativeCursor adds to a launch; both
// renderers above spell it from here so they cannot drift.
const nativeCursorEnv = "CLAUDE_CODE_NATIVE_CURSOR=1"

// leanEnvironment reports whether CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT=1 travels
// with the launch: the configured choice for a conversational spawn, and
// unconditionally for a probe.
func (spawn ClaudeSpawn) leanEnvironment(prefs pfmconfig.ClaudePrefs) bool {
	switch spawn.Purpose {
	case PurposeProbe:
		return true
	case PurposeInteractive, PurposeResume:
		return prefs.SystemPrompt == pfmconfig.SystemPromptLean
	default:
		return false
	}
}

// promptFile is the staged professor prompt for a conversational spawn whose
// account chose it, and "" everywhere else — including when the staged file
// is missing.
//
// A missing prompt must never brick a chat launch. That is the retired
// `pfm internal prompt-args` shim's contract, verbatim: "Fail-OPEN on any
// nonzero exit — a broken prompt layer must not brick every chat spawn." This
// door has no diagnostic/warning channel of its own to name the gap on —
// ShellCommand and Command either succeed or hard-fail validate(), and
// wiring one in here would print on every ordinary launch of an account that
// simply has not run `pfm install` yet. The surface that DOES report this
// gap already exists: `pfm doctor`'s spawn audit
// (cmd/pfm/doctor_spawn_audit.go, spawnDoorStamp feeding classifySpawn)
// reports a live Professor-policy launch with no --system-prompt-file in its
// argv as VIOLATION, "fresh launch with no prompt material — some spawn site
// bypassed the door".
//
// A stat error that is not "file does not exist" (permission denied, a
// dangling symlink, a filesystem gone away) is not the same silence as
// absence. It still fails open today — refusing to launch over a broken
// filesystem permission is worse than the lean fallback — but it is branched
// separately so that distinction survives if a diagnostic channel is ever
// added here.
func (spawn ClaudeSpawn) promptFile(prefs pfmconfig.ClaudePrefs) string {
	switch spawn.Purpose {
	case PurposeInteractive, PurposeResume:
		if prefs.SystemPrompt != pfmconfig.SystemPromptProfessor {
			return ""
		}
		path := ProfessorPromptPath(spawn.Home)
		_, err := os.Stat(path)
		switch {
		case err == nil:
			return path
		case errors.Is(err, os.ErrNotExist):
			return ""
		default:
			return ""
		}
	}
	return ""
}

// autonomy reports whether the launch carries the bypass pair. Only a spawn
// that STARTS a conversation can stall on an approval prompt, so probes and
// queries never take them.
func (spawn ClaudeSpawn) autonomy(prefs pfmconfig.ClaudePrefs) bool {
	if spawn.noAutonomy {
		return false
	}
	switch spawn.Purpose {
	case PurposeInteractive, PurposeResume:
		return prefs.PermissionMode == pfmconfig.PermissionBypass
	default:
		return false
	}
}

// binaryWord resolves the executable word and whether the shell form quotes
// it. An account-level binary override wins over the machine-level one; a
// value that came from the config file — or from an account override — is
// quoted, since it can carry spaces.
func (spawn ClaudeSpawn) binaryWord(prefs pfmconfig.ClaudePrefs) (string, bool) {
	if spawn.binary != "" {
		return spawn.binary, spawn.quoteBinary
	}
	value := prefs.Binary
	if value == "" {
		value = spawn.Machine.Claude.Binary
	}
	quote := spawn.Machine.Source("claude.binary") == pfmconfig.SourceFile ||
		value != spawn.Machine.Claude.Binary
	return value, quote
}
