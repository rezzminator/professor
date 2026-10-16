package hookentry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// liveClients splits the registered clients into those still attached and
// those that vanished without a detach hook; the latter are released.
func (reporter *herdrReporter) liveClients(
	ctx context.Context,
	session string,
	state *herdrStored,
) (gone map[string]herdrClient, err error) {
	if len(state.Clients) == 0 {
		return nil, nil
	}
	output, err := reporter.deps.tmux(
		ctx, reporter.options.socket, "list-clients", "-t", session, "-F", "#{client_pid}",
	)
	if err != nil {
		return nil, fmt.Errorf("list tmux clients of %s: %w", session, err)
	}
	live := map[string]bool{}
	for _, pid := range strings.Fields(output) {
		live[pid] = true
	}
	gone = map[string]herdrClient{}
	for pid, client := range state.Clients {
		if !live[pid] {
			gone[pid] = client
			delete(state.Clients, pid)
		}
	}
	return gone, nil
}

// releaseAll releases the panes of clients that detached or vanished; each
// pane is resolved first, so a moved chat's current pane is the one released.
// A client whose pane is already gone is expected, not an error: nothing is
// sent for it and no error row is written.
func (reporter *herdrReporter) releaseAll(ctx context.Context, engine string, clients map[string]herdrClient) error {
	var errs []error
	for _, pid := range sortedClientPIDs(clients) {
		client, _, err := reporter.currentPane(ctx, pid, clients[pid])
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
// id answers pane_not_found (or, reused, belongs to another chat). With no recorded
// terminal (an attach whose pane.list failed) the recorded id binds one when it
// is still listed, else the pane whose foreground holds the client pid does.
// A client it cannot resolve is an error and nothing is sent to it: the stale
// id is never the fallback.
func (reporter *herdrReporter) currentPane(
	ctx context.Context,
	pid string,
	client herdrClient,
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
	if client.Terminal != "" {
		for _, pane := range panes {
			if pane.TerminalID == client.Terminal {
				resolved.Pane = pane.PaneID
				return resolved, resolved.Pane != client.Pane, nil
			}
		}
		return fail(paneGoneError{fmt.Sprintf("terminal %s is in no pane", client.Terminal)})
	}
	for _, pane := range panes {
		if pane.PaneID == client.Pane {
			resolved.Terminal = pane.TerminalID
			return resolved, false, nil
		}
	}
	pane, found, err := reporter.paneRunning(ctx, client.Socket, pid, panes, "")
	if err != nil {
		return fail(err)
	}
	if found {
		resolved.Pane, resolved.Terminal = pane.PaneID, pane.TerminalID
		return resolved, true, nil
	}
	return fail(paneGoneError{fmt.Sprintf("pane %s is gone and no pane runs the client pid", client.Pane)})
}

// paneRunning returns the listed pane whose foreground processes hold pid,
// asking about the pane first names before the others. A pane whose process
// info cannot be read is skipped.
func (reporter *herdrReporter) paneRunning(
	ctx context.Context,
	socket, pid string,
	panes []herdr.Pane,
	first string,
) (herdr.Pane, bool, error) {
	ordered := make([]herdr.Pane, 0, len(panes))
	for _, pane := range panes {
		if pane.PaneID == first {
			ordered = append([]herdr.Pane{pane}, ordered...)
		} else {
			ordered = append(ordered, pane)
		}
	}
	for _, pane := range ordered {
		line, err := herdr.ProcessInfo{Pane: pane.PaneID, Seq: reporter.nextSent()}.Line()
		if err != nil {
			return herdr.Pane{}, false, err
		}
		results, err := herdr.Call(ctx, socket, line)
		if err != nil {
			continue
		}
		pids, err := herdr.ParseProcessInfo(results[0])
		if err != nil {
			continue
		}
		for _, foreground := range pids {
			if strconv.Itoa(foreground) == pid {
				return pane, true, nil
			}
		}
	}
	return herdr.Pane{}, false, nil
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
		client, panesDiffer, err := reporter.currentPane(ctx, pid, state.Clients[pid])
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

// attach registers the tmux client that just attached, when it attached from a
// Herdr pane, and re-reports the stored state to that pane. tmux copied the
// Herdr variables of THIS client into the session environment (and removes
// them for a client without them); a client without them changes nothing. A
// client already registered is only resolved again, as a pane move happens
// without tmux copying anything: it re-binds and re-sends if its pane changed.
// The copied pane id is the one the pane's shell started with, stale after a
// pane move, and the environment is the LAST attaching client's: a client is
// registered only under the pane whose foreground runs its pid, asked first
// about the copied id; a client no Herdr pane runs is an error, never sent to.
func (reporter *herdrReporter) attach(ctx context.Context, session string, state herdrStored) error {
	if reporter.options.client == "" {
		return fmt.Errorf("attach needs --client {tmux client pid}")
	}
	gone, err := reporter.liveClients(ctx, session, &state)
	if err != nil {
		return err
	}
	errs := []error{reporter.releaseAll(ctx, reporter.engineLabel(state), gone)}
	if len(gone) > 0 {
		errs = append(errs, reporter.writeState(ctx, session, state))
	}
	if _, registered := state.Clients[reporter.options.client]; registered {
		return errors.Join(append(errs, reporter.rebind(ctx, session, state))...)
	}
	target, err := reporter.sessionTarget(ctx, session)
	if err != nil || !target.ok() {
		return errors.Join(append(errs, err)...)
	}
	panes, err := reporter.listPanes(ctx, target.socket)
	if err != nil {
		return errors.Join(append(errs, fmt.Errorf("attach client %s: %w", reporter.options.client, err))...)
	}
	pane, found, err := reporter.paneRunning(ctx, target.socket, reporter.options.client, panes, target.pane)
	if err == nil && !found {
		err = fmt.Errorf("no Herdr pane runs it (the session environment names %s)", target.pane)
	}
	if err != nil {
		return errors.Join(append(errs, fmt.Errorf(
			"attach client %s (nothing registered): %w", reporter.options.client, err,
		))...)
	}
	target.pane = pane.PaneID
	client := herdrClient{Pane: pane.PaneID, Terminal: pane.TerminalID, Socket: target.socket}
	if state.Clients == nil {
		state.Clients = map[string]herdrClient{}
	}
	state.Clients[reporter.options.client] = client
	if state.State == "" {
		reporter.idleAtLaunch(session, &state)
	}
	if err := reporter.writeState(ctx, session, state); err != nil {
		return errors.Join(append(errs, err)...)
	}
	if state.State != "" {
		errs = append(errs, reporter.send(ctx, target, &herdrAction{stored: state}, reporter.nextSent()))
	}
	errs = append(errs, reporter.nameSync(ctx, session, state, clientTargets(state.Clients), []herdrTarget{target}))
	return errors.Join(errs...)
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

// sync is a client's resize: a registered client is re-bound when its pane
// moved; a client never registered changes nothing, since the session
// environment holds the Herdr variables of the last client that attached, not
// of this one.
func (reporter *herdrReporter) sync(ctx context.Context, session string, state herdrStored) error {
	if reporter.options.client == "" {
		return fmt.Errorf("sync needs --client {tmux client pid}")
	}
	if _, registered := state.Clients[reporter.options.client]; !registered {
		return nil
	}
	return reporter.rebind(ctx, session, state)
}

// rebind resolves every registered client; a client whose pane moved gets the
// stored state, name and title at its new pane, and nothing else changes.
func (reporter *herdrReporter) rebind(ctx context.Context, session string, state herdrStored) error {
	clients, moved, changed, err := reporter.resolveClients(ctx, &state, false)
	errs := []error{err}
	if changed {
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

func (reporter *herdrReporter) sessionTarget(ctx context.Context, session string) (herdrTarget, error) {
	output, err := reporter.deps.tmux(ctx, reporter.options.socket, "show-environment", "-t", session)
	if err != nil {
		return herdrTarget{}, fmt.Errorf("read tmux session environment of %s (show-environment): %w", session, err)
	}
	values := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		if name, value, found := strings.Cut(line, "="); found {
			values[name] = value
		}
	}
	if values[herdr.EnvMarker] != "1" {
		return herdrTarget{}, nil
	}
	return herdrTarget{pane: values[herdr.EnvPane], socket: values[herdr.EnvSocket]}, nil
}

// sweep releases and forgets the registered clients tmux no longer lists.
func (reporter *herdrReporter) sweep(ctx context.Context, session string, state herdrStored) error {
	gone, err := reporter.liveClients(ctx, session, &state)
	if err != nil || len(gone) == 0 {
		return err
	}
	if err := reporter.writeState(ctx, session, state); err != nil {
		return err
	}
	return reporter.releaseAll(ctx, reporter.engineLabel(state), gone)
}
