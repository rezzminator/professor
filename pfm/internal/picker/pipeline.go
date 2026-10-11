package picker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	fleetindex "github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/rowfacts"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/ui"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

const (
	fleetRefreshGrowth = 13
)

var (
	// fleetRefreshInterval is the cadence while somebody is driving the picker.
	// One pass is expensive on a real fleet — a tmux fork+exec PER LIVE
	// SOCKET (measured ~50 on this box) plus a whole store read and a
	// per-process scan for every engine detector — so paying it on a fixed
	// clock forever is what let an abandoned picker hold over half a core
	// (2026-09-03: 1741 ticks/30s, ~58%, on a real-fleet real-box measurement
	// with the sky tick already fixed — the scan itself was the rest).
	fleetRefreshInterval = 5 * time.Second
	// fleetRefreshParkThreshold is the point past which the loop stops
	// scheduling unconditional full-fleet passes. Known Codex panes retain
	// lightweight identity checks so /clear in another pane stays observable.
	fleetRefreshParkThreshold = 60 * time.Second
	// fleetRefreshGrowth stretches the interval after every pass nobody
	// interrupted. It is deliberately steep, not the gentle curve a cheaper
	// operation could afford: at ~5+ CPU-seconds a pass, even a handful of
	// passes landing inside a 30s measurement window blows the ≤2%-of-a-core
	// idle budget outright, so the climb is sized to cross
	// fleetRefreshParkThreshold within a SINGLE untouched interval (5s × 13 =
	// 65s ≥ 60s) rather than many gentle ones.
	// Presence polling stays responsive while expensive idle identity probes
	// use their own slower cadence.
	fleetRefreshParkPollInterval  = 2 * time.Second
	fleetRefreshCodexPollInterval = 10 * time.Second
	// fleetRefreshStaleAfter is how old the last attempt must be before
	// a keystroke is allowed to start the next one on the spot. A driven picker
	// refreshes every fleetRefreshInterval, so this only ever fires for a
	// picker that backed off or parked and is being used again.
	fleetRefreshStaleAfter = ui.RefreshStaleAfter
)

// bufferedWarnings collects gather warnings raised from the background
// refresh goroutine while an interactive picker owns the terminal (runLS),
// releasing them to stderr only once flush is called after Pick returns.
type bufferedWarnings struct {
	mu         sync.Mutex
	warnings   []string
	rawEntries []string
}

func (buffer *bufferedWarnings) add(warning string) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	for _, existing := range buffer.warnings {
		if existing == warning {
			return
		}
	}
	buffer.warnings = append(buffer.warnings, warning)
}

func (buffer *bufferedWarnings) Write(p []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	raw := string(p)
	for _, existing := range buffer.rawEntries {
		if existing == raw {
			return len(p), nil
		}
	}
	buffer.rawEntries = append(buffer.rawEntries, raw)
	return len(p), nil
}

// flush prints every warning collected so far and clears the buffer, so a
// caller that flushes between picker frames never
// prints the same warning twice.
func (buffer *bufferedWarnings) flush(stderr io.Writer) {
	buffer.mu.Lock()
	pending := buffer.warnings
	rawEntries := buffer.rawEntries
	buffer.warnings = nil
	buffer.rawEntries = nil
	buffer.mu.Unlock()
	for _, warning := range pending {
		fmt.Fprintf(stderr, "pfm: tmux probe warning: %s\n", warning)
	}
	for _, raw := range rawEntries {
		fmt.Fprint(stderr, raw)
	}
}

type scanRequest struct {
	RepoRoots []string
	View      compose.View
	Query     string
	ReadOnly  bool
	NoSky     bool
	// Safe is the --safe flag verbatim (auto|on|off); resolveCosmosSafe
	// turns it into the snapshot's CosmosSafe bool at build time.
	Safe    string
	Runtime *pfmconfig.Runtime
	Comms   commsReader
	// Schedules reads which sessions have an unseen schedule; nil leaves every
	// row unmarked.
	Schedules unseenScheduleReader
	// Facts reads each chat's model, effort and working state from its
	// transcript tail; nil — every non-interactive caller, so --plain and --tsv
	// stay byte-identical — leaves the rows as composed.
	Facts *rowfacts.Reader
}

// resolveCosmosSafe decides whether the cosmos tab renders in vscode-safe
// mode: a slower clock and coarser colour quantisation that sidestep the
// VS Code WebGL glyph-atlas corruption heavy braille truecolor churn
// triggers across SIBLING terminals (microsoft/vscode#332859). "auto" arms
// it exactly when VS Code's terminal declares itself via TERM_PROGRAM.
func resolveCosmosSafe(flagValue, termProgram string) bool {
	switch flagValue {
	case "on":
		return true
	case "off":
		return false
	default:
		return termProgram == "vscode"
	}
}

type commsReader interface {
	CommsSince(context.Context, int64, int) ([]fleetdb.CommsEvent, error)
}

type unseenScheduleReader interface {
	UnseenScheduleSessionIDs(context.Context) (map[string]bool, error)
}

type cosmosSampler struct{ reader commsReader }

func (sampler cosmosSampler) Sample(ctx context.Context, sinceNS int64) ([]fleetdb.CommsEvent, error) {
	return sampler.reader.CommsSince(ctx, sinceNS, compose.CosmosEventCap)
}

type scanResult struct {
	Output   compose.Output
	Snapshot ui.Snapshot
	Live     gather.Snapshot
	Counters fleetindex.Counters
	Paths    paths.Values
}

// fleetRequest is the scan scope inside a picker request; the rest of
// scanRequest shapes the snapshot the picker renders.
func (request scanRequest) fleetRequest() fleet.Request {
	return fleet.Request{View: request.View, ReadOnly: request.ReadOnly, Runtime: request.Runtime, Workbenches: true}
}

// scanFleet is fleet.Scan plus the picker snapshot over its rows.
func scanFleet(
	ctx context.Context,
	database *store.Store,
	request scanRequest,
	stderr io.Writer,
) (scanResult, error) {
	result, err := fleet.Scan(ctx, database, request.fleetRequest(), stderr)
	if err != nil {
		return scanResult{}, err
	}
	return scanResult{
		Output:   result.Output,
		Snapshot: buildSnapshot(ctx, result.Env, request, result.Output),
		Live:     result.Live,
		Counters: result.Counters,
		Paths:    result.Env.Paths,
	}, nil
}

// scanFleetCached is the picker's first frame: the index as it stands,
// marked refreshing while the real scan runs.
func scanFleetCached(
	ctx context.Context,
	database *store.Store,
	request scanRequest,
) (scanResult, error) {
	result, err := fleet.ScanCached(ctx, database, request.fleetRequest())
	if err != nil {
		return scanResult{}, err
	}
	// The first frame is the index as it stands: reading every transcript tail
	// is the refresh's job, so the picker opens before it is done.
	request.Facts = nil
	snapshot := buildSnapshot(ctx, result.Env, request, result.Output)
	snapshot.Refreshing = true
	return scanResult{Output: result.Output, Snapshot: snapshot, Paths: result.Env.Paths}, nil
}

type indexRunner interface {
	Run(context.Context, fleetindex.Options) (fleetindex.Counters, error)
}

type refreshDependencies struct {
	discover   func([]string) ([]workbench.Bench, []workbench.WalkError)
	newIndexer func(*store.Store) (indexRunner, error)
	// activity is the picker's presence clock. Nil — every non-interactive
	// caller and every existing stream test — reads as permanently active and
	// holds the loop at fleetRefreshInterval, exactly as before the backoff.
	activity *ui.ActivityClock
	clock    clock.Clock
}

func streamFleetRefreshes(
	ctx context.Context,
	database *store.Store,
	request scanRequest,
	warn fleet.Warn,
	stderr io.Writer,
	updates chan<- ui.Snapshot,
	activity *ui.ActivityClock,
) {
	streamFleetRefreshesWith(
		ctx,
		database,
		request,
		warn,
		stderr,
		updates,
		refreshDependencies{activity: activity},
	)
}

// writeRefreshError keeps an intentional picker shutdown from rendering as a
// failed refresh. Errors unrelated to the owning context still surface even
// if cancellation happened concurrently.
func writeRefreshError(ctx context.Context, stderr io.Writer, stage string, err error) bool {
	if contextErr := ctx.Err(); contextErr != nil && errors.Is(err, contextErr) {
		return false
	}
	fmt.Fprintf(stderr, "pfm refresh%s: %v\n", stage, err)
	return true
}

func streamFleetRefreshesWith(
	ctx context.Context,
	database *store.Store,
	request scanRequest,
	warn fleet.Warn,
	stderr io.Writer,
	updates chan<- ui.Snapshot,
	dependencies refreshDependencies,
) {
	defer close(updates)
	if ctx.Err() != nil {
		return
	}
	if err := refreshWorkbenches(request, dependencies.discover); err != nil {
		writeRefreshError(ctx, stderr, " workbenches", err)
	}
	refreshClock := dependencies.clock
	if refreshClock == nil {
		refreshClock = clock.Real
	}
	// lastAttempt records each pass's start, including a failed pass. Even
	// when a stale fleet's passes fail, keys start at most one per bound.
	// Parked Codex probes are not full passes and leave the stamp alone.
	lastAttempt := refreshClock.Now()
	var (
		// pendingRefresh: a failed publication must be retried even if
		// reconciliation already committed the binding and therefore reports no
		// further identity change. It also holds a stream whose first pass failed
		// out of the park.
		pendingRefresh bool
		// failStreak counts consecutive failed passes and retryAt is when the
		// next retry is due (failureBackoff). A pass that lands resets both.
		failStreak int
		retryAt    time.Time
	)
	// fail is every pass error's one door: stderr for the log, and a failure
	// snapshot for the picker, whose alt-screen hides stderr. A stream that
	// only wrote stderr left the TUI showing the last good rows as current. It
	// reports false when the picker is going away and the stream should stop.
	fail := func(stage string, err error, at time.Time) bool {
		if pendingRefresh {
			// A failed pass (not a published pass the presence probe then could
			// not watch) schedules its retry: the loop below holds every timer
			// fire until retryAt, whatever the park poll says.
			failStreak++
			retryAt = at.Add(failureBackoff(failStreak))
		}
		if !writeRefreshError(ctx, stderr, stage, err) {
			return false
		}
		return sendRefreshFailure(ctx, updates, stage, err, at)
	}
	var (
		// lastSent is the last completed pass; presence its fork-free
		// fingerprint, what a parked poll compares against.
		lastSent    sentRefresh
		presence    presenceBaseline
		environment fleet.Env
		data        fleet.Data
		live        gather.Snapshot
		indexer     indexRunner
		err         error
	)
	newIndexer := dependencies.newIndexer
	if newIndexer == nil {
		newIndexer = func(database *store.Store) (indexRunner, error) {
			return fleetindex.NewWithRoots(database, environment.Paths, environment.Paths.Roots)
		}
	}
	// firstPass is the picker's opening look. A failure no longer ends the
	// stream — it is published as a stale marker and the loop below retries on
	// its cadence, so a transient first-pass error never leaves a picker that
	// shows its opening rows forever. It reports false only when the stream
	// must stop (the picker is going away).
	firstPass := func() (proceed bool) {
		stop := func(stage string, err error) bool {
			pendingRefresh = true
			return fail(stage, err, lastAttempt)
		}
		environment, err = fleet.ResolveEnv(request.fleetRequest())
		if err != nil {
			return stop("", err)
		}
		presence.begin(environment, lastAttempt)
		if request.View == compose.DefaultView {
			data, err = fleet.LoadDefaultData(ctx, database)
		} else {
			data, err = fleet.LoadData(ctx, database)
		}
		if err != nil {
			return stop("", err)
		}
		live, err = fleet.Gather(ctx, database, environment, data,
			request.ReadOnly,
			warn,
			stderr,
		)
		if err != nil {
			return stop(" gather", err)
		}
		data, err = fleet.EnrichLive(ctx, database, data, live)
		if err != nil {
			return stop(" live cache", err)
		}
		if _, ok := sendRefresh(ctx, environment, request, data, live, true, updates); !ok {
			return false
		}
		movedBinding := false
		if !request.ReadOnly {
			movedBinding = fleet.ReconcileCodexPanes(ctx, database, live, environment.Runtime(), warn)
		}
		if indexer, err = newIndexer(database); err != nil {
			return stop(" index", err)
		}
		if _, err = indexer.Run(ctx, fleetindex.Options{
			PriorityCWD:  environment.CurrentDir,
			PriorityOnly: true,
		}); err != nil {
			return stop(" project index", err)
		}
		data, err = fleet.LoadData(ctx, database)
		if err != nil {
			return stop("", err)
		}
		if movedBinding {
			// The `live` this pass has been carrying since the gather above was
			// taken BEFORE fleet.ReconcileCodexPanes moved a binding — its
			// LiveCodex.RolloutPath still names the thread the pane just left,
			// so the final snapshot below would show the successor `↻`
			// (resumable) rather than `●` (live) for this whole pass. Gather and
			// re-enrich again, the same way the pass started, now that the
			// reconciled data is what the resolver sees.
			live, err = fleet.Gather(ctx, database, environment, data,
				request.ReadOnly,
				warn,
				stderr,
			)
			if err != nil {
				return stop(" gather", err)
			}
			data, err = fleet.EnrichLive(ctx, database, data, live)
			if err != nil {
				return stop(" live cache", err)
			}
		}
		var ok bool
		if lastSent, ok = sendRefresh(ctx, environment, request, data, live, false, updates); !ok {
			return false
		}
		if err = presence.record(environment, live); err != nil {
			// The rows just landed, but the parked poll cannot watch them:
			// the masthead says so instead of letting them read as watched.
			return fail(" presence", err, lastAttempt)
		}
		return true
	}
	if !firstPass() {
		return
	}

	cadence := newRefreshCadence(dependencies.activity)
	timer := refreshClock.NewTimer(cadence.interval)
	defer timer.Stop()
	// parked survives across iterations: once the cadence backs off past
	// fleetRefreshParkThreshold, the loop stops doing real passes on every
	// fire. Known Codex panes still get a bounded identity/descriptor check:
	// interacting in Codex does not stamp the picker's activity clock. A clear
	// wakes a full pass to publish the new binding and hidden predecessor.
	parked := false
	wake := dependencies.activity.Wake()
	// parkedRollouts is the FDLinks-observed identity of every live Codex
	// PID as of the previous parked poll. fleet.ReconcileCodexPanes' observation
	// step runs a tmux capture-pane over every live Codex pane — the same
	// exec fleetRefreshParkPollInterval exists to avoid paying on every
	// fire — so a parked poll whose procfs probe reports the identical set
	// of rollouts, where every one of them is a shape procfs alone already
	// resolves (see fleet.CodexRolloutFingerprintsSkippable), skips that call
	// outright. Only a reconciliation without warnings populates this cache;
	// a full pass invalidates it so the next idle probe verifies the binding.
	var parkedRollouts map[int]fleet.CodexRolloutFingerprint
	var nextCodexProbe time.Time
	// codexProbeNeedsPass is the parked Codex identity check: it reports whether
	// reconciliation moved a binding and so a full pass must publish it.
	codexProbeNeedsPass := func() bool {
		if request.ReadOnly || len(live.Codex) == 0 || refreshClock.Now().Before(nextCodexProbe) {
			return false
		}
		nextCodexProbe = refreshClock.Now().Add(fleetRefreshCodexPollInterval)
		probe := gather.Snapshot{Panes: live.Panes}
		probe.Codex, err = gather.RefreshCodexHeldRollouts(
			gather.NewProcFS(environment.Paths.ProcRoot), live.Codex, environment.Paths.Roots[pfmengine.Codex],
		)
		if err != nil {
			warn(fmt.Sprintf("Codex idle identity probe: %v", err))
		}
		fingerprints := fleet.CodexRolloutFingerprints(probe.Codex)
		unchanged := fleet.CodexRolloutFingerprintsEqual(parkedRollouts, fingerprints)
		if unchanged && fleet.CodexRolloutFingerprintsSkippable(probe.Codex) {
			// No live Codex PID's FDLinks-observed rollout moved since
			// the previous poll, AND every one of them already has an
			// answer procfs alone can stand behind (a held rollout, or a
			// conflict/error state fleet.ObserveCodexPanes overrides
			// regardless of pane text) — nothing a capture-pane could
			// tell reconciliation that procfs has not already settled.
			// A rollout-LESS Codex process (DetectCodexThreads' normal
			// shape since Codex 0.146.1 — no open rollout fd at all) is
			// never skippable: procfs has no opinion for it, so the
			// pane's own screen is the only signal there is.
			return false
		}
		// A warning can mean the binding was retained for retry. Cache only
		// a fully verified pass; an unchanged rollout is not proof that
		// the previous database write succeeded.
		verified := true
		changed := fleet.ReconcileCodexPanes(ctx, database, probe, environment.Runtime(), func(message string) {
			verified = false
			warn(message)
		})
		if verified {
			parkedRollouts = fingerprints
		} else {
			parkedRollouts = nil
		}
		return changed
	}
	// polls counts parked polls whose presence was unchanged. Every
	// ceil(fleetRefreshInterval / fleetRefreshParkPollInterval)-th one (6 s at
	// the defaults) re-reads the rows' working state over the LAST pass's
	// composed output — a stat per transcript, no tmux and no process scan — so
	// a chat that goes busy or idle while the picker is parked shows it within
	// fleetRefreshInterval rounded up to the park poll.
	polls := 0
	refreshParkedFacts := func() bool {
		polls++
		every := int((fleetRefreshInterval + fleetRefreshParkPollInterval - 1) / fleetRefreshParkPollInterval)
		// A blind presence probe (no baseline) leaves its stale marker up: a
		// facts snapshot would clear it while nothing watches the chat set.
		if request.Facts == nil || !presence.have || polls%max(every, 1) != 0 || !hasLiveRow(lastSent.output.Rows) {
			return true
		}
		reread := environment
		reread.NowNS = refreshClock.Now().UnixNano()
		snapshot := buildSnapshot(ctx, reread, request, lastSent.output)
		if !rowFactsMoved(lastSent.snapshot, snapshot) {
			return true
		}
		if !publishSnapshot(ctx, snapshot, updates) {
			return false
		}
		lastSent.snapshot = snapshot
		return true
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C():
			// A failing refresh backs off: the timer (the park poll, 2 s) fires
			// long before the retry is due, and a full pass per fire would
			// cost seconds of CPU each for as long as the failure lasts. The
			// stale marker stays up and its age keeps counting meanwhile.
			if failStreak > 0 {
				if wait := retryAt.Sub(refreshClock.Now()); wait > 0 {
					timer.Reset(wait)
					continue
				}
			}
		case <-wake:
			// A keystroke lands here, not on the timer. A fleet refreshed
			// moments ago is fresh enough; one the backoff left behind is
			// refreshed now, without waiting out the park poll or a failure
			// backoff: the user is looking at a marked-stale fleet.
			if refreshClock.Now().Sub(lastAttempt) < fleetRefreshStaleAfter {
				continue
			}
		}
		// Rearm BEFORE the pass, never after it. The body below leaves through
		// several `continue`s on transient errors, and a Reset parked at the
		// bottom would be skipped by every one of them — the stream would go
		// permanently silent on the first gather hiccup, which reads on screen
		// as a fleet that simply stopped changing.
		next := cadence.next()
		if parked && next >= fleetRefreshParkThreshold && !pendingRefresh {
			timer.Reset(fleetRefreshParkPollInterval)
			// The parked poll's question is "did the set of chats change?",
			// answered with syscalls only (gather.PresenceProbe): a full pass
			// costs seconds of CPU, but a parked picker that never looked again
			// showed a fleet frozen at the moment it parked. Moved falls
			// through to a full pass. The presence probe itself forks and
			// execs nothing; an unmoved poll then pays only the Codex identity
			// probe, which with a live Codex pane execs (lsof on macOS, a tmux
			// capture-pane for a rollout-less Codex) once per
			// fleetRefreshCodexPollInterval, as it did before the probe.
			moved, presenceErr := presence.moved(environment, live, refreshClock.Now())
			if presenceErr != nil {
				warn(fmt.Sprintf("parked presence probe: %v", presenceErr))
			}
			if !moved && !codexProbeNeedsPass() {
				if !refreshParkedFacts() {
					return
				}
				continue
			}
		}
		parked = next >= fleetRefreshParkThreshold
		if parked {
			timer.Reset(fleetRefreshParkPollInterval)
		} else {
			timer.Reset(next)
		}
		pendingRefresh = true
		passStart := refreshClock.Now()
		lastAttempt = passStart
		environment, err = fleet.ResolveEnv(request.fleetRequest())
		if err != nil {
			if !fail("", err, passStart) {
				return
			}
			continue
		}
		presence.begin(environment, passStart)
		if request.View == compose.DefaultView {
			data, err = fleet.LoadDefaultData(ctx, database)
		} else {
			data, err = fleet.LoadData(ctx, database)
		}
		if err != nil {
			if !fail("", err, passStart) {
				return
			}
			continue
		}
		live, err = fleet.Gather(ctx, database, environment, data,
			request.ReadOnly,
			warn,
			stderr,
		)
		if err != nil {
			if !fail(" gather", err, passStart) {
				return
			}
			continue
		}
		data, err = fleet.EnrichLive(ctx, database, data, live)
		if err != nil {
			if !fail(" live cache", err, passStart) {
				return
			}
			continue
		}
		movedBinding := false
		if !request.ReadOnly {
			movedBinding = fleet.ReconcileCodexPanes(ctx, database, live, environment.Runtime(), warn)
		}
		if _, ok := sendRefresh(ctx, environment, request, data, live, true, updates); !ok {
			return
		}
		if indexer == nil {
			// The first pass failed before it could build the indexer.
			if indexer, err = newIndexer(database); err != nil {
				if !fail(" index", err, passStart) {
					return
				}
				continue
			}
		}
		if _, err = indexer.Run(ctx, fleetindex.Options{
			PriorityCWD:  environment.CurrentDir,
			PriorityOnly: true,
		}); err != nil {
			if !fail(" index", err, passStart) {
				return
			}
			continue
		}
		data, err = fleet.LoadData(ctx, database)
		if err != nil {
			if !fail("", err, passStart) {
				return
			}
			continue
		}
		if movedBinding {
			// Same stale-snapshot trap as the first pass above: `live` was
			// gathered before fleet.ReconcileCodexPanes moved a binding, so the
			// resolver's answer for that pane's process still names the thread
			// it just left. Gather and re-enrich before the final snapshot,
			// or the successor renders `↻` until the next tick.
			live, err = fleet.Gather(ctx, database, environment, data, request.ReadOnly, warn, stderr)
			if err != nil {
				if !fail(" gather", err, passStart) {
					return
				}
				continue
			}
			data, err = fleet.EnrichLive(ctx, database, data, live)
			if err != nil {
				if !fail(" live cache", err, passStart) {
					return
				}
				continue
			}
		}
		var ok bool
		if lastSent, ok = sendRefresh(ctx, environment, request, data, live, false, updates); !ok {
			return
		}
		pendingRefresh, failStreak = false, 0
		if err = presence.record(environment, live); err != nil {
			// The rows just landed, but the parked poll cannot watch them:
			// the masthead says so instead of letting them read as watched.
			if !fail(" presence", err, passStart) {
				return
			}
		}
		// A full pass can publish while reconciliation reports a retryable
		// failure. Let the first parked poll verify the binding before caching.
		parkedRollouts = nil
	}
}

// primaryWriteback decides whether an ls session's picker outcome is worth
// persisting. Zero (and anything non-positive) is never a real account — it
// is the zero value ui.Outcome carries before a picker has ever reported a
// deliberate choice — so it means "nothing to save", not "save account 0".
// Treating it as a real value sent it straight into fleet.SetPrimaryAccount's
// roster check, which rejected it and aborted the whole `pfm ls` run before
// the picker's actual selection ever executed. A cancelled
// picker (Esc/⌃C) never writes either: a wizard's account choice is only a
// pending intent until the picker exits deliberately. An outcome that
// already matches the persisted primary has nothing new to write.
func primaryWriteback(kind ui.OutcomeKind, account, current int) (int, bool) {
	if kind == ui.OutcomeCancelled || account <= 0 || account == current {
		return 0, false
	}
	return account, true
}

func refreshWorkbenches(request scanRequest, discover func([]string) ([]workbench.Bench, []workbench.WalkError)) error {
	var values paths.Values
	if request.Runtime != nil {
		values = request.Runtime.Paths
	} else {
		resolved, err := pfmconfig.ResolvePaths()
		if err != nil {
			return fmt.Errorf("resolve workbench cache paths: %w", err)
		}
		values = resolved
	}
	dirs := append([]string(nil), request.RepoRoots...)
	var roots []string
	var faults []workbench.WalkError
	currentDir, err := os.Getwd()
	if err != nil {
		faults = append(faults, workbench.WalkError{
			Root: ".", Path: ".", Err: fmt.Errorf("read workbench discovery directory: %w", err),
		})
	} else {
		dirs = append(dirs, currentDir)
	}
	seen := make(map[string]bool)
	for _, dir := range dirs {
		managed, err := workbench.ManagedRoots([]string{dir})
		if err != nil {
			faults = append(faults, workbench.WalkError{Root: dir, Path: dir, Err: err})
			continue
		}
		for _, root := range managed {
			if !seen[root] {
				roots = append(roots, root)
				seen[root] = true
			}
		}
	}
	slices.Sort(roots)
	if discover == nil {
		discover = workbench.Discover
	}
	benches, walkErrors := discover(roots)
	return workbench.WriteCache(paths.WorkbenchCache(values), benches, append(faults, walkErrors...))
}

// isDirectory is the picker's answer to an `@` path: a directory that exists
// now. A path that is missing, or runs through a file, is no place; any other
// stat failure is an error the picker names, never "no match".
func isDirectory(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
	return info.IsDir(), nil
}
