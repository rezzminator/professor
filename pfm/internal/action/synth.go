package action

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// hygiene is the launch-environment strip every fleet-born process carries
// for both Claude and Codex. A chat born inside
// another chat's Bash tool inherits that chat's session identity, config dir
// and cache mode, so each one is unset and then re-decided by the launcher.
//
// The ANTHROPIC_*/CLAUDE_CODE_* tail is CC_ENDPOINT_UNSET:
// a shell pointed at a local translating proxy would otherwise hand the next
// launch a foreign endpoint, and it would answer from a foreign model under an
// Anthropic medal. The launcher's verdict is the account; the environment gets
// no vote.
// The list is the source of truth and the shell string is derived from it, so
// the two renderers in claude_spawn.go — a shell `env -u …` prefix and an
// environment slice for direct execution — can never drift apart.
var hygieneNames = []string{
	"CLAUDE_CODE_SESSION_ID",
	"CLAUDECODE",
	"CLAUDE_CODE_CHILD_SESSION",
	"CLAUDE_CONFIG_DIR",
	"CLAUDE_PROJECT_DIR",
	"ENABLE_PROMPT_CACHING_1H",
	"FORCE_PROMPT_CACHING_5M",
	"CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT",
	"ANTHROPIC_BASE_URL",
	"ANTHROPIC_AUTH_TOKEN",
	"ANTHROPIC_MODEL",
	"ANTHROPIC_SMALL_FAST_MODEL",
	"CLAUDE_CODE_AUTO_COMPACT_WINDOW",
	"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC",
	"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK",
	"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY",
}

var hygiene = envStripWords(hygieneNames)

// opencodeHygieneNames widens the fleet strip for OpenCode alone. An OpenCode
// born in a VS Code terminal inherits CLAUDE_CODE_SSE_PORT from the Claude
// Code extension; OpenCode then dials that port without the lock-file token,
// the extension closes the socket with 1008, and OpenCode's backoff resets on
// every open — one rejected handshake per second for the life of the chat.
// Claude keeps the variable: the CLI reads the token and the IDE link is wanted.
var opencodeHygieneNames = append(append([]string{}, hygieneNames...), "CLAUDE_CODE_SSE_PORT")

var opencodeHygiene = envStripWords(opencodeHygieneNames)

// envStripWords renders one strip list as the `env -u NAME …` prefix.
func envStripWords(names []string) string {
	var words strings.Builder
	words.WriteString("env")
	for _, name := range names {
		words.WriteString(" -u ")
		words.WriteString(name)
	}
	return words.String()
}

// LauncherRun is the one argv-preserving Claude spawn command used by the
// managed launcher. It shares the fleet hygiene list with picker/headless
// launches, but intentionally adds no autonomy or resume flags of its own.
// home and claude carry the systemPrompt choice; the launcher re-decides it
// every spawn (hygiene strips any inherited CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT).
func LauncherRun(
	realBinary string,
	args []string,
	configDir, home string,
	claude pfmconfig.ClaudePrefs,
) (string, error) {
	values := append([]string{realBinary, configDir}, args...)
	if hasNUL(values...) {
		return "", errors.New("launcher values cannot contain NUL")
	}
	if realBinary == "" {
		return "", errors.New("real Claude binary is empty")
	}
	// The launcher states its own binary and config dir — it has already
	// resolved the real Claude behind the shim, and its config dir came from
	// the environment rather than from an account row — so it hands the door a
	// single implicit account carrying only the prompt policy. Account 0 is
	// deliberately absent from that roster: the CLAUDE_CONFIG_DIR assignment
	// below is the launcher's, not an account's.
	return ClaudeSpawn{
		Purpose:           PurposeInteractive,
		Home:              home,
		Args:              args,
		Machine:           pfmconfig.Config{Claude: claude},
		explicitConfigDir: configDir,
		binary:            realBinary,
		quoteBinary:       true,
		noAutonomy:        true,
	}.ShellCommand()
}

// autonomyFlags is CC_AUTONOMY_FLAGS — the full-autonomy
// posture every path that STARTS a Claude chat carries. `--allow-…` is the
// enabling half (the harness refuses the bypass without it), `--dangerously-…`
// the acting half; both are required. Chats run unattended overnight, so a
// mid-task approval prompt is a stalled chat with nobody awake to clear it.
//
// Fresh and resumed Claude launches share ClaudeSpawn's policy. Codex carries
// its own bypass flag and never these.
const autonomyFlags = "--allow-dangerously-skip-permissions --dangerously-skip-permissions"

// Synthesize produces a deterministic action plan without touching tmux,
// processes, the filesystem, stdin, stdout, or /dev/tty.
func Synthesize(request Request) (Plan, error) {
	machine := normalizedMachineConfig(request.Config, request.Home)
	route, err := routeForKind(request.Row.Kind)
	if err != nil {
		return Plan{}, err
	}
	if request.Prompt != "" && route != NewClaude && route != NewCodex && route != NewOpenCode {
		return Plan{}, fmt.Errorf("initial prompt is not valid for %s route", request.Row.Kind)
	}
	switch route {
	case ResumeOpenCode:
		// One fleet-wide OpenCode seat: no per-account roster to satisfy, the
		// binary comes from the machine config's opencode section.
	case NewOpenCode:
		if _, found := machine.OpenCodeAccountByID(request.PrimaryAccount); !found {
			return Plan{}, fmt.Errorf(
				"requested OpenCode account %d is not in the configured roster",
				request.PrimaryAccount,
			)
		}
	case NewClaude, Agent, ResumeClaude:
		if _, found := machine.Account(request.PrimaryAccount); !found {
			return Plan{}, fmt.Errorf(
				"requested Claude account %d is not in the configured roster",
				request.PrimaryAccount,
			)
		}
	case NewCodex, ResumeCodex:
		if _, found := machine.CodexAccountByID(request.PrimaryAccount); !found {
			return Plan{}, fmt.Errorf(
				"requested Codex account %d is not in the configured roster",
				request.PrimaryAccount,
			)
		}
	}
	if hasNUL(
		request.Row.ID,
		request.Row.CWD,
		request.Row.Socket,
		request.Row.SessionName,
		request.Row.WindowName,
		request.Row.Name,
		request.Row.ConfigDir,
		request.Prompt,
		request.Home,
		request.FreshSocket,
	) {
		return Plan{}, errors.New("action values cannot contain NUL")
	}

	plan := Plan{Route: route}
	switch route {
	case NewClaude:
		if request.Row.CWD == "" || request.FreshSocket == "" {
			return Plan{}, errors.New("new Claude action requires cwd and fresh socket")
		}
		var arguments []string
		if request.Prompt != "" {
			arguments = append(arguments, request.Prompt)
		}
		run, err := claudeCommand(
			PurposeInteractive,
			request.Home,
			request.PrimaryAccount,
			request.Cache1H,
			machine,
			arguments...)
		if err != nil {
			return Plan{}, err
		}
		plan.Run = run
		plan = onChatServer(plan, request, machine, pfmengine.Claude)
	case NewCodex:
		if request.Row.CWD == "" {
			return Plan{}, errors.New("new Codex action requires a project directory")
		}
		account, _ := machine.CodexAccountByID(request.PrimaryAccount)
		plan.Line = "(cd -- " + Quote(request.Row.CWD) + " && CODEX_HOME=" +
			Quote(account.Home) + " cx"
		if request.Prompt != "" {
			plan.Line += " " + Quote(request.Prompt)
		}
		plan.Line += ")"
	case NewOpenCode:
		if request.Row.CWD == "" || request.FreshSocket == "" {
			return Plan{}, errors.New("new OpenCode action requires cwd and fresh socket")
		}
		var command strings.Builder
		command.WriteString(opencodeHygiene)
		command.WriteByte(' ')
		command.WriteString(binaryWord(
			machine.OpenCode.Binary,
			pfmengine.MustLookup(pfmengine.OpenCode).Binary,
			machine.Source("opencode.binary") == pfmconfig.SourceFile,
		))
		command.WriteByte(' ')
		command.WriteString(Quote(request.Row.CWD))
		if request.Prompt != "" {
			command.WriteString(" --prompt ")
			command.WriteString(Quote(request.Prompt))
		}
		plan.Run = command.String()
		plan = onChatServer(plan, request, machine, pfmengine.OpenCode)
	case ResumeOpenCode:
		if request.Row.ID == "" || request.Row.CWD == "" ||
			request.FreshSocket == "" {
			return Plan{}, errors.New(
				"OpenCode resume requires id, cwd, and fresh socket",
			)
		}
		var command strings.Builder
		command.WriteString(opencodeHygiene)
		command.WriteByte(' ')
		command.WriteString(binaryWord(
			machine.OpenCode.Binary,
			pfmengine.MustLookup(pfmengine.OpenCode).Binary,
			machine.Source("opencode.binary") == pfmconfig.SourceFile,
		))
		command.WriteString(" --session ")
		command.WriteString(Quote(request.Row.ID))
		command.WriteByte(' ')
		command.WriteString(Quote(request.Row.CWD))
		plan.Run = opencodeContinuityBanner(request.Row) + command.String()
		plan = onChatServer(plan, request, machine, pfmengine.OpenCode)
	case Live:
		if request.Row.Socket == "" {
			return Plan{}, errors.New("live action requires a socket")
		}
		plan.Line = attachLine(
			request.Row.Socket,
			liveTarget(request.Row),
			request.Bunker,
		)
	case Agent:
		if request.Row.ID == "" || request.Row.CWD == "" ||
			request.FreshSocket == "" {
			return Plan{}, errors.New(
				"agent action requires id, cwd, and fresh socket",
			)
		}
		owningConfig := request.Row.ConfigDir
		if filepath.Clean(owningConfig) == filepath.Join(request.Home, ".claude") {
			owningConfig = ""
		}
		agentRun := agentCommand(
			request.Cache1H,
			request.Row.ID,
			request.Row.CWD,
			machine.Path,
			owningConfig,
		)
		resume, err := claudeCommand(
			PurposeResume,
			request.Home,
			request.PrimaryAccount,
			request.Cache1H,
			machine,
			"--resume",
			request.Row.ID,
		)
		if err != nil {
			return Plan{}, err
		}
		plan.Run = agentRun +
			" || { echo; echo " +
			Quote("agent router failed — resuming fresh:") +
			"; exec " + resume + "; }"
		plan = onChatServer(plan, request, machine, pfmengine.Claude)
	case ResumeClaude:
		if request.Row.ID == "" || request.Row.CWD == "" ||
			request.FreshSocket == "" {
			return Plan{}, errors.New(
				"resuming Claude requires id, cwd, and fresh socket",
			)
		}
		resume, err := claudeCommand(
			PurposeResume,
			request.Home,
			request.PrimaryAccount,
			request.Cache1H,
			machine,
			"--resume",
			request.Row.ID,
		)
		if err != nil {
			return Plan{}, err
		}
		agent := agentCommand(
			request.Cache1H,
			request.Row.ID,
			request.Row.CWD,
			machine.Path,
		)
		plan.Run = resume +
			" || { echo; echo " +
			Quote("resume refused — session is live elsewhere:") +
			"; " + agent + "; }"
		plan = onChatServer(plan, request, machine, pfmengine.Claude)
	case ResumeCodex:
		if request.Row.ID == "" || request.Row.CWD == "" ||
			request.FreshSocket == "" {
			return Plan{}, errors.New(
				"resuming Codex requires id, cwd, and fresh socket",
			)
		}
		plan.Run = continuityBanner(request.Row) +
			codexCommandFor(machine, request.PrimaryAccount, "resume", request.Row.ID)
		plan = onChatServer(plan, request, machine, pfmengine.Codex)
	}
	return plan, nil
}

func routeForKind(kind compose.Kind) (Route, error) {
	switch kind {
	case compose.NewClaude:
		return NewClaude, nil
	case compose.NewCodex:
		return NewCodex, nil
	case compose.NewOpenCode:
		return NewOpenCode, nil
	case compose.LiveClaude, compose.LiveCodex, compose.LiveOpenCode, compose.LiveSplit:
		return Live, nil
	// A booting row carries no other identity than its socket, so Enter can
	// only ever attach it — the same Live route an ordinary live row takes,
	// which is exactly the "no other operations" the fix promises: it is
	// never resumable (no transcript exists yet to resume) and Open's own
	// mismatched-account gate and dead-socket resume fallback stay unreached
	// because this Kind is deliberately absent from the Kind list Open()
	// special-cases (executor.go).
	case compose.Booting:
		return Live, nil
	case compose.Agent:
		return Agent, nil
	case compose.ResumeClaude:
		return ResumeClaude, nil
	case compose.ResumeCodex:
		return ResumeCodex, nil
	case compose.ResumeOpenCode:
		return ResumeOpenCode, nil
	default:
		return 0, fmt.Errorf("unsupported row kind %s", kind)
	}
}

func claudeCommand(
	purpose Purpose,
	home string,
	account int,
	cache1H bool,
	machine pfmconfig.Config,
	args ...string,
) (string, error) {
	return claudeCommandWith(purpose, hygieneNames, home, account, cache1H, machine, args...)
}

// claudeCommandWith is claudeCommand over a caller-chosen environment strip,
// so the headless route can widen the strip without owning a second copy of
// the account, cache and autonomy decisions. Both are now thin wrappers over
// ClaudeSpawn — the one door — and exist only because the synthesis routes
// read better as one expression inside Synthesize.
func claudeCommandWith(
	purpose Purpose,
	strip []string,
	home string,
	account int,
	cache1H bool,
	machine pfmconfig.Config,
	args ...string,
) (string, error) {
	return ClaudeSpawn{
		Purpose: purpose,
		Account: account,
		Cache1H: cache1H,
		Args:    args,
		Home:    home,
		Machine: machine,
		strip:   strip,
	}.ShellCommand()
}

// ProfessorPromptPath is the composed Claude prompt `pfm install` stages
// under the managed root; claude.systemPrompt "professor" points every
// managed launch at it via --system-prompt-file.
func ProfessorPromptPath(home string) string {
	return paths.HarnessPromptPath(home, pfmengine.Claude)
}

// continuityBanner is the first thing a resumed Codex pane prints, above
// Codex's own boot chrome.
//
// A resume lands on a fresh tmux server, so the pane opens with an empty
// scrollback and Codex repaints its first-run screen. That screen is
// indistinguishable from a cold start, and a reader who trusts it concludes
// the seat lost its memory — an orchestrator re-briefed a seat carrying 2.5M
// tokens as if it were a replacement.
//
// The banner therefore says what is CHECKABLE and asserts nothing it cannot
// see. It must never claim the context survived: whether Codex rehydrates a
// thread is its own decision, made after this line is printed, and a
// `history_mode = paginated` thread reloads from Codex's history store rather
// than from its rollout — that store can hold a handful of items for a
// conversation whose rollout is complete, and the resume then comes up empty
// while the transcript on disk is whole. A banner that asserted continuity
// there would be the very failure it exists to prevent: a surface reporting a
// state it never verified. So it names the id, points at the transcript, and
// tells the reader which pixel decides the question.
//
// Codex writes its TUI into the normal buffer rather than the alternate
// screen, so these lines stay the oldest entries in the pane's scrollback for
// the life of the seat — the exact position a reader checks when asking "did
// this start fresh?".
func continuityBanner(row compose.Row) string {
	lines := []string{
		"═══ RESUME of " + row.ID + " — verify before trusting this pane ═══",
		"    Loaded? Read the status line: no context/token counts means Codex" +
			" did NOT rehydrate this thread.",
		"    Scrollback is never restored — the prior tmux server is reaped on" +
			" kill.",
		"    Came up empty? The rollout is whole: pfm chat recover " + row.ID,
	}
	if row.Path != "" {
		lines = append(
			lines,
			"    Transcript, complete whatever this pane shows: "+row.Path,
		)
	}
	var banner strings.Builder
	banner.WriteString("printf '%s\\n'")
	for _, line := range lines {
		banner.WriteByte(' ')
		banner.WriteString(Quote(line))
	}
	banner.WriteString("; ")
	return banner.String()
}

// opencodeContinuityBanner is the OpenCode twin of continuityBanner, minus
// everything Codex-specific. It names the id and states what is checkable;
// whether OpenCode rehydrated the session is its own decision, so the banner
// asserts nothing about memory.
func opencodeContinuityBanner(row compose.Row) string {
	lines := []string{
		"═══ RESUME of " + row.ID + " — verify before trusting this pane ═══",
		"    History loaded? Check the session list above the composer before",
		"    treating this seat as continuing its prior work.",
	}
	var banner strings.Builder
	banner.WriteString("printf '%s\\n'")
	for _, line := range lines {
		banner.WriteByte(' ')
		banner.WriteString(Quote(line))
	}
	banner.WriteString("; ")
	return banner.String()
}

func codexCommandFor(machine pfmconfig.Config, account int, args ...string) string {
	return codexCommandWithAccount(hygiene, machine, account, args...)
}

func codexCommandWithAccount(
	environmentStrip string,
	machine pfmconfig.Config,
	account int,
	args ...string,
) string {
	var command strings.Builder
	policy := machine.EffectiveCodex(account)
	command.WriteString(environmentStrip)
	if selected, found := machine.CodexAccountByID(account); found {
		command.WriteString(" CODEX_HOME=")
		command.WriteString(Quote(selected.Home))
	}
	command.WriteByte(' ')
	command.WriteString(binaryWord(
		policy.Binary,
		pfmengine.MustLookup(pfmengine.Codex).Binary,
		policy.Binary != "" && policy.Binary != pfmengine.MustLookup(pfmengine.Codex).Binary,
	))
	if policy.Yolo {
		command.WriteString(" --dangerously-bypass-approvals-and-sandbox")
	} else {
		command.WriteString(" --sandbox workspace-write")
	}
	for _, argument := range args {
		command.WriteByte(' ')
		command.WriteString(Quote(argument))
	}
	return command.String()
}

func normalizedMachineConfig(machine pfmconfig.Config, home string) pfmconfig.Config {
	if machine.Version == pfmconfig.Version &&
		(len(machine.Accounts) != 0 || len(machine.CodexAccounts) != 0 || len(machine.OpenCodeAccounts) != 0) {
		return machine
	}
	return pfmconfig.Defaults(home, nil)
}

func binaryWord(value, fallback string, quote bool) string {
	if value == "" {
		value = fallback
	}
	if quote {
		return Quote(value)
	}
	return value
}

// claudeBinaryWord is the raw executable word claudeCommandWith embeds —
// stated separately so a plan can carry it for the spawn preflight without a
// second copy of the binary decision drifting from the command itself.
func claudeBinaryWord(machine pfmconfig.Config) string {
	return binaryWord(machine.Claude.Binary, pfmengine.MustLookup(pfmengine.Claude).Binary, false)
}

// codexBinaryWord is the raw executable word codexCommandWithAccount embeds.
func codexBinaryWord(machine pfmconfig.Config, account int) string {
	return binaryWord(machine.EffectiveCodex(account).Binary, pfmengine.MustLookup(pfmengine.Codex).Binary, false)
}

func agentCommand(
	cache1H bool,
	id, cwd, machineConfigPath string,
	configDir ...string,
) string {
	var command strings.Builder
	command.WriteString(hygiene)
	if cache1H {
		command.WriteString(" ENABLE_PROMPT_CACHING_1H=1")
	} else {
		command.WriteString(" FORCE_PROMPT_CACHING_5M=1")
	}
	command.WriteString(" pfm")
	if machineConfigPath != "" {
		command.WriteString(" --config ")
		command.WriteString(Quote(machineConfigPath))
	}
	command.WriteString(" internal agent-open --id ")
	command.WriteString(Quote(id))
	command.WriteString(" --cwd ")
	command.WriteString(Quote(cwd))
	if len(configDir) != 0 && configDir[0] != "" {
		command.WriteString(" --config ")
		command.WriteString(Quote(configDir[0]))
	}
	return command.String()
}

// onChatServer puts the plan's run on a fresh server the executor creates
// detached through the one chat-server creator (spawn.TmuxSpawner.NewSession),
// born with the engine's short name as its window, and makes the eval line
// the attach to it. The line never creates a server itself: an attached
// `tmux new-session` there was a creator with no title policy and a window
// its pane command renamed.
func onChatServer(plan Plan, request Request, machine pfmconfig.Config, engine pfmengine.ID) Plan {
	titles := machine.Tmux.Titles
	plan.ChatServer = &ChatServer{
		Socket: request.FreshSocket,
		CWD:    request.Row.CWD,
		Window: pfmengine.MustLookup(engine).Short,
		Run:    plan.Run,
		Titles: &titles,
	}
	plan.Line = attachLine(request.FreshSocket, request.FreshSocket, request.Bunker)
	return plan
}

func attachLine(socket, target string, bunker bool) string {
	prefix := "TMUX= "
	if bunker {
		prefix += "exec "
	}
	line := prefix + "tmux -L " + Quote(socket) + " attach"
	if target != "" {
		line += " -t " + Quote(target)
	}
	return line
}

func liveTarget(row compose.Row) string {
	if row.Kind == compose.LiveCodex {
		if row.SessionName != "" && row.WindowName != "" {
			return row.SessionName + ":" + row.WindowName
		}
	}
	if row.SessionName != "" {
		return row.SessionName
	}
	return row.Socket
}
