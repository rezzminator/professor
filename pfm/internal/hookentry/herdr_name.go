package hookentry

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// title carries a chat title changed inside OpenCode to the tmux window and the
// chat's name option, the pair every other name writer sets; tmux's
// window-renamed hook then runs the name sync.
func (reporter *herdrReporter) title(ctx context.Context, session string, payload herdrPayload) error {
	title := strings.TrimSpace(payload.Title)
	if title == "" {
		return errors.New("title event with an empty title (skipped)")
	}
	pane := reporter.deps.getenv("TMUX_PANE")
	if pane == "" {
		return errors.New("title event with no $TMUX_PANE to rename the window of (skipped)")
	}
	// The name first and in the same command, its final `;` escaped: the
	// spelling every name writer shares (gather.ChatNameCommand).
	arguments := append(
		gather.ChatNameCommand(session, title),
		";", "rename-window", "-t", pane, spawn.WindowName(title),
	)
	if _, err := reporter.deps.tmux(ctx, reporter.options.socket, arguments...); err != nil {
		return fmt.Errorf("rename window of %s to chat title %q: %w", pane, title, err)
	}
	return nil
}

// name re-sends the chat's name to every live registered pane: the tmux
// window-renamed hook runs it, and every name writer ends in a window rename.
func (reporter *herdrReporter) name(ctx context.Context, session string, state herdrStored) error {
	gone, err := reporter.liveClients(ctx, session, &state)
	if err != nil {
		return err
	}
	clients, moved, changed, resolveErr := reporter.resolveClients(ctx, &state, false)
	errs := []error{resolveErr, reporter.releaseAll(ctx, reporter.engineLabel(state), gone)}
	if changed || len(gone) > 0 {
		if err := reporter.writeState(ctx, session, state); err != nil {
			return errors.Join(append(errs, err)...)
		}
	}
	for _, pid := range sortedClientPIDs(movedClients(clients, moved)) {
		if state.State != "" {
			errs = append(errs, reporter.send(
				ctx,
				herdrTarget{pane: clients[pid].Pane, socket: clients[pid].Socket},
				&herdrAction{stored: state},
				reporter.nextSent(),
			))
		}
	}
	fresh := clientTargets(movedClients(clients, moved))
	errs = append(errs, reporter.nameSync(ctx, session, state, clientTargets(clients), fresh))
	return errors.Join(errs...)
}

// nameSync is the one name sync: read the chat's name from the session
// (unset is an unnamed chat: no rename, no title), name each pane's agent
// after it, its title the exact name, and store it in @pfm_herdr once every
// pane carries it. A name the panes already carry names only the fresh
// targets (a pane just attached or moved), so a window rename that changed
// nothing sends Herdr nothing; a failed naming leaves the stored name as it
// was, and the next rename tries again.
func (reporter *herdrReporter) nameSync(
	ctx context.Context,
	session string,
	state herdrStored,
	targets, fresh []herdrTarget,
) error {
	output, err := reporter.deps.tmux(
		ctx,
		reporter.options.socket,
		"show-options",
		"-qv",
		"-t",
		session,
		herdr.ChatNameOption,
	)
	if err != nil {
		return fmt.Errorf("read %s of %s: %w", herdr.ChatNameOption, session, err)
	}
	name := strings.TrimSpace(output)
	if name == "" {
		return nil
	}
	if state.Name == name {
		targets = fresh
	}
	var errs []error
	if state.State != "" {
		// Otherwise no agent was reported to the panes yet (or it was
		// released): there is nothing in them to name.
		for _, target := range targets {
			errs = append(errs, reporter.namePane(ctx, target, name))
		}
	}
	if err := errors.Join(errs...); err != nil || state.Name == name {
		return err
	}
	state.Name = name
	return reporter.writeState(ctx, session, state)
}

// namePane names the agent in one pane after the chat. A name another agent
// holds, or one Herdr refuses, moves on to the next free suffix, up to -9; the
// pane's title is the exact chat name.
func (reporter *herdrReporter) namePane(ctx context.Context, target herdrTarget, name string) error {
	if !target.ok() {
		return nil
	}
	line, err := herdr.AgentList{Seq: reporter.nextSent()}.Line()
	if err != nil {
		return err
	}
	results, err := herdr.Call(ctx, target.socket, line)
	if err != nil {
		return fmt.Errorf("name pane %s: list agents: %w", target.pane, err)
	}
	agents, err := herdr.ParseAgentList(results[0])
	if err != nil {
		return fmt.Errorf("name pane %s: %w", target.pane, err)
	}
	taken := map[string]bool{}
	for _, agent := range agents {
		if agent.PaneID != target.pane && agent.Name != "" {
			taken[agent.Name] = true
		}
	}
	var refusal *herdr.Refusal
	for attempt := 0; attempt < herdrNameAttempts; attempt++ {
		candidate := herdr.AgentName(name, taken)
		rename, err := herdr.AgentRename{Target: target.pane, Name: candidate, Seq: reporter.nextSent()}.Line()
		if err != nil {
			return err
		}
		err = herdr.Send(ctx, target.socket, rename)
		if err == nil {
			break
		}
		if !errors.As(err, &refusal) {
			return fmt.Errorf("name pane %s %q: %w", target.pane, candidate, err)
		}
		taken[candidate] = true
		if attempt == herdrNameAttempts-1 {
			return fmt.Errorf("name pane %s %q: refused %s", target.pane, herdr.AgentName(name, nil), refusal.Code)
		}
	}
	title, err := herdr.Metadata{Pane: target.pane, Title: name, Seq: reporter.nextSent()}.Line()
	if err != nil {
		return err
	}
	if err := herdr.Send(ctx, target.socket, title); err != nil {
		return fmt.Errorf("title pane %s %q: %w", target.pane, name, err)
	}
	return nil
}
