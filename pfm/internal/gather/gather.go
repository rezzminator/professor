// Package gather probes live tmux and process state without indexing files.
package gather

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"golang.org/x/sync/errgroup"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
)

// CodexNameResolver resolves the indexed display name for a rollout path.
type CodexNameResolver func(rolloutPath string) string

// CodexIDNameResolver resolves the indexed display name for a Codex thread id.
// It is the only naming rung a session that writes no rollout file has, and
// the index behind it owns the precedence: the thread's own name, then the
// session_index name, then its first prompt.
type CodexIDNameResolver func(threadID string) string

// Dependencies supplies probe interfaces and process-local policy.
type Dependencies struct {
	ProcFS      ProcFS
	Tmux        TmuxClient
	TmuxBinary  string
	TmuxTmpDir  string
	Now         func() time.Time
	CodexName   CodexNameResolver
	CodexIDName CodexIDNameResolver
	CodexThread CodexThreadResolver
	// CodexHomes is the config-owned roster. Nil preserves the historical
	// paths.CodexHome singleton for direct callers; an empty non-nil slice
	// means no configured Codex engine.
	CodexHomes   []string
	ClaudeBinary string
	CodexBinary  string
	LabelEmojis  []string
	ReadOnly     bool
}

// Gatherer creates immutable live-state snapshots.
type Gatherer struct {
	paths        paths.Values
	proc         ProcFS
	tmux         TmuxClient
	now          func() time.Time
	codexName    CodexNameResolver
	codexIDName  CodexIDNameResolver
	codexThread  CodexThreadResolver
	codexHomes   []string
	claudeBinary string
	codexBinary  string
	labelEmojis  []string
	readOnly     bool
}

// New resolves paths and fills real implementations for omitted interfaces.
func New(dependencies Dependencies) (*Gatherer, error) {
	resolved, err := paths.Resolve()
	if err != nil {
		return nil, fmt.Errorf("resolve gather paths: %w", err)
	}
	proc := dependencies.ProcFS
	if proc == nil {
		proc = NewProcFS(resolved.ProcRoot)
	}
	now := dependencies.Now
	if now == nil {
		now = time.Now
	}
	tmux := dependencies.Tmux
	if tmux == nil {
		tmuxTmpDir := dependencies.TmuxTmpDir
		if tmuxTmpDir == "" {
			tmuxTmpDir = os.Getenv("TMUX_TMPDIR")
			if tmuxTmpDir == "" {
				tmuxTmpDir = "/tmp"
			}
		}
		tmux = CommandTmux{
			Binary:     dependencies.TmuxBinary,
			TmuxTmpDir: tmuxTmpDir,
		}
	}
	codexHomes := dependencies.CodexHomes
	if codexHomes == nil {
		codexHomes = append([]string(nil), resolved.Roots[pfmengine.Codex]...)
	} else {
		codexHomes = append([]string{}, codexHomes...)
	}
	return &Gatherer{
		paths:        resolved,
		proc:         proc,
		tmux:         tmux,
		now:          now,
		codexName:    dependencies.CodexName,
		codexIDName:  dependencies.CodexIDName,
		codexThread:  dependencies.CodexThread,
		codexHomes:   codexHomes,
		claudeBinary: dependencies.ClaudeBinary,
		codexBinary:  dependencies.CodexBinary,
		labelEmojis:  append([]string(nil), dependencies.LabelEmojis...),
		readOnly:     dependencies.ReadOnly,
	}, nil
}

// Gather probes live state. Pane-dependent filesystem and ProcFS probes run
// concurrently after the one-command-per-socket tmux snapshot is available.
func (gatherer *Gatherer) Gather(ctx context.Context) (Snapshot, error) {
	var tmuxProbe TmuxProbe
	var err error
	if gatherer.readOnly {
		tmuxProbe, err = ProbeTmuxReadOnly(
			ctx,
			gatherer.paths.TmuxDir,
			gatherer.tmux,
			gatherer.now(),
		)
	} else {
		tmuxProbe, err = ProbeTmux(
			ctx,
			gatherer.paths.TmuxDir,
			gatherer.tmux,
			gatherer.now(),
		)
	}
	if err != nil {
		return Snapshot{}, err
	}

	// Fetched once and shared: DetectCodexThreadsInRoots, DetectAgents,
	// DetectClaudeProcesses and DetectCache1H each used to enumerate every
	// /proc pid and read its cmdline independently — four full walks of a
	// ~1950-process box on every gather pass, live fleet or not. One walk,
	// four detectors reading from it, is the whole fix (see processCmdlines).
	cmdlines, err := processCmdlines(gatherer.proc)
	if err != nil {
		return Snapshot{}, err
	}

	var crumbs CrumbProbe
	var codex []LiveCodex
	var claudeProcesses []ClaudeProcess
	var agents []Agent
	var cacheSockets []string
	var paneLabels []PaneLabel
	group, _ := errgroup.WithContext(ctx)
	group.Go(func() error {
		capturer, ok := gatherer.tmux.(PaneCapturer)
		if !ok {
			return nil
		}
		paneLabels = CaptureClaudeLabels(ctx, capturer, tmuxProbe.Panes, gatherer.labelEmojis)
		return nil
	})
	group.Go(func() error {
		var err error
		if gatherer.readOnly {
			crumbs, err = ReadCrumbsReadOnly(gatherer.paths.SIDDir, tmuxProbe.Panes)
		} else {
			crumbs, err = ReadCrumbs(gatherer.paths.SIDDir, tmuxProbe.Panes)
		}
		return err
	})
	group.Go(func() error {
		var err error
		codex, err = detectCodexThreadsInRootsFrom(
			cmdlines,
			gatherer.proc,
			gatherer.codexHomes,
			tmuxProbe.Panes,
			gatherer.codexThread,
			gatherer.codexBinary,
		)
		return err
	})
	group.Go(func() error {
		var err error
		agents, err = detectAgentsFrom(
			cmdlines,
			gatherer.proc,
			gatherer.paths.Home,
			tmuxProbe.Panes,
			gatherer.claudeBinary,
		)
		return err
	})
	group.Go(func() error {
		var err error
		claudeProcesses, err = detectClaudeProcessesFrom(
			cmdlines,
			gatherer.proc,
			tmuxProbe.Panes,
			gatherer.claudeBinary,
		)
		return err
	})
	group.Go(func() error {
		var err error
		cacheSockets, err = detectCache1HFrom(cmdlines, gatherer.proc, tmuxProbe.Panes, gatherer.claudeBinary)
		return err
	})
	if err := group.Wait(); err != nil {
		return Snapshot{}, err
	}

	crumblessLive := DetectCrumblessLive(
		gatherer.proc,
		claudeProcesses,
		crumbs.Crumbs,
		tmuxProbe.Panes,
	)

	return Snapshot{
		Panes:           append([]Pane(nil), tmuxProbe.Panes...),
		Crumbs:          append([]Crumb(nil), crumbs.Crumbs...),
		Codex:           append([]LiveCodex(nil), codex...),
		ClaudeProcesses: append([]ClaudeProcess(nil), claudeProcesses...),
		Agents:          append([]Agent(nil), agents...),
		Cache1HSockets:  append([]string(nil), cacheSockets...),
		Renames: computeWindowRenames(
			tmuxProbe.Panes,
			codex,
			paneLabels,
			gatherer.codexName,
			gatherer.codexIDName,
		),
		CrumblessLive: append([]CrumblessLive(nil), crumblessLive...),
		CorpseSwept:   append([]string(nil), tmuxProbe.CorpseSwept...),
		StaleSwept:    append([]string(nil), crumbs.StaleSwept...),
		Warnings:      append([]string(nil), tmuxProbe.ProbeWarnings...),
	}, nil
}

// computeWindowRenames converges BOTH engines' window names — the fleet's DNS
// records — in one pass. A codex window follows the thread's indexed name; a
// claude window follows the 🔖 label its own statusline renders, which is what
// /rename set and what chat.sh resolves a chat by. Two writers of one name is
// how a chat ends up answering to something nobody typed, so this is the only
// one.
func computeWindowRenames(
	panes []Pane,
	codex []LiveCodex,
	labels []PaneLabel,
	resolveRollout CodexNameResolver,
	resolveID CodexIDNameResolver,
) []WindowRename {
	if resolveRollout == nil && resolveID == nil && len(labels) == 0 {
		return nil
	}
	paneByTarget := make(map[string]Pane, len(panes))
	for index := range panes {
		pane := panes[index]
		paneByTarget[pane.Socket+"\x00"+pane.PaneID] = pane
	}
	seenWindows := make(map[string]struct{})
	planned := make([]WindowRename, 0)
	for _, live := range codex {
		pane, found := paneByTarget[live.Socket+"\x00"+live.PaneID]
		if !found {
			continue
		}
		target := WindowNameFor(codexWindowName(live, resolveRollout, resolveID))
		if target == "" || pane.WindowName == target || pane.WindowID == "" {
			continue
		}
		key := pane.Socket + "\x00" + pane.WindowID
		if _, duplicate := seenWindows[key]; duplicate {
			continue
		}
		seenWindows[key] = struct{}{}
		planned = append(planned, WindowRename{
			Socket:      pane.Socket,
			SessionName: pane.SessionName,
			WindowID:    pane.WindowID,
			CurrentName: pane.WindowName,
			TargetName:  target,
		})
	}
	planned = append(
		planned,
		claudeWindowRenames(paneByTarget, labels, seenWindows)...,
	)

	// Two windows converging on ONE name is an ambiguity, not a rename: the
	// window name is how the fleet addresses a chat, so two chats answering to
	// it is worse than either keeping the name it has. Skip both windows.
	windowsByTarget := make(map[string]int, len(planned))
	for _, rename := range planned {
		windowsByTarget[rename.TargetName]++
	}
	renames := make([]WindowRename, 0, len(planned))
	for _, rename := range planned {
		if windowsByTarget[rename.TargetName] > 1 {
			continue
		}
		renames = append(renames, rename)
	}
	sort.Slice(renames, func(left, right int) bool {
		if renames[left].Socket != renames[right].Socket {
			return renames[left].Socket < renames[right].Socket
		}
		return renames[left].WindowID < renames[right].WindowID
	})
	return renames
}

// claudeWindowRenames plans the claude half: one window takes the 🔖 label its
// own panes render.
//
// A window is left ALONE — never renamed — when its panes disagree about the
// label (two /chat:branch siblings share a window and a window name cannot
// carry both) or when any of its panes could not be captured at all. A failed
// capture is not an absent label: renaming from the sibling that DID answer
// would stamp one chat's name on a window hosting two.
func claudeWindowRenames(
	paneByTarget map[string]Pane,
	labels []PaneLabel,
	seenWindows map[string]struct{},
) []WindowRename {
	type windowPlan struct {
		pane  Pane
		label string
		skip  bool
	}
	order := make([]string, 0, len(labels))
	plans := make(map[string]*windowPlan, len(labels))
	for _, label := range labels {
		key := label.Socket + "\x00" + label.WindowID
		plan, found := plans[key]
		if !found {
			plan = &windowPlan{}
			plans[key] = plan
			order = append(order, key)
		}
		if label.Failed {
			plan.skip = true
			continue
		}
		if label.Label == "" {
			continue
		}
		clipped := WindowNameFor(label.Label)
		if plan.label != "" && plan.label != clipped {
			plan.skip = true
			continue
		}
		pane, found := paneByTarget[label.Socket+"\x00"+label.PaneID]
		if !found {
			continue
		}
		plan.label = clipped
		plan.pane = pane
	}

	renames := make([]WindowRename, 0, len(order))
	for _, key := range order {
		plan := plans[key]
		if plan.skip || plan.label == "" || plan.pane.WindowID == "" {
			continue
		}
		if plan.pane.WindowName == plan.label {
			continue
		}
		if _, duplicate := seenWindows[key]; duplicate {
			continue
		}
		seenWindows[key] = struct{}{}
		renames = append(renames, WindowRename{
			Socket:      plan.pane.Socket,
			SessionName: plan.pane.SessionName,
			WindowID:    plan.pane.WindowID,
			CurrentName: plan.pane.WindowName,
			TargetName:  plan.label,
		})
	}
	return renames
}

// codexWindowName walks the naming rungs for one live Codex pane: the rollout
// file it holds, then the thread id it was identified by. A resumed or
// paginated session has only the second — keying the rename on the rollout
// path alone left every such window carrying the name it was born with, or,
// worse, a sibling's.
func codexWindowName(
	live LiveCodex,
	resolveRollout CodexNameResolver,
	resolveID CodexIDNameResolver,
) string {
	if resolveRollout != nil && live.RolloutPath != "" {
		if name := resolveRollout(live.RolloutPath); name != "" {
			return name
		}
	}
	if resolveID != nil && live.ThreadID != "" {
		return resolveID(live.ThreadID)
	}
	return ""
}

// WindowNameRunes bounds a tmux window name. A tab is a handful of columns
// wide, and a name that overflows it is a name nobody can read anyway.
const WindowNameRunes = 24

// WindowNameFor is the ONE mapping from a chat's name — a codex thread name or
// a claude 🔖 label — to the tmux window name it converges on.
//
// Every writer of a window name calls it: both halves of computeWindowRenames
// here, and the claude statusline's own convergence (a /rename must not wait
// for the name-sync timer). Two writers that clipped differently would each
// see the other's name as drift and rename the window back and forth forever.
func WindowNameFor(name string) string {
	return clipRunes(name, WindowNameRunes)
}

func clipRunes(value string, limit int) string {
	count := 0
	for index := range value {
		if count == limit {
			return value[:index]
		}
		count++
	}
	return value
}
