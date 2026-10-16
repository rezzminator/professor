package hookentry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/reload"
)

// clientSplit is the tmux clients of a chat session set against its registry.
type clientSplit struct {
	// gone are the registered clients that vanished without a detach hook, now
	// removed from the registry; the caller releases their panes.
	gone map[string]herdrClient
	// adopted are the live clients that were unregistered and run in a Herdr
	// pane, now in the registry.
	adopted []string
	// errs are the live clients whose pane could not be resolved (named, never
	// read as "outside Herdr"); a client outside Herdr is no error.
	errs error
}

// markMoved counts every adopted client that resolved as moved: its pane has
// not been told the state, name and title yet.
func (split clientSplit) markMoved(resolved map[string]herdrClient, moved map[string]bool) {
	for _, pid := range split.adopted {
		if _, ok := resolved[pid]; ok {
			moved[pid] = true
		}
	}
}

// liveClients splits the registered clients into those still attached and
// those that vanished without a detach hook; the latter are removed from the
// registry. With adopt, every live client not yet registered whose own
// environment runs in a Herdr pane joins the registry (the one resolver
// decides the pane), so a record born before the hooks, across a Herdr restart
// or never attached heals on any run. The one tmux call is the steady state's
// already: a registry with clients lists them anyway.
func (reporter *herdrReporter) liveClients(
	ctx context.Context,
	session string,
	state *herdrStored,
	adopt bool,
) (split clientSplit, err error) {
	if len(state.Clients) == 0 && !adopt {
		return clientSplit{}, nil
	}
	output, err := reporter.deps.tmux(
		ctx, reporter.options.socket, "list-clients", "-t", session, "-F", "#{client_pid}",
	)
	if err != nil {
		return clientSplit{}, fmt.Errorf("list tmux clients of %s: %w", session, err)
	}
	live := map[string]bool{}
	for _, pid := range strings.Fields(output) {
		live[pid] = true
	}
	split.gone = map[string]herdrClient{}
	for pid, client := range state.Clients {
		if !live[pid] {
			split.gone[pid] = client
			delete(state.Clients, pid)
		}
	}
	if !adopt {
		return split, nil
	}
	var errs []error
	resolver := reporter.resolver()
	for _, pid := range slices.Sorted(maps.Keys(live)) {
		if _, registered := state.Clients[pid]; registered {
			continue
		}
		number, err := strconv.Atoi(pid)
		if err != nil {
			errs = append(errs, fmt.Errorf("tmux client %q of %s is no pid: %w", pid, session, err))
			continue
		}
		location, found, err := resolver.Resolve(ctx, number)
		if err != nil {
			errs = append(errs, fmt.Errorf("adopt tmux client %s of %s (nothing registered): %w", pid, session, err))
			continue
		}
		if !found {
			continue
		}
		if state.Clients == nil {
			state.Clients = map[string]herdrClient{}
		}
		state.Clients[pid] = herdrClient{Pane: location.Pane, Terminal: location.Terminal, Socket: location.Socket}
		split.adopted = append(split.adopted, pid)
	}
	split.errs = errors.Join(errs...)
	return split, nil
}

// resolver is the one rule for "which Herdr pane runs this client": the
// client's own environment, this run's cached pane listing and one
// pane.process_info per candidate pane.
func (reporter *herdrReporter) resolver() herdr.Resolver {
	resolver := herdr.WireResolver(reporter.deps.environ, herdr.Call, reporter.nextSent)
	resolver.Panes = reporter.listPanes
	return resolver
}

// releaseAll releases the panes of clients that detached or vanished; each
// pane is resolved first, so a moved chat's current pane is the one released.
// A client whose pane is already gone is expected, not an error: nothing is
// sent for it and no error row is written.
func (reporter *herdrReporter) releaseAll(ctx context.Context, engine string, clients map[string]herdrClient) error {
	var errs []error
	for _, pid := range sortedClientPIDs(clients) {
		client, _, err := reporter.currentPane(ctx, pid, clients[pid], false)
		if err != nil {
			// A pane that is already gone has nothing left to release.
			if !isPaneGone(err) {
				errs = append(errs, err)
			}
			continue
		}
		errs = append(errs, reporter.send(
			ctx,
			herdrTarget{pane: client.Pane, socket: client.Socket},
			&herdrAction{release: true, stored: herdrStored{Engine: engine}},
			reporter.nextSent(),
		))
	}
	return errors.Join(errs...)
}

func sortedClientPIDs(clients map[string]herdrClient) []string {
	pids := make([]string, 0, len(clients))
	for pid := range clients {
		pids = append(pids, pid)
	}
	sort.Strings(pids)
	return pids
}

// listPanes is the one pane.list of a Herdr socket for this run.
func (reporter *herdrReporter) listPanes(ctx context.Context, socket string) ([]herdr.Pane, error) {
	if listing, ok := reporter.panes[socket]; ok {
		return listing.panes, listing.err
	}
	listing := paneListing{}
	line, err := herdr.PaneList{Seq: reporter.nextSent()}.Line()
	if err == nil {
		var results []json.RawMessage
		if results, err = herdr.Call(ctx, socket, line); err == nil {
			listing.panes, err = herdr.ParsePanes(results[0])
		}
	}
	if err != nil {
		listing.err = fmt.Errorf("list panes: %w", err)
	}
	if reporter.panes == nil {
		reporter.panes = map[string]paneListing{}
	}
	reporter.panes[socket] = listing
	return listing.panes, listing.err
}

// paneGoneError is a client whose pane Herdr no longer lists: the pane closed
// while its chat lived on, which a release treats as already done.
type paneGoneError struct{ reason string }

func (gone paneGoneError) Error() string { return gone.reason }

func isPaneGone(err error) bool {
	var gone paneGoneError
	return errors.As(err, &gone)
}

// currentPane resolves a registered client to the pane its terminal is in NOW:
// a Herdr pane move keeps the terminal and assigns a new pane id, and the old
// id answers pane_not_found (or, reused, belongs to another chat). A terminal
// no pane lists any more (a Herdr restart assigns fresh terminals) and an entry
// with no recorded terminal and a gone pane id are resolved by the pane whose
// foreground holds the client pid, when walk allows the walk: a client already
// gone from tmux holds no pane. A client it cannot resolve is an error and
// nothing is sent to it: the stale id is never the fallback.
func (reporter *herdrReporter) currentPane(
	ctx context.Context,
	pid string,
	client herdrClient,
	walk bool,
) (herdrClient, bool, error) {
	fail := func(cause error) (herdrClient, bool, error) {
		return client, false, fmt.Errorf(
			"resolve pane of client %s (recorded %s, terminal %q): %w",
			pid,
			client.Pane,
			client.Terminal,
			cause,
		)
	}
	panes, err := reporter.listPanes(ctx, client.Socket)
	if err != nil {
		return fail(err)
	}
	resolved := client
	gone := fmt.Sprintf("pane %s is gone and no pane runs the client pid", client.Pane)
	if client.Terminal != "" {
		for _, pane := range panes {
			if pane.TerminalID == client.Terminal {
				resolved.Pane = pane.PaneID
				return resolved, resolved.Pane != client.Pane, nil
			}
		}
		gone = fmt.Sprintf("terminal %s is in no pane and no pane runs the client pid", client.Terminal)
		if !walk {
			return fail(paneGoneError{fmt.Sprintf("terminal %s is in no pane", client.Terminal)})
		}
	} else {
		for _, pane := range panes {
			if pane.PaneID == client.Pane {
				resolved.Terminal = pane.TerminalID
				return resolved, false, nil
			}
		}
	}
	number, err := strconv.Atoi(pid)
	if err != nil {
		return fail(fmt.Errorf("the client is no pid: %w", err))
	}
	pane, found, err := reporter.resolver().PaneRunning(ctx, client.Socket, number, panes, "")
	if err != nil {
		return fail(err)
	}
	if !found {
		return fail(paneGoneError{gone})
	}
	resolved.Pane, resolved.Terminal = pane.PaneID, pane.TerminalID
	return resolved, true, nil
}

// resolveClients resolves every registered client in place. It returns the
// clients it can send to, the pids whose pane id changed (they get the state,
// name and title again) and whether the registry changed and must be stored.
// A client it cannot resolve stays in the registry for the next run; when the
// caller is releasing, one whose pane is gone is no error.
func (reporter *herdrReporter) resolveClients(ctx context.Context, state *herdrStored, releasing bool) (
	resolved map[string]herdrClient, moved map[string]bool, changed bool, err error,
) {
	resolved, moved = map[string]herdrClient{}, map[string]bool{}
	var errs []error
	for _, pid := range sortedClientPIDs(state.Clients) {
		client, panesDiffer, err := reporter.currentPane(ctx, pid, state.Clients[pid], true)
		if err != nil {
			// A release has nothing to send to a pane that is already gone.
			if !releasing || !isPaneGone(err) {
				errs = append(errs, err)
			}
			continue
		}
		if client != state.Clients[pid] {
			state.Clients[pid] = client
			changed = true
		}
		resolved[pid] = client
		moved[pid] = panesDiffer
	}
	return resolved, moved, changed, errors.Join(errs...)
}

func movedClients(clients map[string]herdrClient, moved map[string]bool) map[string]herdrClient {
	only := map[string]herdrClient{}
	for pid, client := range clients {
		if moved[pid] {
			only[pid] = client
		}
	}
	return only
}

// attach brings the pane of a tmux client that just attached up to date: the
// one client pass (reconcile) adopts it when its own environment runs in a
// Herdr pane, and re-reports the stored state, name and title to it. The pane
// is the one whose foreground runs the client pid, asked first about the pane
// the client's own HERDR_PANE_ID names; a client outside Herdr changes
// nothing, a client no Herdr pane runs is an error and is never sent to.
func (reporter *herdrReporter) attach(ctx context.Context, session string, state herdrStored) error {
	if reporter.options.client == "" {
		return fmt.Errorf("attach needs --client {tmux client pid}")
	}
	return reporter.reconcile(ctx, session, state, true)
}

// idleAtLaunch gives a chat nothing has reported for yet the idle state of the
// engine that owns its session name: Codex fires SessionStart only at its first
// turn, so without it a fresh chat is invisible in Herdr until prompted. The
// seq stays untouched, so any engine event still wins; a session no engine owns
// reports nothing.
func (reporter *herdrReporter) idleAtLaunch(session string, state *herdrStored) {
	id, owned := pfmengine.FromSocket(session)
	if !owned {
		return
	}
	state.Engine, state.State = herdr.Label(id), string(herdr.StateIdle)
}

// sync is a client's resize: the client pass adopts the client when it runs in
// a Herdr pane and re-binds every registered client whose pane moved, as a pane
// move happens without tmux copying anything.
func (reporter *herdrReporter) sync(ctx context.Context, session string, state herdrStored) error {
	if reporter.options.client == "" {
		return fmt.Errorf("sync needs --client {tmux client pid}")
	}
	return reporter.reconcile(ctx, session, state, false)
}

// reconcile is the one client pass of the tmux-fired verbs (attach, sync,
// name): release the clients tmux no longer lists, adopt the live ones that
// run in a Herdr pane, resolve every registered client to its pane now, and
// bring each new or moved pane up to date with the stored state, name and
// title. With atLaunch (an attach), a chat nothing reported for yet is stored
// idle under its socket's engine first (idleAtLaunch).
func (reporter *herdrReporter) reconcile(
	ctx context.Context,
	session string,
	state herdrStored,
	atLaunch bool,
) error {
	split, err := reporter.liveClients(ctx, session, &state, true)
	if err != nil {
		return err
	}
	clients, moved, changed, resolveErr := reporter.resolveClients(ctx, &state, false)
	split.markMoved(clients, moved)
	errs := []error{split.errs, resolveErr, reporter.releaseAll(ctx, reporter.engineLabel(state), split.gone)}
	if atLaunch && len(split.adopted) > 0 && state.State == "" {
		reporter.idleAtLaunch(session, &state)
		changed = true
	}
	if changed || len(split.gone) > 0 || len(split.adopted) > 0 {
		if err := reporter.writeState(ctx, session, state); err != nil {
			return errors.Join(append(errs, err)...)
		}
	}
	movedNow := movedClients(clients, moved)
	for _, pid := range sortedClientPIDs(movedNow) {
		if state.State != "" {
			errs = append(errs, reporter.send(
				ctx,
				herdrTarget{pane: movedNow[pid].Pane, socket: movedNow[pid].Socket},
				&herdrAction{stored: state},
				reporter.nextSent(),
			))
		}
	}
	errs = append(errs, reporter.nameSync(ctx, session, state, clientTargets(clients), clientTargets(movedNow)))
	return errors.Join(errs...)
}

// detach releases the pane of a registered client that detached; a client
// that was never registered (one without Herdr) changes nothing. tmux knows no
// pid for a client that already left, so a detach hook without --client sweeps:
// every registered client tmux no longer lists is released.
func (reporter *herdrReporter) detach(ctx context.Context, session string, state herdrStored) error {
	if reporter.options.client == "" {
		return reporter.sweep(ctx, session, state)
	}
	client, registered := state.Clients[reporter.options.client]
	if !registered {
		return nil
	}
	delete(state.Clients, reporter.options.client)
	if err := reporter.writeState(ctx, session, state); err != nil {
		return err
	}
	return reporter.releaseAll(
		ctx,
		reporter.engineLabel(state),
		map[string]herdrClient{reporter.options.client: client},
	)
}

func clientTargets(clients map[string]herdrClient) []herdrTarget {
	targets := make([]herdrTarget, 0, len(clients))
	for _, pid := range sortedClientPIDs(clients) {
		targets = append(targets, herdrTarget{pane: clients[pid].Pane, socket: clients[pid].Socket})
	}
	return targets
}

// blankTitle empties the chat server's set-titles-string at a session end. The
// outer title a tmux client writes (a Herdr pane's terminal title) is that
// format, and the engine's pane title empties as it exits, so tmux's last write
// would leave "⬢ " behind it; blanked, tmux writes the empty title at once and
// every later render stays empty until the server dies, or until name-sync
// rewrites the option on a server that outlived its end (Claude's /logout, an
// OpenCode dispose). Under set-titles off the string is inert. Only a server
// that ends with its chat is blanked: an engine in the user's own tmux leaves
// that live server's titles alone, and so does a reload's /exit, whose engine
// respawns on the same server (reload.InFlight), and an engine whose server
// keeps another pane. A reload or a pane that cannot be ruled out leaves the
// title as it is.
func (reporter *herdrReporter) blankTitle(ctx context.Context, action *herdrAction) error {
	socketName := filepath.Base(reporter.options.socket)
	if !action.release || !gather.IsChatSocketName(socketName) {
		return nil
	}
	pane := reporter.tmuxPane
	if pane == "" || reporter.deps.sidDir == "" {
		return fmt.Errorf(
			"outer terminal title left as is at session end: no pane (%q) or SID directory (%q) to probe a reload with",
			pane, reporter.deps.sidDir,
		)
	}
	inFlight, err := reload.InFlight(reporter.deps.sidDir, socketName, pane)
	if err != nil {
		return fmt.Errorf("outer terminal title left as is at session end: probe the reload of %s %s: %w",
			socketName, pane, err)
	}
	if inFlight {
		return nil
	}
	panes, err := reporter.deps.tmux(ctx, reporter.options.socket, "list-panes", "-a", "-F", "#{pane_id}")
	if err != nil {
		return fmt.Errorf("outer terminal title left as is at session end: list the panes of %s: %w", socketName, err)
	}
	for _, other := range strings.Fields(panes) {
		if other != pane {
			return nil
		}
	}
	if _, err := reporter.deps.tmux(
		ctx, reporter.options.socket, "set-option", "-g", "set-titles-string", "",
	); err != nil {
		return fmt.Errorf("blank the outer terminal title at session end (set-titles-string): %w", err)
	}
	return nil
}

// sweep releases and forgets the registered clients tmux no longer lists.
func (reporter *herdrReporter) sweep(ctx context.Context, session string, state herdrStored) error {
	split, err := reporter.liveClients(ctx, session, &state, false)
	if err != nil || len(split.gone) == 0 {
		return err
	}
	if err := reporter.writeState(ctx, session, state); err != nil {
		return err
	}
	return reporter.releaseAll(ctx, reporter.engineLabel(state), split.gone)
}

// herdrStateUnknown is the state of a pane whose chat no engine hook has
// reported for: Herdr's own "unknown", an honest "not known" over a guessed idle.
const herdrStateUnknown = "unknown"

// HerdrReannounce brings the chat server at socket back onto Herdr: for each
// session it adopts every live tmux client whose own environment runs in a
// Herdr pane, then re-reports the stored state (unknown when no engine event
// ever stored one, the engine named by the socket's prefix) and the name to
// every live client, returning the pane ids reached. `pfm name-sync --apply`
// calls it per chat server, so a chat that predates the hooks or a Herdr
// restart heals without being re-opened. A host with no Herdr reaches nothing
// and says so with no error.
func HerdrReannounce(ctx context.Context, socket string) ([]string, error) {
	return reannounceOn(ctx, socket, realHerdrDeps())
}

func reannounceOn(ctx context.Context, socket string, deps herdrDeps) ([]string, error) {
	engine, owned := pfmengine.FromSocket(filepath.Base(socket))
	if !owned {
		return nil, fmt.Errorf("herdr re-announce %s: not a chat server socket (no engine owns its prefix)", socket)
	}
	reporter := &herdrReporter{
		deps:    deps,
		stderr:  io.Discard,
		options: herdrOptions{engine: engine, event: herdr.EventSync, socket: socket},
		seq:     uint64(deps.now().UnixNano()),
	}
	if !reporter.hostHasHerdr() {
		return nil, nil
	}
	output, err := deps.tmux(ctx, socket, "list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil, fmt.Errorf("herdr re-announce %s: list the sessions: %w", socket, err)
	}
	var (
		errs    []error
		reached = map[string]bool{}
	)
	for _, session := range strings.Fields(output) {
		panes, err := reporter.reannounceSession(ctx, session)
		if err != nil {
			errs = append(errs, fmt.Errorf("herdr re-announce %s session %s: %w", socket, session, err))
		}
		for _, pane := range panes {
			reached[pane] = true
		}
	}
	return slices.Sorted(maps.Keys(reached)), errors.Join(errs...)
}

// reannounceSession is one session's re-announce, under the session lock: the
// client pass (adopt, re-bind, release) and then the state and the name to
// every live client. It returns the panes the state reached.
func (reporter *herdrReporter) reannounceSession(ctx context.Context, session string) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, herdrDeadline)
	defer cancel()
	reporter.session = session
	unlock, err := reporter.lock(ctx, session)
	if err != nil {
		return nil, err
	}
	defer unlock()
	state, err := reporter.readState(ctx, session)
	if err != nil {
		return nil, err
	}
	split, err := reporter.liveClients(ctx, session, &state, true)
	if err != nil {
		return nil, err
	}
	clients, _, rebound, resolveErr := reporter.resolveClients(ctx, &state, false)
	errs := []error{split.errs, resolveErr, reporter.releaseAll(ctx, reporter.engineLabel(state), split.gone)}
	changed := rebound || len(split.gone) > 0 || len(split.adopted) > 0
	if state.State == "" {
		state.Engine, state.State, changed = herdr.Label(reporter.options.engine), herdrStateUnknown, true
	}
	if state.Engine == "" {
		state.Engine, changed = herdr.Label(reporter.options.engine), true
	}
	if changed {
		if err := reporter.writeState(ctx, session, state); err != nil {
			return nil, errors.Join(append(errs, err)...)
		}
	}
	var reached []string
	for _, pid := range sortedClientPIDs(clients) {
		client := clients[pid]
		target := herdrTarget{pane: client.Pane, socket: client.Socket}
		if err := reporter.send(ctx, target, &herdrAction{stored: state}, reporter.nextSent()); err != nil {
			errs = append(errs, err)
			continue
		}
		reached = append(reached, client.Pane)
	}
	every := clientTargets(clients)
	errs = append(errs, reporter.nameSync(ctx, session, state, every, every))
	return reached, errors.Join(errs...)
}
