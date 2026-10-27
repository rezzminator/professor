package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// A home follows the tab (not the pane that happened to request attention).
// Placed tracks only pfm's own index shifts, so a user's reorder cancels it.
type priorityHome struct {
	Agent  priorityWaiting `json:"agent"`
	Left   string          `json:"left,omitempty"`
	Index  int             `json:"index"`
	Placed int             `json:"placed"`
}

func validatePriorityHomes(homes map[string]priorityHome) error {
	for tab, home := range homes {
		if tab == "" || home.Agent.Tab != tab || home.Agent.Workspace == "" || home.Agent.Pane == "" ||
			home.Index < 0 || home.Placed < 0 || home.Left == tab {
			return fmt.Errorf("invalid tab home %q: %+v", tab, home)
		}
	}
	return nil
}

func (painter *indicatorPainter) readPriorityLayout(ctx context.Context) (map[string][]string, map[string]bool, error) {
	body, err := painter.priorityRead(ctx, "tab.list", struct{}{})
	if err != nil {
		return nil, nil, err
	}
	tabs, err := decodePriorityTabs(body)
	if err != nil {
		return nil, nil, err
	}
	var listing struct {
		Tabs []priorityTab `json:"tabs"`
	}
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, nil, fmt.Errorf("decode tab.list focus: %w", err)
	}
	// Herdr 0.9.5 `herdr api schema --json`: TabInfo.focused is required.
	// Plugin pane.focused/tab.focused/workspace.focused already wake this painter;
	// their socket payloads are pane_focused{pane_id,workspace_id},
	// tab_focused{tab_id,workspace_id}, workspace_focused{workspace_id}.
	// Read the authoritative tab snapshot, including focus on non-agent panes.
	focused := map[string]bool{}
	for _, tab := range listing.Tabs {
		if tab.Focused == nil {
			return nil, nil, fmt.Errorf("tab.list tab %s missing focused", tab.Tab)
		}
		focused[tab.Tab] = *tab.Focused
	}
	return tabs, focused, nil
}

func (painter *indicatorPainter) settlePriority(agent priorityWaiting) {
	memo := painter.priorityState
	if memo.Settled == nil {
		memo.Settled = map[string]priorityWaiting{}
	}
	if current, ok := memo.Waiting[agent.Tab]; ok {
		memo.Settled[agent.Tab] = current
	}
	delete(memo.Homes, agent.Tab)
}

func (painter *indicatorPainter) cancelPriorityHomes(tabs map[string][]string) {
	for tab, home := range painter.priorityState.Homes {
		if slices.Index(tabs[home.Agent.Workspace], tab) != home.Placed {
			painter.settlePriority(home.Agent)
		}
	}
}

func (painter *indicatorPainter) returnPriorityTabs(
	ctx context.Context,
	tabs map[string][]string,
	focused map[string]bool,
) bool {
	painter.cancelPriorityHomes(tabs)
	// Sorted IDs make simultaneous workspace-local focus observations stable.
	ids := make([]string, 0, len(painter.priorityState.Homes))
	for tab := range painter.priorityState.Homes {
		ids = append(ids, tab)
	}
	slices.Sort(ids)
	for _, tab := range ids {
		home, ok := painter.priorityState.Homes[tab]
		if !ok || !focused[tab] {
			continue
		}
		if err := painter.provePane(ctx, painter.socket, home.Agent.Pane); err != nil {
			if errors.Is(err, ErrNotFleetPane) {
				painter.settlePriority(home.Agent)
				continue
			}
			painter.priorityDiagnostic(fmt.Errorf("return tab %s home ownership: %w", tab, err))
			return false
		}
		fresh, _, err := painter.readPriorityLayout(ctx)
		if err != nil {
			painter.priorityDiagnostic(fmt.Errorf("return tab %s layout: %w", tab, err))
			return false
		}
		clear(tabs)
		for workspace, order := range fresh {
			tabs[workspace] = order
		}
		painter.cancelPriorityHomes(tabs)
		if _, ok := painter.priorityState.Homes[tab]; !ok {
			continue
		}
		order := tabs[home.Agent.Workspace]
		current := slices.Index(order, tab)
		remaining := slices.Delete(slices.Clone(order), current, current+1)
		destination := min(home.Index, len(remaining))
		if neighbour := slices.Index(remaining, home.Left); neighbour >= 0 {
			destination = neighbour + 1
		}
		// Clear before writing, including ambiguous failures: a later hook must
		// never replay a possibly completed return. No focus command is issued.
		painter.settlePriority(home.Agent)
		if !painter.placePriorityTab(ctx, home.Agent, tabs, current, destination) {
			return false
		}
		painter.cancelPriorityHomes(tabs)
	}
	return painter.storePriority()
}

func (painter *indicatorPainter) placePriorityTab(
	ctx context.Context,
	agent priorityWaiting,
	tabs map[string][]string,
	current, destination int,
) bool {
	// Predict only our own index shifts. Never adopt a concurrent user's drag
	// from the move response as a new pfm placement.
	for tab, home := range painter.priorityState.Homes {
		if home.Agent.Workspace != agent.Workspace {
			continue
		}
		if tab == agent.Tab {
			home.Placed = destination
		} else {
			if home.Placed > current {
				home.Placed--
			}
			if home.Placed >= destination {
				home.Placed++
			}
		}
		painter.priorityState.Homes[tab] = home
	}
	if !painter.storePriority() {
		return false
	}
	if current == destination {
		return true
	}
	insert := destination
	if current < destination {
		insert++
	}
	moved, err := painter.movePriorityTab(ctx, agent, insert)
	if err != nil {
		delete(painter.priorityState.Homes, agent.Tab)
		painter.priorityDiagnostic(err)
		return false
	}
	for workspace, order := range moved {
		tabs[workspace] = order
	}
	return true
}
