// Package fleet takes one snapshot of every chat on the machine: an index
// delta over transcripts and rollouts, a live gather of tmux panes and engine
// processes, the Codex pane reconcile, and the composed rows. The picker,
// `pfm chat ls`, MCP chat_ls and every verb that resolves a chat by name read
// the fleet through Scan, so a chat one surface can see is a chat all of them
// can address.
package fleet

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"

	"hostops/pfm/internal/clock"
	"hostops/pfm/internal/compose"
	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/gather"
	fleetindex "hostops/pfm/internal/index"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/store"
)

// TestNowNSEnv pins the scan clock under test — a jail's fixtures carry fixed
// timestamps, and a real clock would age them out of every recency cap.
const TestNowNSEnv = "PFM_TEST_NOW_NS"

// Request is one scan's scope.
type Request struct {
	View compose.View
	// ReadOnly scans write nothing: no tmux window renames and no Codex pane
	// reconcile. Every verb that only resolves a target scans read-only.
	ReadOnly bool
	// Runtime is the process's loaded machine policy. Nil resolves paths and
	// runs on the default config.
	Runtime *pfmconfig.Runtime
}

// Env is the machine state one scan composes against.
type Env struct {
	Paths      paths.Values
	Config     pfmconfig.Config
	CurrentDir string
	NowNS      int64
	// Primary is the operator's primary Claude account (see PrimaryAccount).
	Primary int
}

// Runtime is the Env's machine policy in the shape every fleet writer takes.
func (env Env) Runtime() pfmconfig.Runtime {
	return pfmconfig.Runtime{Config: env.Config, Paths: env.Paths}
}

// Result is one composed snapshot of the fleet.
type Result struct {
	Output   compose.Output
	Live     gather.Snapshot
	Counters fleetindex.Counters
	Env      Env
}

// Scan runs the index delta, gathers live state, reconciles Codex panes (not
// in a ReadOnly scan), and composes the rows. Gather warnings print to stderr.
func Scan(
	ctx context.Context,
	database *store.Store,
	request Request,
	stderr io.Writer,
) (Result, error) {
	env, err := ResolveEnv(request)
	if err != nil {
		return Result{}, err
	}
	indexer, err := fleetindex.NewWithRoots(database, env.Paths, env.Paths.Roots)
	if err != nil {
		return Result{}, err
	}
	counters, err := indexer.Run(ctx, fleetindex.Options{
		PriorityCWD: env.CurrentDir,
	})
	if err != nil {
		return Result{}, err
	}
	data, err := LoadData(ctx, database)
	if err != nil {
		return Result{}, err
	}
	live, err := Gather(ctx, database, env, data, request.ReadOnly, PrintWarn(stderr), stderr)
	if err != nil {
		return Result{}, err
	}
	// The one-shot path reconciles too: a /clear observed here must not wait
	// for somebody to open the picker before the fleet stops pointing at the
	// thread it replaced. A read-only scan still writes nothing.
	if !request.ReadOnly && ReconcileCodexPanes(ctx, database, live, env.Runtime(), PrintWarn(stderr)) {
		data, err = LoadData(ctx, database)
		if err != nil {
			return Result{}, err
		}
		// A pass that moved a binding changed what the resolver answers for
		// the pane's own process: `live` above was gathered BEFORE the move,
		// so its LiveCodex.RolloutPath still names the thread the pane just
		// left. Composing from that snapshot renders the successor `↻`
		// (resumable) for this whole call instead of `●` (live) — gather
		// again against the reconciled data before this call hands anybody
		// a row.
		live, err = Gather(ctx, database, env, data, request.ReadOnly, PrintWarn(stderr), stderr)
		if err != nil {
			return Result{}, err
		}
	}
	return Result{
		Output:   ComposeFleet(env, request.View, data, live),
		Live:     live,
		Counters: counters,
		Env:      env,
	}, nil
}

// ScanCached composes from the index as it stands — no index delta, no live
// gather — for a picker's first frame while the real scan runs.
func ScanCached(
	ctx context.Context,
	database *store.Store,
	request Request,
) (Result, error) {
	env, err := ResolveEnv(request)
	if err != nil {
		return Result{}, err
	}
	var data Data
	if request.View == compose.DefaultView {
		data, err = LoadDefaultData(ctx, database)
	} else {
		data, err = LoadData(ctx, database)
	}
	if err != nil {
		return Result{}, err
	}
	return Result{Output: ComposeFleet(env, request.View, data, gather.Snapshot{}), Env: env}, nil
}

// ResolveRow looks id up in a compose pass over CURRENT database state
// plus a live gather — the picker's own source of truth for what exists right
// now — and reports the engine, rollout path, and live tmux address (socket
// name, pane id) of the row that carries it. It finds exactly the ids the
// picker displays, including a live agent row and a live Codex pane the
// index has not caught up with; an id nothing composes returns all empty
// strings, which leaves an ordinary kill free to refuse it as unindexed.
// Errors from the pass itself are swallowed the same way: a failed vouch
// attempt falls through to that same refusal rather than replacing the
// kill's own error. A row with no live socket returns an empty socket and
// pane, which is how kill.Manager tells a hide of a resumable-only chat from
// a hide of a live one — the latter also ends it.
//
// The rollout path lets kill.Manager resolve an UNINDEXED Codex lineage
// member to its root through the file's own session_meta header
// (resolveUnindexedCodexParent) instead of hiding under the member's own id
// — the id compose never carries once a full lineage IS indexed, since a
// Codex row is always keyed on its lineage root, never a member.
//
// This deliberately skips the indexer Scan runs: a caller resolving one
// id for a kill has no business reconciling the whole filesystem index, and
// a delta run can prune a transcript row whose file is not there YET — the
// exact row a kill right after spawning a chat is racing to catch.
func ResolveRow(
	ctx context.Context,
	database *store.Store,
	id string,
	stderr io.Writer,
	runtime *pfmconfig.Runtime,
) (engine pfmengine.ID, rolloutPath, socket, paneID string) {
	request := Request{View: compose.AllView, Runtime: runtime}
	env, err := ResolveEnv(request)
	if err != nil {
		return "", "", "", ""
	}
	data, err := LoadData(ctx, database)
	if err != nil {
		return "", "", "", ""
	}
	live, err := Gather(ctx, database, env, data, false, PrintWarn(stderr), stderr)
	if err != nil {
		return "", "", "", ""
	}
	rows := ComposeFleet(env, request.View, data, live).Rows
	for index := range rows {
		row := rows[index]
		if row.ID == id {
			return compose.EngineForKind(row.Kind), row.Path, row.Socket, row.PaneID
		}
	}
	return "", "", "", ""
}

// ResolveEnv reads the machine state a scan composes against: the request's
// runtime (or resolved defaults), the working directory, the clock
// (TestNowNSEnv under test), and the primary account.
func ResolveEnv(request Request) (Env, error) {
	var resolved paths.Values
	var machine pfmconfig.Config
	if request.Runtime != nil {
		resolved = request.Runtime.Paths
		machine = request.Runtime.Config
	} else {
		var err error
		resolved, err = paths.Resolve()
		if err != nil {
			return Env{}, err
		}
		machine = pfmconfig.Defaults(
			resolved.Home,
			resolved.Roots[pfmengine.Claude],
			resolved.FirstRoot(pfmengine.Codex),
		)
	}
	currentDir, err := os.Getwd()
	if err != nil {
		return Env{}, fmt.Errorf("read current directory: %w", err)
	}
	nowNS := clock.Real.Now().UnixNano()
	if value := (paths.OSEnv{}).Get(TestNowNSEnv); value != "" {
		parsed, parseErr := strconv.ParseInt(value, 10, 64)
		if parseErr != nil {
			return Env{}, fmt.Errorf("%s: %w", TestNowNSEnv, parseErr)
		}
		nowNS = parsed
	}
	return Env{
		Paths:      resolved,
		CurrentDir: currentDir,
		NowNS:      nowNS,
		Primary:    PrimaryAccount(resolved, machine),
		Config:     machine,
	}, nil
}

// Compose classifies, merges and sorts one view's rows from the loaded data
// and the live snapshot. It never writes.
func ComposeFleet(env Env, view compose.View, data Data, live gather.Snapshot) compose.Output {
	output := compose.Compose(compose.Input{
		Snapshot:         live,
		Transcripts:      data.Transcripts,
		Rollouts:         data.Rollouts,
		OpenCodeSessions: data.OpenCodeSessions,
		CxNames:          data.CxNames,
		Killed:           data.Killed,
		AccountRoots:     accountRoots(env.Config.Accounts),
		CodexHomes:       codexAccountRoots(env.Config.CodexAccounts),
		Options: compose.Options{
			View:                view,
			CurrentDir:          env.CurrentDir,
			CurrentSocket:       CurrentSocket(),
			PrimaryAccount:      env.Primary,
			CodexAccountIDs:     env.Config.CodexAccountIDs(),
			PrimaryCodexAccount: env.Config.PrimaryCodexAccount(),
			OpenCodeAccountIDs:  env.Config.OpenCodeAccountIDs(),
			PrimaryOpenCode:     env.Config.PrimaryOpenCodeAccount(),
			NowNS:               env.NowNS,
		},
	})
	if data.CachedCounts != nil {
		output.KilledCount = data.CachedCounts.Killed
		output.SuppressedCount = data.CachedCounts.Suppressed
	}
	return output
}
