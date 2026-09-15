package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"hostops/pfm/internal/action"
	"hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/paths"
)

// spawnVerdict is one live chat's standing against the configured prompt
// policy.
type spawnVerdict string

const (
	// spawnInjected: the launch carries the policy's prompt material.
	spawnInjected spawnVerdict = "INJECTED"
	// spawnPredatesLayer: the chat was born before the prompt layer existed,
	// so its flagless argv is history rather than a defect. It is stated
	// separately from a clean verdict — the seat IS running the CLI's own
	// prompt and only a reload fixes it.
	spawnPredatesLayer spawnVerdict = "PREDATES-LAYER"
	// spawnViolation: born after the layer, still flagless. Some spawn site
	// bypassed the door.
	spawnViolation spawnVerdict = "VIOLATION"
)

// spawnObservation is everything the classifier is allowed to see: one live
// pane's Claude process as /proc reports it.
type spawnObservation struct {
	Socket string
	PID    int
	Argv   []string
	// Environ is the process's own environment. A nil map means it could not
	// be read, which is NOT the same as an empty one — the classifier says so.
	Environ map[string]string
	// EnvironErr is the reason Environ is nil.
	EnvironErr error
	// StartedUnix is the process birth time in epoch seconds; 0 means unknown.
	StartedUnix int64
}

// classifySpawn is the pure verdict. layerStampUnix is the moment this host's
// current spawn door went live (spawnDoorStamp); 0 means the stamp is
// unavailable and the age signal is unusable.
//
// The reason string names WHICH signal decided, because the two flagless
// outcomes are indistinguishable without it: "old chat" and "broken spawn
// site" look identical in argv.
func classifySpawn(observation spawnObservation, layerStampUnix int64) (spawnVerdict, string) {
	promptReason := ""
	for _, argument := range observation.Argv {
		if argument == "--system-prompt-file" ||
			strings.HasPrefix(argument, "--system-prompt-file=") {
			promptReason = "argv carries --system-prompt-file"
			break
		}
	}
	if promptReason == "" && observation.Environ["CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT"] == "1" {
		promptReason = "lean prompt armed in the process environment"
	}
	if promptReason != "" {
		// The staged prompt is meant to be the ONLY persona layer. A launch
		// carrying it without also disabling Claude Code's own output style
		// still double-applies a persona — the exact defect this flag exists
		// to close — so it is reported as its own outcome, distinct from a
		// spawn site that injected nothing at all.
		if !argvCarriesOutputStyleDefault(observation.Argv) {
			// ...but only a seat born AFTER the current spawn door went live
			// can be blamed on a spawn site. An older chat carries the argv of the
			// pfm that launched it, and predates this flag exactly as a
			// flagless chat predates the prompt itself — a reload is the fix,
			// not a bug hunt. Skipping this check would accuse every live
			// seat on the host the moment the flag ships.
			if age, older := predatesLayer(observation, layerStampUnix); older {
				return spawnPredatesLayer, fmt.Sprintf(
					"%s but argv is missing --settings %s, and the process started %s before this host's current spawn door was installed — reload to carry it",
					promptReason,
					pfmengine.OutputStyleDefaultSettings,
					age,
				)
			}
			return spawnViolation, fmt.Sprintf(
				"%s but argv is missing --settings %s — Claude Code's own output style can still double-apply on top of it",
				promptReason,
				pfmengine.OutputStyleDefaultSettings,
			)
		}
		return spawnInjected, promptReason
	}
	if age, older := predatesLayer(observation, layerStampUnix); older {
		return spawnPredatesLayer, fmt.Sprintf(
			"process started %s before this host's current spawn door was installed", age,
		)
	}
	for _, argument := range observation.Argv {
		if argument == "--resume" || strings.HasPrefix(argument, "--resume=") {
			return spawnPredatesLayer, "resumed argv with no prompt material — reborn before the door"
		}
	}
	if observation.Environ == nil {
		// A process whose environment could not be read cannot be cleared: the
		// lean arm lives ONLY there. Calling it a violation would be a guess,
		// and calling it clean would be a lie, so it is reported as the
		// unaudited seat it is.
		return spawnViolation, fmt.Sprintf(
			"fresh flagless argv AND the environment could not be read (%v) — verdict unproven",
			observation.EnvironErr,
		)
	}
	return spawnViolation, "fresh launch with no prompt material — some spawn site bypassed the door"
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

// argvCarriesOutputStyleDefault reports whether argv disables Claude Code's
// own output style the way every fleet spawn door does: `--settings
// {"outputStyle":"default"}`, as one word pair or as `--settings=<json>`.
func argvCarriesOutputStyleDefault(argv []string) bool {
	for index, argument := range argv {
		if argument == "--settings" {
			if index+1 < len(argv) && argv[index+1] == pfmengine.OutputStyleDefaultSettings {
				return true
			}
			continue
		}
		if value, found := strings.CutPrefix(
			argument,
			"--settings=",
		); found &&
			value == pfmengine.OutputStyleDefaultSettings {
			return true
		}
	}
	return false
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
	prefs := machine.EffectiveClaude(primary)
	if prefs.SystemPrompt == "" || prefs.SystemPrompt == config.SystemPromptProduction {
		// Production expects no prompt material anywhere, so every seat would
		// classify as a violation. Saying "clean" here would be a coincidence
		// detector: it would print the same word whether the door worked or
		// not.
		fmt.Fprintf(
			stdout,
			"doctor: spawn-audit: policy=%s — no prompt material is expected, nothing to audit\n",
			promptPolicyName(prefs.SystemPrompt),
		)
		return 0
	}

	observations, unread, err := liveClaudeSpawns(ctx, resolved, machine)
	if err != nil {
		fmt.Fprintf(stdout, "doctor: spawn-audit: CHECK FAILED to run (%v) — live chats unaudited\n", err)
		return 1
	}

	stamp, stampSignal := spawnDoorStamp(resolved.Home)
	if len(observations) == 0 {
		fmt.Fprintf(
			stdout,
			"doctor: spawn-audit: policy=%s — no live Claude chats found\n",
			promptPolicyName(prefs.SystemPrompt),
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
	for _, observation := range observations {
		verdict, reason := classifySpawn(observation, stamp)
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
	fmt.Fprintf(
		stdout,
		"doctor: spawn-audit: policy=%s chats=%d injected=%d predates-layer=%d violations=%d (age signal: %s)\n",
		promptPolicyName(prefs.SystemPrompt),
		len(observations),
		counts[spawnInjected],
		counts[spawnPredatesLayer],
		counts[spawnViolation],
		stampSignal,
	)
	warnings := spawnAuditUnreadWarnings(stdout, unread)
	if counts[spawnViolation] != 0 {
		warnings++
	}
	return warnings
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

// spawnDoorStamp is the moment this host's CURRENT spawn door went live: the
// later of the staged professor prompt's mtime (the prompt layer) and the
// running pfm binary's mtime (the argv every door builds). One stamp cannot
// be the prompt alone: the --settings output-style flag shipped releases
// after the prompt, and an install that leaves the prompt's bytes unchanged
// never moves its mtime — so every chat launched by an older pfm read as born
// "after the layer" and indicted a door that was never broken. Only a seat
// born after BOTH can blame the door now installed; an older seat carries the
// argv of the pfm that launched it, and a reload is its fix. The signal names
// every input, so a reader knows what the age claim rests on.
func spawnDoorStamp(home string) (int64, string) {
	var stamp int64
	var sources, failures []string
	consider := func(label, path string, err error) {
		if err == nil {
			var info os.FileInfo
			if info, err = os.Stat(path); err == nil {
				stamp = max(stamp, info.ModTime().Unix())
				sources = append(sources, "mtime of "+path)
				return
			}
		}
		failures = append(failures, fmt.Sprintf("%s: %v", label, err))
	}
	consider("prompt layer", action.ProfessorPromptPath(home), nil)
	executable, err := spawnDoorExecutable()
	consider("pfm binary", executable, err)
	if stamp == 0 {
		return 0, fmt.Sprintf("unavailable (%s) — age never decided a verdict", strings.Join(failures, "; "))
	}
	signal := fmt.Sprintf(
		"%s, the later of %s",
		time.Unix(stamp, 0).Format(time.RFC3339),
		strings.Join(sources, " and "),
	)
	if len(failures) != 0 {
		signal += " (unreadable: " + strings.Join(failures, "; ") + ")"
	}
	return stamp, signal
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
) ([]spawnObservation, []string, error) {
	client := gather.CommandTmux{TmuxTmpDir: filepath.Dir(resolved.TmuxDir)}
	probe, err := gather.ProbeTmuxReadOnly(ctx, resolved.TmuxDir, client, time.Now())
	if err != nil {
		return nil, nil, fmt.Errorf("probe tmux sockets under %s: %w", resolved.TmuxDir, err)
	}
	unread := append([]string(nil), probe.ProbeWarnings...)
	proc := gather.NewProcFS(resolved.ProcRoot)
	binary := claudeBinaryName(machine)

	observations := make([]spawnObservation, 0, len(probe.Panes))
	for _, pane := range probe.Panes {
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
