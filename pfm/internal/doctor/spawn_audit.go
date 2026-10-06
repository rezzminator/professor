package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/agentrole"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/usagehook"
)

// spawnVerdict is one live chat's standing against the configured prompt
// policy.
type spawnVerdict string

const (
	// spawnInjected: the launch carries the policy's prompt material.
	spawnInjected spawnVerdict = "INJECTED"
	// spawnPredatesLayer: the chat carries an older launch or hook set;
	// only a reload brings it through the current spawn door.
	spawnPredatesLayer spawnVerdict = "PREDATES-LAYER"
	// spawnViolation: required launch material is missing; the reason
	// distinguishes a bypassed door from an unavailable composed prompt.
	spawnViolation spawnVerdict = "VIOLATION"
)

type rolePromptOutcome string

const (
	rolePromptOK          rolePromptOutcome = "ROLE-OK"
	rolePromptMismatch    rolePromptOutcome = "ROLE-MISMATCH"
	rolePromptCheckFailed rolePromptOutcome = "ROLE-CHECK-FAILED"
)

type rolePromptRead struct {
	role   string
	prompt string
	found  bool
	err    error
}

// spawnObservation is everything the classifier is allowed to see: one live
// pane's Claude process as /proc reports it.
type spawnObservation struct {
	Socket   string
	PID      int
	Argv     []string
	Parsed   claudelaunch.Parsed
	ParseErr error
	// Environ is the process's own environment. A nil map means it could not
	// be read, which is NOT the same as an empty one — the classifier says so.
	Environ map[string]string
	// EnvironErr is the reason Environ is nil.
	EnvironErr error
	// StartedUnix is the process birth time in epoch seconds; 0 means unknown.
	StartedUnix int64
}

// classifySpawn grades the decoded launch against the account's prompt policy and registry hooks.
func classifySpawn(
	parsed claudelaunch.Parsed,
	observation spawnObservation,
	prefs config.ClaudePrefs,
	home string,
	layerStampUnix int64,
	promptErr error,
) (spawnVerdict, string) {
	missing := ""
	switch promptPolicyName(prefs.SystemPrompt) {
	case config.SystemPromptProfessor:
		if parsed.PromptFile == "" {
			missing = "no --system-prompt-file prompt material"
			if promptErr != nil {
				return spawnViolation, missing + fmt.Sprintf(
					" — composed prompt unavailable (%v); every door omits the flag until it exists: update or restore the clone, then reload",
					promptErr,
				)
			}
		}
	case config.SystemPromptLean:
		if parsed.SettingsEnv["CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT"] != "1" {
			missing = "lean prompt missing from the --settings payload"
		}
	}
	if missing == "" && (parsed.Settings == nil || parsed.Settings["outputStyle"] != "default") {
		missing = "missing --settings outputStyle default"
	}
	if missing == "" && !sameSpawnHooks(parsed.Hooks, claudelaunch.HookTemplates(home)) {
		return spawnPredatesLayer, "hook set differs from the registry — reload to carry it"
	}
	if missing == "" {
		switch promptPolicyName(prefs.SystemPrompt) {
		case config.SystemPromptProfessor:
			return spawnInjected, "argv carries --system-prompt-file and registry payload"
		case config.SystemPromptLean:
			return spawnInjected, "lean prompt armed in the --settings payload and registry hooks"
		default:
			return spawnInjected, "production payload and registry hooks"
		}
	}
	if age, older := predatesLayer(observation, layerStampUnix); older {
		return spawnPredatesLayer, missing + fmt.Sprintf(" (born %s before the current spawn door)", age)
	}
	if parsed.Resume != "" && parsed.Settings == nil {
		return spawnPredatesLayer, "resumed argv with no registry payload — reborn before the door"
	}
	return spawnViolation, missing + " — some spawn site bypassed the door"
}

func sameSpawnHooks(actual, expected []claudelaunch.Hook) bool {
	if len(actual) != len(expected) {
		return false
	}
	type key struct {
		event, matcher, command string
		async                   bool
	}
	counts := map[key]int{}
	for _, hook := range expected {
		counts[key{hook.Event, hook.Matcher, hook.Command, hook.Async}]++
	}
	for _, hook := range actual {
		counts[key{hook.Event, hook.Matcher, hook.Command, hook.Async}]--
	}
	for _, count := range counts {
		if count != 0 {
			return false
		}
	}
	return true
}

// classifyRolePrompt separates a readable-but-invalid role channel from a
// channel that could not be read. The distinction is visible because a role
// mismatch is a finding about bytes we saw, while a failed check proves
// nothing about what the seat carries.
func classifyRolePrompt(path string, read rolePromptRead) (rolePromptOutcome, string) {
	if !read.found {
		return rolePromptCheckFailed, fmt.Sprintf("%s does not exist", path)
	}
	if read.err != nil {
		var pathErr *os.PathError
		if errors.As(read.err, &pathErr) {
			return rolePromptCheckFailed, fmt.Sprintf("could not read %s (%v)", path, read.err)
		}
		return rolePromptMismatch, fmt.Sprintf("%s does not match the role prompt format: %v", path, read.err)
	}
	if read.role == "" {
		return rolePromptMismatch, fmt.Sprintf("%s has an empty role marker", path)
	}
	if strings.TrimSpace(read.prompt) == "" {
		return rolePromptMismatch, fmt.Sprintf("%s has role=%s but an empty prompt channel", path, read.role)
	}
	return rolePromptOK, fmt.Sprintf("%s carries role=%s and a non-empty prompt channel", path, read.role)
}

// printSpawnRoleAudit emits the additional role-channel audit. Ordinary
// staged-prompt seats are deliberately silent here; without a role seat there
// is no role-fleet claim to make and therefore no summary line.
func printSpawnRoleAudit(stdout io.Writer, observations []spawnObservation) int {
	counts := map[rolePromptOutcome]int{}
	roleSeats := 0
	for _, observation := range observations {
		parsed, err := observation.Parsed, observation.ParseErr
		if parsed.Settings == nil && parsed.PromptFile == "" && err == nil {
			parsed, err = claudelaunch.Parse(observation.Argv)
		}
		if err != nil || !agentrole.IsSeatPromptPath(parsed.PromptFile) {
			continue
		}
		path := parsed.PromptFile
		roleSeats++
		role, prompt, found, err := agentrole.ReadSeatPromptFile(path)
		outcome, reason := classifyRolePrompt(path, rolePromptRead{
			role: role, prompt: prompt, found: found, err: err,
		})
		counts[outcome]++
		fmt.Fprintf(
			stdout,
			"doctor: spawn-audit: %s %s pid=%d file=%s — %s\n",
			outcome,
			observation.Socket,
			observation.PID,
			path,
			reason,
		)
	}
	if roleSeats == 0 {
		return 0
	}
	fmt.Fprintf(
		stdout,
		"doctor: spawn-audit: role-seats=%d ok=%d mismatched=%d unreadable=%d\n",
		roleSeats,
		counts[rolePromptOK],
		counts[rolePromptMismatch],
		counts[rolePromptCheckFailed],
	)
	if counts[rolePromptMismatch] != 0 || counts[rolePromptCheckFailed] != 0 {
		return 1
	}
	return 0
}

// predatesLayer reports how long before the current spawn door went live this
// process was born, and whether the age signal decided anything at all. A missing
// birth time or a missing stamp (either one 0) leaves age unusable: the
// caller must then fall through to a signal it can actually read, never treat
// an unreadable age as "not old".
func predatesLayer(observation spawnObservation, layerStampUnix int64) (time.Duration, bool) {
	if observation.StartedUnix <= 0 || layerStampUnix <= 0 ||
		observation.StartedUnix >= layerStampUnix {
		return 0, false
	}
	return time.Duration(layerStampUnix-observation.StartedUnix) * time.Second, true
}

// printSpawnAuditDoctor audits every live Claude chat against the configured
// system-prompt policy and reports one line per seat plus a verdict summary.
//
// The three failure surfaces are deliberately distinct: a clean audit, an
// audit with nothing to look at, and an audit that could not run. A probe that
// could not run never renders as "no violations".
func printSpawnAuditDoctor(
	ctx context.Context,
	stdout io.Writer,
	resolved paths.Values,
	machine config.Config,
	primary int,
) int {
	return printSpawnAuditDoctorWithClock(ctx, stdout, resolved, machine, primary, clock.Real)
}

func printSpawnAuditDoctorWithClock(
	ctx context.Context,
	stdout io.Writer,
	resolved paths.Values,
	machine config.Config,
	primary int,
	clk clock.Clock,
) int {
	prefs := machine.EffectiveClaude(primary)
	policy := promptPolicyName(prefs.SystemPrompt)
	for _, account := range machine.Accounts {
		if promptPolicyName(machine.EffectiveClaude(account.ID).SystemPrompt) != policy {
			policy = "per-account"
			break
		}
	}

	observations, unread, err := spawnObservationsProbe(ctx, resolved, machine, clk)
	if err != nil {
		fmt.Fprintf(stdout, "doctor: spawn-audit: CHECK FAILED to run (%v) — live chats unaudited\n", err)
		return 1
	}

	stamp, stampSignal, promptErr := spawnDoorStamp(resolved.Home)
	if len(observations) == 0 {
		fmt.Fprintf(
			stdout,
			"doctor: spawn-audit: policy=%s — no live Claude chats found\n",
			policy,
		)
		return spawnAuditUnreadWarnings(stdout, unread)
	}

	sort.Slice(observations, func(left, right int) bool {
		if observations[left].Socket != observations[right].Socket {
			return observations[left].Socket < observations[right].Socket
		}
		return observations[left].PID < observations[right].PID
	})
	counts := map[spawnVerdict]int{}
	undecodable := 0
	for index := range observations {
		observation := &observations[index]
		if warning := decodeSpawn(observation); warning != "" {
			unread = append(unread, warning)
			undecodable++
			continue
		}
		accountID, accountReason := spawnAccount(machine, primary, *observation)
		verdict, reason := classifySpawn(
			observation.Parsed,
			*observation,
			machine.EffectiveClaude(accountID),
			resolved.Home,
			stamp,
			promptErr,
		)
		if accountReason != "" {
			reason += " (" + accountReason + ")"
		}
		counts[verdict]++
		fmt.Fprintf(
			stdout,
			"doctor: spawn-audit: %s %s pid=%d — %s\n",
			verdict,
			observation.Socket,
			observation.PID,
			reason,
		)
	}
	roleWarnings := printSpawnRoleAudit(stdout, observations)
	fmt.Fprintf(
		stdout,
		"doctor: spawn-audit: policy=%s chats=%d injected=%d predates-layer=%d violations=%d undecodable=%d (age signal: %s)\n",
		policy,
		len(observations),
		counts[spawnInjected],
		counts[spawnPredatesLayer],
		counts[spawnViolation],
		undecodable,
		stampSignal,
	)
	warnings := spawnAuditUnreadWarnings(stdout, unread)
	if counts[spawnViolation] != 0 {
		warnings++
	}
	warnings += roleWarnings
	return warnings
}

func decodeSpawn(observation *spawnObservation) string {
	observation.Parsed, observation.ParseErr = claudelaunch.Parse(observation.Argv)
	if observation.ParseErr != nil {
		return fmt.Sprintf("%s pid=%d: argv undecodable: %v", observation.Socket, observation.PID, observation.ParseErr)
	}
	return ""
}

func spawnAccount(machine config.Config, primary int, observation spawnObservation) (int, string) {
	if len(machine.Accounts) == 0 {
		return primary, ""
	}
	if observation.Environ == nil {
		return primary, "account environment unreadable; graded against primary"
	}
	dir := observation.Environ["CLAUDE_CONFIG_DIR"]
	for _, account := range machine.Accounts {
		if usagehook.SameConfigDir(account.ConfigDir, dir) {
			return account.ID, ""
		}
	}
	return primary, "account unmatched; graded against primary"
}

// spawnAuditUnreadWarnings reports the sockets and panes the audit could not
// read. They are never folded into the clean count — an unread seat is an
// unanswered question, not a passing one.
func spawnAuditUnreadWarnings(stdout io.Writer, unread []string) int {
	if len(unread) == 0 {
		return 0
	}
	sort.Strings(unread)
	fmt.Fprintf(
		stdout,
		"doctor: spawn-audit: %d seat(s) could NOT be audited: %s\n",
		len(unread),
		strings.Join(unread, "; "),
	)
	return 1
}

func promptPolicyName(value string) string {
	if value == "" {
		return config.SystemPromptProduction
	}
	return value
}

// spawnDoorExecutable locates the pfm binary whose spawn doors the audit
// judges; tests point it at a fixture.
var spawnDoorExecutable = os.Executable

var spawnObservationsProbe = liveClaudeSpawns

// spawnDoorStamp is the moment this host's CURRENT spawn door went live: the
// later of the clone's composed professor prompt mtime (the prompt layer) and the
// running pfm binary's mtime (the argv every door builds). One stamp cannot
// be the prompt alone: the --settings output-style flag shipped releases
// after the prompt, and an install that leaves the prompt's bytes unchanged
// never moves its mtime — so every chat launched by an older pfm read as born
// "after the layer" and indicted a door that was never broken. Only a seat
// born after BOTH can blame the door now installed; an older seat carries the
// argv of the pfm that launched it, and a reload is its fix. The signal names
// every input, so a reader knows what the age claim rests on.
func spawnDoorStamp(home string) (int64, string, error) {
	var stamp int64
	var sources, failures []string
	consider := func(label, path string, err error) error {
		if err == nil {
			var info os.FileInfo
			info, err = os.Stat(path)
			if err == nil && label == "prompt layer" {
				if !info.Mode().IsRegular() {
					err = fmt.Errorf("%s is not a regular file", path)
				} else {
					_, err = os.ReadFile(path)
				}
			}
			if err == nil {
				stamp = max(stamp, info.ModTime().Unix())
				sources = append(sources, "mtime of "+path)
				return nil
			}
		}
		failures = append(failures, fmt.Sprintf("%s: %v", label, err))
		return err
	}
	promptPath, promptErr := action.ProfessorPromptPath(home)
	promptErr = consider("prompt layer", promptPath, promptErr)
	executable, err := spawnDoorExecutable()
	consider("pfm binary", executable, err)
	if stamp == 0 {
		return 0, fmt.Sprintf("unavailable (%s) — age never decided a verdict", strings.Join(failures, "; ")), promptErr
	}
	signal := fmt.Sprintf(
		"%s, the later of %s",
		time.Unix(stamp, 0).Format(time.RFC3339),
		strings.Join(sources, " and "),
	)
	if len(failures) != 0 {
		signal += " (unreadable: " + strings.Join(failures, "; ") + ")"
	}
	return stamp, signal, promptErr
}

// liveClaudeSpawns enumerates the fleet's own Claude sockets through the same
// gather probe the picker uses, then resolves each pane's engine process from
// /proc. It is never a name scan of the process table: `pgrep -f` matches the
// searcher's own wrapper shell, and a bare binary-name match cannot tell a
// fleet chat from any other process on the box running the same executable.
func liveClaudeSpawns(
	ctx context.Context,
	resolved paths.Values,
	machine config.Config,
	clk clock.Clock,
) ([]spawnObservation, []string, error) {
	client := gather.TmuxProbe{TmuxTmpDir: filepath.Dir(resolved.TmuxDir)}
	if clk == nil {
		clk = clock.Real
	}
	probe, err := gather.ProbeTmuxReadOnly(ctx, resolved.TmuxDir, client, clk.Now())
	if err != nil {
		return nil, nil, fmt.Errorf("probe tmux sockets under %s: %w", resolved.TmuxDir, err)
	}
	unread := append([]string(nil), probe.ProbeWarnings...)
	proc := gather.NewProcFS(resolved.ProcRoot)
	binary := claudeBinaryName(machine)

	observations := make([]spawnObservation, 0, len(probe.Panes))
	for index := range probe.Panes {
		pane := &probe.Panes[index]
		if id, ok := pfmengine.FromSocket(pane.Socket); !ok || id != pfmengine.Claude {
			continue
		}
		pid, argv, found, resolveErr := resolveClaudeProcess(proc, pane.PID, binary)
		if resolveErr != nil {
			unread = append(unread, fmt.Sprintf("%s pane pid %d: %v", pane.Socket, pane.PID, resolveErr))
			continue
		}
		if !found {
			// The pane is alive but no Claude process sits under it — a shell
			// the user dropped to, or a chat mid-exit. Not a finding.
			continue
		}
		observation := spawnObservation{Socket: pane.Socket, PID: pid, Argv: argv}
		environ, environErr := proc.Environ(pid)
		if environErr != nil {
			observation.EnvironErr = environErr
		} else {
			observation.Environ = environ
		}
		if birth, ok := proc.(gather.ProcBirth); ok {
			if started, birthErr := birth.Birth(pid); birthErr == nil {
				observation.StartedUnix = started
			}
		}
		observations = append(observations, observation)
	}
	return observations, unread, nil
}

func claudeBinaryName(machine config.Config) string {
	if machine.Claude.Binary != "" {
		return machine.Claude.Binary
	}
	return pfmengine.MustLookup(pfmengine.Claude).Binary
}

// resolveClaudeProcess finds the Claude process a pane runs. tmux may put a
// shell between the pane and the engine, so the pane pid is checked first and
// its descendants after — bounded, because an unbounded walk over a live
// process table is how a doctor check becomes the slowest thing in the run.
func resolveClaudeProcess(proc gather.ProcFS, panePID int, binary string) (int, []string, bool, error) {
	if panePID <= 0 {
		return 0, nil, false, nil
	}
	if argv, err := proc.Cmdline(panePID); err == nil && gather.IsClaudeCommand(argv, binary) {
		return panePID, argv, true, nil
	}
	pids, err := proc.PIDs()
	if err != nil {
		return 0, nil, false, fmt.Errorf("read process table: %w", err)
	}
	children := make(map[int][]int, len(pids))
	for _, pid := range pids {
		stat, err := proc.Stat(pid)
		if err != nil || stat.ParentPID <= 0 {
			continue
		}
		children[stat.ParentPID] = append(children[stat.ParentPID], pid)
	}
	frontier := []int{panePID}
	for depth := 0; depth < 4 && len(frontier) != 0; depth++ {
		next := make([]int, 0, len(frontier))
		for _, pid := range frontier {
			for _, child := range children[pid] {
				if argv, err := proc.Cmdline(child); err == nil && gather.IsClaudeCommand(argv, binary) {
					return child, argv, true, nil
				}
				next = append(next, child)
			}
		}
		frontier = next
	}
	return 0, nil, false, nil
}
