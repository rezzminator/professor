package picker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"hostops/pfm/internal/clock"
	"hostops/pfm/internal/compose"
	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/fleet"
	"hostops/pfm/internal/fleetdb"
	"hostops/pfm/internal/gather"
	fleetindex "hostops/pfm/internal/index"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/store"
	"hostops/pfm/internal/ui"
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
)

// refreshCadence is one refresh stream's backoff state. It starts at
// fleetRefreshInterval and stretches by fleetRefreshGrowth after each pass
// that nobody interrupted, capped at fleetRefreshParkThreshold, so a picker
// being driven stays prompt. streamFleetRefreshesWith is what turns
// "capped" into "stopped" — see the park/poll split there.
type refreshCadence struct {
	activity  *ui.ActivityClock
	lastStamp int64
	interval  time.Duration
}

func newRefreshCadence(activity *ui.ActivityClock) *refreshCadence {
	return &refreshCadence{
		activity:  activity,
		lastStamp: activity.StampNS(),
		interval:  fleetRefreshInterval,
	}
}

// next reports how long to wait before the next pass. Any interaction since
// the previous call snaps the cadence back to fleetRefreshInterval; otherwise
// it grows, capped at fleetRefreshMaxInterval.
//
// A nil clock — every non-interactive caller — never backs off. An absent
// presence signal is a claim about US, not about the user, and must never be
// spent as evidence that nobody is there.
func (cadence *refreshCadence) next() time.Duration {
	if cadence.activity == nil {
		return fleetRefreshInterval
	}
	if stamp := cadence.activity.StampNS(); stamp != cadence.lastStamp {
		cadence.lastStamp = stamp
		cadence.interval = fleetRefreshInterval
		return cadence.interval
	}
	grown := time.Duration(float64(cadence.interval) * fleetRefreshGrowth)
	if grown > fleetRefreshParkThreshold {
		grown = fleetRefreshParkThreshold
	}
	cadence.interval = grown
	return cadence.interval
}

// bufferedWarnings collects gather warnings raised from the background
// refresh goroutine while an interactive picker owns the terminal (runLS),
// releasing them to stderr only once flush is called after Pick returns.
type bufferedWarnings struct {
	mu       sync.Mutex
	warnings []string
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

// flush prints every warning collected so far and clears the buffer, so a
// caller that flushes between picker frames never
// prints the same warning twice.
func (buffer *bufferedWarnings) flush(stderr io.Writer) {
	buffer.mu.Lock()
	pending := buffer.warnings
	buffer.warnings = nil
	buffer.mu.Unlock()
	for _, warning := range pending {
		fmt.Fprintf(stderr, "pfm: tmux probe warning: %s\n", warning)
	}
}

type scanRequest struct {
	View     compose.View
	Query    string
	ReadOnly bool
	Cache1H  bool
	NoSky    bool
	// Safe is the --safe flag verbatim (auto|on|off); resolveCosmosSafe
	// turns it into the snapshot's CosmosSafe bool at build time.
	Safe    string
	Runtime *pfmconfig.Runtime
	Comms   commsReader
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
	return fleet.Request{View: request.View, ReadOnly: request.ReadOnly, Runtime: request.Runtime}
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
	snapshot := buildSnapshot(ctx, result.Env, request, result.Output)
	snapshot.Refreshing = true
	return scanResult{Output: result.Output, Snapshot: snapshot, Paths: result.Env.Paths}, nil
}

// buildSnapshot is the picker's view of one composed pass: rows, account
// chrome, and the cosmos over the comms ledger.
func buildSnapshot(
	ctx context.Context,
	environment fleet.Env,
	request scanRequest,
	output compose.Output,
) ui.Snapshot {
	cosmos := compose.BuildCosmos(output.Rows, nil, environment.NowNS, true)
	if request.Comms != nil {
		events, err := request.Comms.CommsSince(
			ctx,
			environment.NowNS-int64(compose.CosmosWindow),
			compose.CosmosEventCap,
		)
		if err != nil {
			cosmos.Err = fmt.Errorf("read comms ledger: %w", err).Error()
		} else {
			cosmos = compose.BuildCosmos(output.Rows, events, environment.NowNS, true)
			if len(events) == compose.CosmosEventCap {
				cosmos.Warnings = append(cosmos.Warnings, compose.CosmosTruncationWarning)
			}
		}
	}
	machine := environment.Config
	return ui.Snapshot{
		Rows:                   output.Rows,
		View:                   request.View,
		KilledCount:            output.KilledCount,
		SuppressedCount:        output.SuppressedCount,
		PrimaryAccount:         environment.Primary,
		AccountIDs:             machine.AccountIDs(),
		AccountEmojis:          machine.AccountEmojis(),
		CodexPrimaryAccount:    machine.PrimaryCodexAccount(),
		CodexAccountIDs:        machine.CodexAccountIDs(),
		CodexAccountEmojis:     machine.CodexAccountEmojis(),
		OpenCodePrimaryAccount: machine.PrimaryOpenCodeAccount(),
		OpenCodeAccountIDs:     machine.OpenCodeAccountIDs(),
		Theme:                  machine.Theme,
		Cache1H:                request.Cache1H,
		NowNS:                  environment.NowNS,
		InitialQuery:           request.Query,
		NoSky:                  request.NoSky,
		CosmosSafe:             resolveCosmosSafe(request.Safe, (paths.OSEnv{}).Get("TERM_PROGRAM")),
		Cosmos:                 cosmos,
	}
}

type indexRunner interface {
	Run(context.Context, fleetindex.Options) (fleetindex.Counters, error)
}

type refreshDependencies struct {
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
	environment, err := fleet.ResolveEnv(request.fleetRequest())
	if err != nil {
		writeRefreshError(ctx, stderr, "", err)
		return
	}
	var data fleet.Data
	if request.View == compose.DefaultView {
		data, err = fleet.LoadDefaultData(ctx, database)
	} else {
		data, err = fleet.LoadData(ctx, database)
	}
	if err != nil {
		writeRefreshError(ctx, stderr, "", err)
		return
	}
	live, err := fleet.Gather(ctx, database, environment, data,
		request.ReadOnly,
		warn,
		stderr,
	)
	if err != nil {
		writeRefreshError(ctx, stderr, " gather", err)
		return
	}
	data, err = fleet.EnrichLive(ctx, database, data, live)
	if err != nil {
		writeRefreshError(ctx, stderr, " live cache", err)
		return
	}
	if !sendRefresh(ctx, environment, request, data, live, true, updates) {
		return
	}
	movedBinding := false
	if !request.ReadOnly {
		movedBinding = fleet.ReconcileCodexPanes(ctx, database, live, environment.Runtime(), warn)
	}

	newIndexer := dependencies.newIndexer
	if newIndexer == nil {
		newIndexer = func(database *store.Store) (indexRunner, error) {
			return fleetindex.NewWithRoots(database, environment.Paths, environment.Paths.Roots)
		}
	}
	indexer, err := newIndexer(database)
	if err != nil {
		writeRefreshError(ctx, stderr, " index", err)
		return
	}
	if _, err := indexer.Run(ctx, fleetindex.Options{
		PriorityCWD:  environment.CurrentDir,
		PriorityOnly: true,
	}); err != nil {
		writeRefreshError(ctx, stderr, " project index", err)
		return
	}
	data, err = fleet.LoadData(ctx, database)
	if err != nil {
		writeRefreshError(ctx, stderr, "", err)
		return
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
			writeRefreshError(ctx, stderr, " gather", err)
			return
		}
		data, err = fleet.EnrichLive(ctx, database, data, live)
		if err != nil {
			writeRefreshError(ctx, stderr, " live cache", err)
			return
		}
	}
	if !sendRefresh(ctx, environment, request, data, live, false, updates) {
		return
	}

	cadence := newRefreshCadence(dependencies.activity)
	refreshClock := dependencies.clock
	if refreshClock == nil {
		refreshClock = clock.Real
	}
	timer := refreshClock.NewTimer(cadence.interval)
	defer timer.Stop()
	// parked survives across iterations: once the cadence backs off past
	// fleetRefreshParkThreshold, the loop stops doing real passes on every
	// fire. Known Codex panes still get a bounded identity/descriptor check:
	// interacting in Codex does not stamp the picker's activity clock. A clear
	// wakes a full pass to publish the new binding and hidden predecessor.
	parked := false
	// A failed publication must be retried even if reconciliation already
	// committed the binding and therefore reports no further identity change.
	pendingRefresh := false
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
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C():
		}
		// Rearm BEFORE the pass, never after it. The body below leaves through
		// several `continue`s on transient errors, and a Reset parked at the
		// bottom would be skipped by every one of them — the stream would go
		// permanently silent on the first gather hiccup, which reads on screen
		// as a fleet that simply stopped changing.
		next := cadence.next()
		if parked && next >= fleetRefreshParkThreshold && !pendingRefresh {
			timer.Reset(fleetRefreshParkPollInterval)
			if request.ReadOnly || len(live.Codex) == 0 || refreshClock.Now().Before(nextCodexProbe) {
				continue
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
				continue
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
			if !changed {
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
		environment, err = fleet.ResolveEnv(request.fleetRequest())
		if err != nil {
			writeRefreshError(ctx, stderr, "", err)
			continue
		}
		if request.View == compose.DefaultView {
			data, err = fleet.LoadDefaultData(ctx, database)
		} else {
			data, err = fleet.LoadData(ctx, database)
		}
		if err != nil {
			if !writeRefreshError(ctx, stderr, "", err) {
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
			if !writeRefreshError(ctx, stderr, " gather", err) {
				return
			}
			continue
		}
		data, err = fleet.EnrichLive(ctx, database, data, live)
		if err != nil {
			if !writeRefreshError(ctx, stderr, " live cache", err) {
				return
			}
			continue
		}
		movedBinding := false
		if !request.ReadOnly {
			movedBinding = fleet.ReconcileCodexPanes(ctx, database, live, environment.Runtime(), warn)
		}
		if !sendRefresh(ctx, environment, request, data, live, true, updates) {
			return
		}
		if _, err = indexer.Run(ctx, fleetindex.Options{
			PriorityCWD:  environment.CurrentDir,
			PriorityOnly: true,
		}); err != nil {
			if !writeRefreshError(ctx, stderr, " index", err) {
				return
			}
			continue
		}
		data, err = fleet.LoadData(ctx, database)
		if err != nil {
			if !writeRefreshError(ctx, stderr, "", err) {
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
				if !writeRefreshError(ctx, stderr, " gather", err) {
					return
				}
				continue
			}
			data, err = fleet.EnrichLive(ctx, database, data, live)
			if err != nil {
				if !writeRefreshError(ctx, stderr, " live cache", err) {
					return
				}
				continue
			}
		}
		if !sendRefresh(ctx, environment, request, data, live, false, updates) {
			return
		}
		pendingRefresh = false
		// A full pass can publish while reconciliation reports a retryable
		// failure. Let the first parked poll verify the binding before caching.
		parkedRollouts = nil
	}
}

func sendRefresh(
	ctx context.Context,
	environment fleet.Env,
	request scanRequest,
	data fleet.Data,
	live gather.Snapshot,
	refreshing bool,
	updates chan<- ui.Snapshot,
) bool {
	snapshot := buildSnapshot(ctx, environment, request, fleet.ComposeFleet(environment, request.View, data, live))
	snapshot.Refreshing = refreshing
	select {
	case updates <- snapshot:
		return true
	case <-ctx.Done():
		return false
	}
}

// primaryWriteback decides whether an ls session's picker outcome is worth
// persisting. Zero (and anything non-positive) is never a real account — it
// is the zero value ui.Outcome carries before a picker has ever reported a
// deliberate choice — so it means "nothing to save", not "save account 0".
// Treating it as a real value sent it straight into fleet.SetPrimaryAccount's
// roster check, which rejected it and aborted the whole `pfm ls` run before
// the picker's actual selection ever executed. A cancelled
// picker (Esc/⌃C) never writes either: a ⌃S account switch is only a
// pending intent until the picker exits deliberately. An outcome that
// already matches the persisted primary has nothing new to write.
func primaryWriteback(kind ui.OutcomeKind, account, current int) (int, bool) {
	if kind == ui.OutcomeCancelled || account <= 0 || account == current {
		return 0, false
	}
	return account, true
}

func inBunker() bool {
	return fleet.CurrentSocket() == "vsct"
}
