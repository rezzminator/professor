package reap

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"hostops/pfm/internal/fleetdb"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/paths"
)

const (
	// deadAfter is how long a socket file with no server behind it must have
	// sat there before it counts as a corpse rather than a server still
	// starting up. It mirrors the picker's own corpse sweep.
	deadAfter = time.Hour
	// vsctMaxIdle is how long a detached plain-terminal bunker may sit unused.
	vsctMaxIdle = 7 * 24 * time.Hour
	// defaultBusyRecent is how recently a transcript must have been written
	// for its chat to count as working. A running turn writes continuously.
	defaultBusyRecent = time.Minute
	// defaultHorizon is how long an ATTACHED chat may sit with no last
	// registered prompt or response before it is reapable. The order: "if a
	// chat is idle for 24h, it has to get killed ... make it 48h."
	defaultHorizon = 48 * time.Hour
	// defaultClientActive is exemption 1's window — a chat whose most
	// recently active attached client moved inside the last hour is never
	// reaped, whatever its transcript idle time.
	defaultClientActive = time.Hour
	// probeTimeout bounds every tmux call. A wedged server answers its socket
	// and then says nothing, and an unbounded probe waits on it forever — the
	// sweep hangs and the operator sees a tool that "does not run" rather than
	// one socket it could not read. On timeout the socket is reported unread,
	// which is a SKIP, never a kill.
	probeTimeout = 5 * time.Second
)

// KillServerFunc ends one chat's tmux server and every handle pointing at it.
// The reaper takes it as a dependency rather than spelling the sequence again:
// the picker's ⌃X and ⌃O already own that sequence, and two spellings of "end
// this chat" is how one of them starts leaving crumbs behind (K3).
type KillServerFunc func(ctx context.Context, socket string) error

// Dependencies are the reaper's collaborators. Every one of them has a real
// default; a test supplies its own.
type Dependencies struct {
	Paths          paths.Values
	Tmux           Tmux
	Proc           gather.ProcFS
	Busy           BusyProbe
	ClaudeBinary   string
	CodexBinary    string
	OpencodeBinary string
	// CodexRoots is every configured Codex account home. The idle-horizon
	// check resolves a Codex seat to its rollout by finding the live process
	// that still holds it open, the same in-process path DetectCodex already
	// used for the roster — never a shelled-out `pfm chat resolve`.
	CodexRoots []string
	KillServer KillServerFunc
	Now        func() time.Time
}

// Runner performs one sweep.
type Runner struct {
	paths          paths.Values
	tmux           Tmux
	proc           gather.ProcFS
	busy           BusyProbe
	claudeBinary   string
	codexBinary    string
	opencodeBinary string
	codexRoots     []string
	killServer     KillServerFunc
	now            func() time.Time
}

// Options are one sweep's knobs.
type Options struct {
	// Apply performs the reap. Without it nothing is killed, moved, or
	// removed — the sweep only classifies.
	Apply bool
	// BusyRecent is the transcript-write window that counts as working.
	BusyRecent time.Duration
	// Horizon is how long an attached chat may sit with no last registered
	// prompt or response before it is reapable. Zero uses defaultHorizon.
	Horizon time.Duration
	// ClientActive is exemption 1's window. Zero uses defaultClientActive.
	ClientActive time.Duration
	// Self is the caller's own socket, which is never a candidate.
	Self string
}

// Report is one sweep's result.
type Report struct {
	Decisions []Decision
	AgentsOK  bool
	BusyError string

	Keep      int
	Orphans   int
	DeadFiles int
	Killed    int
	// Unknown counts chats whose transcript could not be located, read, or
	// parsed — reported distinctly from Keep so a run that could not read
	// anything is never printed identically to a run that found nothing to
	// do.
	Unknown int
	// Failed counts decisions where apply actually attempted a kill or a
	// remove and the attempt itself errored. Any non-zero Failed means the
	// command's own exit code must be non-zero.
	Failed      int
	KeepKB      int64
	OrphanKB    int64
	FreedKB     int64
	AvailBefore int64
	AvailAfter  int64
	// Warnings are non-fatal apply-time failures that never affect the exit
	// code — today, only an orphaned role- crumb (internal/rearm's format)
	// that could not be removed. Reported so the failure is visible; never
	// swallowed, never made destructive.
	Warnings []string
}

// New fills the real implementations for anything the caller omitted.
func New(dependencies Dependencies) (*Runner, error) {
	resolved := dependencies.Paths
	if resolved.TmuxDir == "" {
		var err error
		resolved, err = paths.Resolve()
		if err != nil {
			return nil, fmt.Errorf("resolve reap paths: %w", err)
		}
	}
	now := dependencies.Now
	if now == nil {
		now = time.Now
	}
	tmux := dependencies.Tmux
	if tmux == nil {
		tmux = CommandTmux{TmuxDir: resolved.TmuxDir, Now: now}
	}
	proc := dependencies.Proc
	if proc == nil {
		proc = gather.NewProcFS(resolved.ProcRoot)
	}
	busy := dependencies.Busy
	if busy == nil {
		busy = NewClaudeAgents(resolved)
	}
	if dependencies.KillServer == nil {
		return nil, errors.New("reap needs a kill-server implementation")
	}
	return &Runner{
		paths:          resolved,
		tmux:           tmux,
		proc:           proc,
		busy:           busy,
		claudeBinary:   dependencies.ClaudeBinary,
		codexBinary:    dependencies.CodexBinary,
		opencodeBinary: dependencies.OpencodeBinary,
		codexRoots:     dependencies.CodexRoots,
		killServer:     dependencies.KillServer,
		now:            now,
	}, nil
}

// Run probes the fleet, classifies every socket, and — only with Apply —
// performs the actions the plan asked for.
func (runner *Runner) Run(
	ctx context.Context,
	options Options,
) (Report, error) {
	input := Input{
		Self:         options.Self,
		Apply:        options.Apply,
		DeadAfter:    deadAfter,
		VSCTMaxIdle:  vsctMaxIdle,
		BusyRecent:   options.BusyRecent,
		Horizon:      options.Horizon,
		ClientActive: options.ClientActive,
	}
	if input.BusyRecent <= 0 {
		input.BusyRecent = defaultBusyRecent
	}
	if input.Horizon <= 0 {
		input.Horizon = defaultHorizon
	}
	if input.ClientActive <= 0 {
		input.ClientActive = defaultClientActive
	}

	var report Report
	busyIDs, err := runner.busy.BusySessions(ctx)
	input.AgentsOK = err == nil
	input.BusyIDs = busyIDs
	if err != nil {
		report.BusyError = err.Error()
	}
	report.AgentsOK = input.AgentsOK

	tree, err := NewProcessTree(runner.proc, runner.claudeBinary, runner.codexBinary, runner.opencodeBinary)
	if err != nil {
		return Report{}, err
	}

	sockets, err := runner.probeSockets(ctx, tree)
	if err != nil {
		return Report{}, err
	}
	state := fleetdb.Open(ctx, runner.paths)
	branchSeats, branchErr := state.BranchSeats(ctx)
	closeErr := state.Close()
	if branchErr != nil || closeErr != nil {
		return Report{}, fmt.Errorf("read detached branch seats: %w", errors.Join(branchErr, closeErr))
	}
	for index := range sockets {
		_, sockets[index].DetachedFork = branchSeats[sockets[index].Name]
	}
	runner.probeIdleSignals(ctx, sockets)
	input.Sockets = sockets
	input.RecentIDs = runner.recentSessions(sockets, input.BusyRecent)

	sessionCtx, cancelSessions := context.WithTimeout(ctx, probeTimeout)
	sessions, err := runner.tmux.Sessions(sessionCtx, "vsct")
	cancelSessions()
	if err != nil {
		return Report{}, fmt.Errorf("list bunker sessions: %w", err)
	}
	input.VSCT = sessions

	decisions := Plan(input)
	if options.Apply {
		report.AvailBefore = runner.availableKB()
		var warnings []string
		decisions, warnings = runner.apply(ctx, decisions)
		report.Warnings = warnings
		report.AvailAfter = runner.availableKB()
	}
	report.Decisions = decisions
	for _, decision := range decisions {
		if decision.Failed {
			report.Failed++
		}
		switch decision.State {
		case StateOrphan, StateFork, StateIdle:
			report.Orphans++
			report.OrphanKB += decision.RSSKB
		case StateKilled:
			report.Killed++
			report.FreedKB += decision.RSSKB
		case StateDead:
			report.DeadFiles++
		case StateUnknown:
			report.Unknown++
		case StateSkip:
			report.Keep++
			report.KeepKB += decision.RSSKB
		default:
			report.Keep++
			report.KeepKB += decision.RSSKB
		}
	}
	return report, nil
}

// probeSockets reads every chat socket in the jail's socket directory.
func (runner *Runner) probeSockets(
	ctx context.Context,
	tree *ProcessTree,
) ([]Socket, error) {
	entries, err := os.ReadDir(runner.paths.TmuxDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read tmux socket directory: %w", err)
	}

	type socketFile struct {
		name    string
		modTime time.Time
	}
	files := make([]socketFile, 0, len(entries))
	for _, entry := range entries {
		if !isReapSocketName(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.Mode()&os.ModeSocket == 0 {
			continue
		}
		files = append(files, socketFile{name: entry.Name(), modTime: info.ModTime()})
	}
	sort.Slice(files, func(left, right int) bool {
		return files[left].name < files[right].name
	})

	allPanes := make([]gather.Pane, 0)
	sockets := make([]Socket, 0, len(files))
	for _, file := range files {
		socket := Socket{Name: file.name, Age: runner.now().Sub(file.modTime)}
		panes, err := runner.listPanes(ctx, file.name)
		switch {
		case err == nil:
			socket.HasServer = len(panes) > 0
		case errors.Is(err, gather.ErrServerGone):
			socket.HasServer = false
		default:
			socket.ProbeFailed = true
			socket.ProbeError = err.Error()
		}
		panePIDs := make([]int, 0, len(panes))
		for index := range panes {
			pane := &panes[index]
			allPanes = append(allPanes, *pane)
			panePIDs = append(panePIDs, pane.PID)
			if pane.Attached {
				socket.Attached = true
			}
			if socket.Label == "" && pane.WindowName != "" {
				socket.Label = pane.WindowName
			}
			if socket.CWD == "" {
				socket.CWD = pane.CurrentPath
			}
		}
		socket.RSSKB = tree.SubtreeRSSKB(panePIDs...)
		socket.Foreign = tree.ForeignProcesses(panePIDs)
		sockets = append(sockets, socket)
	}

	// Crumbs are read AFTER every pane is known, because a pane crumb is only
	// meaningful beside the pane list that says whether that pane still exists.
	crumbs, err := gather.ReadCrumbsReadOnly(runner.paths.SIDDir, allPanes)
	if err != nil {
		return nil, fmt.Errorf("read session crumbs: %w", err)
	}
	byName := make(map[string]int, len(sockets))
	for index := range sockets {
		byName[sockets[index].Name] = index
	}
	for _, crumb := range crumbs.Crumbs {
		index, found := byName[crumb.Socket]
		if !found {
			continue
		}
		sockets[index].HasCrumb = true
		if uuid := sessionIDFromPath(crumb.TranscriptPath); uuid != "" {
			sockets[index].CrumbUUIDs = append(sockets[index].CrumbUUIDs, uuid)
			sockets[index].transcripts = append(
				sockets[index].transcripts,
				crumb.TranscriptPath,
			)
			sockets[index].ActivityPaths = append(
				sockets[index].ActivityPaths,
				crumb.TranscriptPath,
			)
		}
	}

	// A Codex seat carries no SID crumb — the idle-horizon check resolves it
	// through the same in-process path the roster already uses: a live Codex
	// process still holds its own rollout file open, so DetectCodex finds it
	// by walking that process's file descriptors, never by shelling out to
	// `pfm chat resolve`. A resolution failure here is soft on purpose — it
	// leaves those sockets' ActivityPaths empty, which the idle check reads
	// as UNKNOWN (never reaped), not as "no Codex chats exist".
	if len(runner.codexRoots) > 0 {
		liveCodex, err := gather.DetectCodexThreadsInRoots(
			runner.proc, runner.codexRoots, allPanes, nil, runner.codexBinary,
		)
		if err == nil {
			for _, thread := range liveCodex {
				if thread.RolloutPath == "" {
					continue
				}
				index, found := byName[thread.Socket]
				if !found {
					continue
				}
				sockets[index].ActivityPaths = append(
					sockets[index].ActivityPaths,
					thread.RolloutPath,
				)
			}
		}
	}
	return sockets, nil
}

// isReapSocketName is the reaper's selection seam. Keeping the delegation
// here makes it testable without minting a live-looking socket and prevents
// reap from growing a second, drifting prefix classifier.
func isReapSocketName(name string) bool {
	return gather.IsChatSocketName(name)
}

// listPanes probes one socket under a deadline, so one unresponsive server
// cannot stall the whole sweep.
func (runner *Runner) listPanes(
	ctx context.Context,
	socket string,
) ([]gather.Pane, error) {
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	panes, err := runner.tmux.ListPanes(probeCtx, socket)
	if err != nil && probeCtx.Err() != nil && ctx.Err() == nil {
		return nil, fmt.Errorf(
			"socket %s did not answer within %s",
			socket,
			probeTimeout,
		)
	}
	return panes, err
}

// recentSessions is the second busy signal: a session that was idle when the
// snapshot was taken can start work while detached — a queued --then steer, a
// /loop turn — and a running turn writes its transcript the whole time.
func (runner *Runner) recentSessions(
	sockets []Socket,
	window time.Duration,
) map[string]struct{} {
	recent := make(map[string]struct{})
	cutoff := runner.now().Add(-window)
	for socketIndex := range sockets {
		socket := &sockets[socketIndex]
		for index, path := range socket.transcripts {
			info, err := os.Stat(path)
			if err != nil || !info.ModTime().After(cutoff) {
				continue
			}
			if index < len(socket.CrumbUUIDs) {
				recent[socket.CrumbUUIDs[index]] = struct{}{}
			}
		}
	}
	return recent
}

// probeIdleSignals fills the two signals planAttached needs and neither
// tmux's own window_activity nor a transcript's raw mtime can answer
// honestly at this timescale: how long since the chat's own last parseable
// record, and how long since the socket's most recently active client
// moved. It only asks tmux for client activity on ATTACHED sockets — the
// only ones planAttached ever reads it for — so a fleet of mostly-detached
// sockets costs no extra probe calls.
func (runner *Runner) probeIdleSignals(ctx context.Context, sockets []Socket) {
	now := runner.now()
	for index := range sockets {
		if stamp, ok := socketActivity(sockets[index].ActivityPaths); ok {
			sockets[index].ActivityOK = true
			idle := now.Sub(stamp)
			if idle < 0 {
				idle = 0
			}
			sockets[index].IdleFor = idle
		}
		if !sockets[index].Attached {
			continue
		}
		clientCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		idle, found, err := runner.tmux.ClientIdle(clientCtx, sockets[index].Name)
		cancel()
		// Carry the probe's own success — never infer it from a duration.
		// A zero-value ClientIdle from either failure below must not read as
		// "just active": planAttached gates on ClientIdleOK BEFORE it ever
		// reads ClientIdle, so both cases land on StateUnknown, visible to
		// the operator, exactly like an unreadable transcript already does.
		switch {
		case err != nil:
			sockets[index].ClientProbeError = err.Error()
		case !found:
			// Attached is set but list-clients found no client — the 17-day
			// zombie-client shape this whole wave was commissioned for.
		default:
			sockets[index].ClientIdleOK = true
			sockets[index].ClientIdle = idle
		}
	}
}

// apply performs the plan. Every kill re-verifies the socket at kill time:
// the fleet spawns and attaches chats while a sweep is running, and the
// verdict was formed seconds ago.
func (runner *Runner) apply(
	ctx context.Context,
	decisions []Decision,
) ([]Decision, []string) {
	applied := make([]Decision, 0, len(decisions))
	warnings := make([]string, 0)
	for _, decision := range decisions {
		switch decision.Action {
		case ActionRemoveSocketFile:
			// A corpse can become a live server after planning but before
			// apply. Removing the path in that race detaches a working chat
			// from every client and lookup that reaches it by socket name.
			// Re-probe and remove only when tmux confirms the server is gone.
			if _, err := runner.listPanes(ctx, decision.Socket); err == nil {
				decision.State = StateSkip
				decision.Reason = "socket answered during apply"
				break
			} else if !errors.Is(err, gather.ErrServerGone) {
				decision.State = StateSkip
				decision.Reason = fmt.Sprintf("re-probe before remove: %v", err)
				decision.Failed = true
				break
			}
			path := filepath.Join(runner.paths.TmuxDir, decision.Socket)
			if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				decision.State = StateSkip
				decision.Reason = fmt.Sprintf("remove socket file: %v", err)
				decision.Failed = true
				break
			}
			if _, err := removeRoleCrumb(runner.paths.SIDDir, decision.Socket); err != nil {
				warnings = append(warnings, fmt.Sprintf(
					"%s: remove role crumb: %v", decision.Socket, err,
				))
			}
		case ActionKillServer:
			panes, err := runner.listPanes(ctx, decision.Socket)
			if err != nil && !errors.Is(err, gather.ErrServerGone) {
				decision.State = StateSkip
				decision.Reason = fmt.Sprintf("re-probe before kill: %v", err)
				decision.Failed = true
				break
			}
			attached := false
			for index := range panes {
				if panes[index].Attached {
					attached = true
				}
			}
			// An idle-horizon kill is expected to still be attached — that IS
			// the premise (exemption 1 already spared it if the operator had
			// been recently active). Only the orphan path treats a fresh
			// attach as the sweep's own verdict going stale mid-run.
			if attached && decision.State != StateIdle {
				decision.State = StateSkip
				decision.Reason = "attached since the sweep started"
				break
			}
			if err := runner.killServer(ctx, decision.Socket); err != nil {
				decision.State = StateSkip
				decision.Reason = fmt.Sprintf("kill-server: %v", err)
				decision.Failed = true
				break
			}
			decision.State = StateKilled
			if _, err := removeRoleCrumb(runner.paths.SIDDir, decision.Socket); err != nil {
				warnings = append(warnings, fmt.Sprintf(
					"%s: remove role crumb: %v", decision.Socket, err,
				))
			}
		case ActionKillSession:
			name := strings.TrimPrefix(decision.Socket, "vsct:")
			if err := runner.tmux.KillSession(ctx, "vsct", name); err != nil {
				decision.State = StateSkip
				decision.Reason = fmt.Sprintf("kill-session: %v", err)
				decision.Failed = true
				break
			}
			decision.State = StateKilled
		}
		decision.Action = ActionNone
		applied = append(applied, decision)
	}
	return applied, warnings
}

// roleCrumbPath is the socket-scoped role crumb the T1 seat re-arm wave
// writes into SIDDir as "role-<socket>" for a `--role` seat — a sibling
// worktree, internal/rearm there, owns that format and this branch does not
// import it (it does not exist here and this branch must build alone). T1
// removes its own crumb on the canonical kill path; every OTHER path that
// ends a chat's server, including every one below, orphans it, so clearing
// it here is this sweep's job. Match by filename only; reconcile the two at
// merge if the shape moves.
func roleCrumbPath(sidDir, socket string) string {
	return filepath.Join(sidDir, "role-"+socket)
}

// removeRoleCrumb is best-effort: a missing crumb is the ordinary case (most
// seats carry no --role), never a warning. A removal that fails for any
// other reason IS a warning — never swallowed, never turned into a non-zero
// exit, since it holds up nothing this sweep's own exit code answers for.
func removeRoleCrumb(sidDir, socket string) (removed bool, err error) {
	if sidDir == "" {
		return false, nil
	}
	if err := os.Remove(roleCrumbPath(sidDir, socket)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// availableKB reads the machine's own available memory, which is the only
// honest measure of what a reap reclaimed — summed RSS double-counts the
// runtime pages several node processes share.
func (runner *Runner) availableKB() int64 {
	content, err := os.ReadFile(filepath.Join(runner.paths.ProcRoot, "meminfo"))
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.HasPrefix(line, "MemAvailable:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		value, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return value
	}
	return 0
}

// sessionIDFromPath reads the session id out of a transcript pathname.
func sessionIDFromPath(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}
