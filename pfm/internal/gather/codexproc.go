package gather

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"hostops/pfm/internal/resolve"
)

// CodexThreadResolver names the conversation behind a live codex process that
// holds no rollout file descriptor. It receives the process's exported
// CODEX_THREAD_ID, its pane directory, its start time in epoch seconds, and
// the pane's own socket and id — the last two so an implementation can
// consult a fleet-recorded pane binding, which outranks both the exported
// variable and the birth-window guess (store.NewCodexThreadResolverRoots).
// It returns the thread id with the rollout path the Codex state store
// records for it. An empty id means the process could not be identified and
// must not become a live row.
type CodexThreadResolver func(
	exported string,
	cwd string,
	birth int64,
	socket string,
	paneID string,
) (id, rolloutPath string)

// DetectCodex maps live codex processes to panes using pid ancestry. It sees
// only sessions that hold a rollout file descriptor or state the thread they
// resumed in their own argv.
func DetectCodex(proc ProcFS, codexHome string, panes []Pane) ([]LiveCodex, error) {
	return DetectCodexThreads(proc, codexHome, panes, nil)
}

// DetectCodexThreads is DetectCodex plus state-store identity, so a Codex
// session that writes no rollout file — the normal shape of a paginated
// thread since Codex 0.146.1 — is still a live chat instead of a missing one.
func DetectCodexThreads(
	proc ProcFS,
	codexHome string,
	panes []Pane,
	identify CodexThreadResolver,
	binaries ...string,
) ([]LiveCodex, error) {
	return DetectCodexThreadsInRoots(proc, []string{codexHome}, panes, identify, binaries...)
}

// DetectCodexThreadsInRoots is DetectCodexThreads over every configured
// Codex home. A process belongs to the account whose sessions directory owns
// its rollout descriptor; rollout-less sessions use the roster-wide resolver.
func DetectCodexThreadsInRoots(
	proc ProcFS,
	codexHomes []string,
	panes []Pane,
	identify CodexThreadResolver,
	binaries ...string,
) ([]LiveCodex, error) {
	cmdlines, err := processCmdlines(proc)
	if err != nil {
		return nil, fmt.Errorf("list processes for Codex scan: %w", err)
	}
	return detectCodexThreadsInRootsFrom(cmdlines, proc, codexHomes, panes, identify, binaries...)
}

// detectCodexThreadsInRootsFrom is DetectCodexThreadsInRoots over an
// already-fetched pid->cmdline snapshot — see processCmdlines.
func detectCodexThreadsInRootsFrom(
	cmdlines map[int][]string,
	proc ProcFS,
	codexHomes []string,
	panes []Pane,
	identify CodexThreadResolver,
	binaries ...string,
) ([]LiveCodex, error) {
	pids := sortedPIDs(cmdlines)
	paneByPID := panesByPID(panes)

	live := make([]LiveCodex, 0)
	for _, pid := range pids {
		cmdline := cmdlines[pid]
		if !IsCodexCommand(cmdline, binaries...) {
			continue
		}
		pane, found := paneForProcess(proc, pid, paneByPID)
		if !found {
			continue
		}
		links, err := proc.FDLinks(pid)
		if err != nil {
			live = append(
				live,
				LiveCodex{
					PID:           pid,
					PanePID:       pane.PID,
					Socket:        pane.Socket,
					PaneID:        pane.PaneID,
					IdentityError: fmt.Sprintf("read Codex descriptors: %v", err),
				},
			)
			continue
		}
		rolloutPath, identityErr := heldCodexRoot(links, codexHomes)
		if errors.Is(identityErr, errHeldSubagents) && hasCodexAncestor(proc, pid, pane.PID, cmdlines, binaries) {
			continue
		}
		if identityErr != nil {
			live = append(
				live,
				LiveCodex{
					PID:           pid,
					PanePID:       pane.PID,
					Socket:        pane.Socket,
					PaneID:        pane.PaneID,
					IdentityError: identityErr.Error(),
				},
			)
			continue
		}
		// True only when the loop above actually found the rollout among
		// this process's own FDLinks — before argv/env/identify get a
		// chance to fill rolloutPath in from somewhere else. Only a
		// currently-held rollout may claim the process is doing this
		// conversation right now.
		rolloutHeld := rolloutPath != ""
		// A process-held rollout is what the live process is doing NOW. It
		// outranks launch argv and inherited environment because a compact or
		// reset continuation can rotate the thread without replacing the
		// process. The argv remains the deterministic identity for a RESUMED
		// session that holds no rollout file: its thread was created hours or
		// days before the process, so no birth match can reach it. Both outrank
		// exported CODEX_THREAD_ID because that variable is inherited.
		threadID := CodexRolloutID(rolloutPath)
		if threadID == "" {
			threadID = codexResumeArgv(cmdline)
		}
		if threadID == "" && identify != nil {
			threadID = codexThreadEnv(proc, pid)
		}
		if rolloutPath == "" && threadID == "" && identify == nil {
			continue
		}

		if rolloutPath == "" && identify != nil {
			id, path := identify(threadID, pane.CurrentPath, processBirth(proc, pid), pane.Socket, pane.PaneID)
			if id == "" {
				continue
			}
			threadID = id
			rolloutPath = path
		}
		live = append(live, LiveCodex{
			PID:         pid,
			PanePID:     pane.PID,
			Socket:      pane.Socket,
			PaneID:      pane.PaneID,
			RolloutPath: rolloutPath,
			ThreadID:    threadID,
			RolloutHeld: rolloutHeld,
		})
	}
	sort.Slice(live, func(left, right int) bool {
		if live[left].Socket != live[right].Socket {
			return live[left].Socket < live[right].Socket
		}
		return live[left].PID < live[right].PID
	})
	return live, nil
}

// CodexRolloutID returns the conversation id carried by a rollout path.
// Empty means the process holds no recognisable Codex rollout. Keeping this
// parser in gather makes the live-process observation the single source used
// by both composition and persistent pane rebinding.
func CodexRolloutID(path string) string {
	base := filepath.Base(path)
	if filepath.Ext(base) != ".jsonl" {
		return ""
	}
	stem := strings.TrimSuffix(base, ".jsonl")
	rest, found := strings.CutPrefix(stem, "rollout-")
	if !found {
		return ""
	}
	if len(rest) > 20 &&
		rest[4] == '-' &&
		rest[7] == '-' &&
		rest[10] == 'T' &&
		rest[13] == '-' &&
		rest[16] == '-' &&
		rest[19] == '-' {
		threadID, _, _ := strings.Cut(rest[20:], "_")
		return threadID
	}
	return rest
}

// CodexThreadID names the live conversation a detected Codex process owns.
// A current rollout always wins; ThreadID is the rollout-less resolver rung.
func CodexThreadID(process LiveCodex) string {
	if id := CodexRolloutID(process.RolloutPath); id != "" {
		return id
	}
	return process.ThreadID
}

// codexResumeArgv reads the conversation a Codex process was launched to
// resume: `codex … resume <uuid>` names it outright. Only a uuid counts —
// `codex resume --last` and `codex resume <name>` identify nothing here.
func codexResumeArgv(cmdline []string) string {
	for index := 0; index+1 < len(cmdline); index++ {
		if cmdline[index] != "resume" {
			continue
		}
		if candidate := cmdline[index+1]; isUUID(candidate) {
			return candidate
		}
	}
	return ""
}

// codexThreadEnv reads the identity a Codex session exports into the shells
// it spawns. It is absent from many sessions, which is why the caller still
// needs the directory and birth-time fallback.
func codexThreadEnv(proc ProcFS, pid int) string {
	environment, err := proc.Environ(pid)
	if err != nil {
		return ""
	}
	return environment[resolve.CodexThreadEnv]
}

func processBirth(proc ProcFS, pid int) int64 {
	birther, supported := proc.(ProcBirth)
	if !supported {
		return 0
	}
	birth, err := birther.Birth(pid)
	if err != nil {
		return 0
	}
	return birth
}

// RefreshCodexHeldRollouts checks only already-known PIDs, not the whole
// process tree. Idle pane reconciliation must never let an old FD snapshot
// overrule a newly cleared screen. Processes that exited contribute no claim.
func RefreshCodexHeldRollouts(proc ProcFS, previous []LiveCodex, roots []string) ([]LiveCodex, error) {
	live := make([]LiveCodex, 0, len(previous))
	var issues []error
	for _, process := range previous {
		links, err := proc.FDLinks(process.PID)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			issues = append(issues, fmt.Errorf("read Codex pid %d rollout descriptors: %w", process.PID, err))
			process.RolloutPath, process.ThreadID, process.RolloutHeld = "", "", false
			process.IdentityError = fmt.Sprintf("read Codex descriptors: %v", err)
			live = append(live, process)
			continue
		}
		path, identityErr := heldCodexRoot(links, roots)
		process.RolloutPath, process.ThreadID, process.RolloutHeld = path, CodexRolloutID(path), path != ""
		process.IdentityError = ""
		if identityErr != nil {
			process.IdentityError = identityErr.Error()
		}
		live = append(live, process)
	}
	return live, errors.Join(issues...)
}

func isRolloutUnder(root, target string) bool {
	if !strings.HasPrefix(filepath.Base(target), "rollout-") ||
		filepath.Ext(target) != ".jsonl" {
		return false
	}
	relative, err := filepath.Rel(root, filepath.Clean(target))
	return err == nil &&
		relative != "." &&
		relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func panesByPID(panes []Pane) map[int]Pane {
	paneByPID := make(map[int]Pane, len(panes))
	for index := range panes {
		pane := panes[index]
		paneByPID[pane.PID] = pane
	}
	return paneByPID
}

func paneForProcess(proc ProcFS, pid int, paneByPID map[int]Pane) (Pane, bool) {
	current := pid
	for depth := 0; depth <= 4; depth++ {
		if pane, found := paneByPID[current]; found {
			return pane, true
		}
		if depth == 4 {
			break
		}
		stat, err := proc.Stat(current)
		if err != nil || stat.ParentPID <= 1 || stat.ParentPID == current {
			break
		}
		current = stat.ParentPID
	}
	return Pane{}, false
}
