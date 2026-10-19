package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

const priorityDone = "done"

// priorityWaiting is one proved fleet pane representing its whole tab. Herdr's
// effective done means completed and unseen; idle is not a waiting completion.
type priorityWaiting struct {
	Pane      string `json:"pane_id"`
	Tab       string `json:"tab_id"`
	Workspace string `json:"workspace_id"`
	State     string `json:"agent_status"`
	Seq       uint64 `json:"state_change_seq"`
}

type priorityTab struct {
	Tab       string `json:"tab_id"`
	Workspace string `json:"workspace_id"`
}

type priorityMemo struct {
	Server      int64                      `json:"server"`
	Waiting     map[string]priorityWaiting `json:"waiting"`
	Diagnostics map[string]bool            `json:"diagnostics,omitempty"`
	unavailable bool
}

// prioritize shares the indicator event/tick path and singleton, not a new
// monitor. Only entering/changing a waiting state triggers a reconciliation:
// working transitions and the user's manual reorder never trigger moves.
func (painter *indicatorPainter) prioritize(ctx context.Context) {
	if painter.priority == nil {
		return
	}
	enabled, err := painter.priority()
	if err != nil {
		painter.priorityDiagnostic(fmt.Errorf("read configuration: %w", err))
		return
	}
	if !enabled {
		painter.priorityState = nil
		return
	}
	agents, err := painter.priorityRead(ctx, "agent.list", struct{}{})
	if err != nil {
		painter.priorityDiagnostic(err)
		return
	}
	var listing struct {
		Type   string            `json:"type"`
		Agents []priorityWaiting `json:"agents"`
	}
	if err := json.Unmarshal(agents, &listing); err != nil {
		painter.priorityDiagnostic(fmt.Errorf("decode agent.list: %w", err))
		return
	}
	if listing.Type != "agent_list" || listing.Agents == nil {
		painter.priorityDiagnostic(fmt.Errorf("unexpected agent.list: %s", agents))
		return
	}
	memo := painter.loadPriority()
	if memo.unavailable {
		return
	}
	waiting := map[string]priorityWaiting{}
	for _, agent := range listing.Agents {
		if agent.Pane == "" {
			painter.priorityDiagnostic(fmt.Errorf("agent.list entry has no pane_id: %+v", agent))
			continue
		}
		// The indicator classifier distinguishes a positively foreign pane from an
		// unavailable owner. Neither can become a priority anchor or anchor error.
		_, err := painter.report(ctx, indicatorTarget{kind: targetPane, id: agent.Pane, status: agent.State}, nil)
		if err != nil && (!errors.Is(err, errIndicatorOwnership) || memo.Waiting[agent.Tab] != agent) {
			continue
		}
		// Unavailable ownership is not a departure from the waiting state. Keep
		// an identical observation already handled, without authorizing a move:
		// every mutation below still requires a fresh successful proof.
		if agent.State != string(StateBlocked) && agent.State != priorityDone {
			continue
		}
		if agent.Tab == "" || agent.Workspace == "" {
			painter.priorityDiagnostic(
				fmt.Errorf("fleet pane %s state %s has no tab/workspace anchor", agent.Pane, agent.State),
			)
			continue
		}
		previous, ok := waiting[agent.Tab]
		if !ok || priorityLess(agent, previous) {
			waiting[agent.Tab] = agent
		}
	}
	changed := false
	for tab, agent := range waiting {
		if memo.Waiting[tab] != agent {
			changed = true
		}
	}
	removed := len(memo.Waiting) != len(waiting)
	memo.Waiting = waiting
	if changed {
		memo.Diagnostics = nil
	}
	// Record before any socket mutation. A failed/ambiguous write must not be
	// blindly retried by the next tick or another hook process in the same state.
	if (changed || removed) && !painter.storePriority() {
		return
	}
	if !changed {
		return
	}
	body, err := painter.priorityRead(ctx, "tab.list", struct{}{})
	if err != nil {
		painter.priorityDiagnostic(err)
		return
	}
	tabs, err := decodePriorityTabs(body)
	if err != nil {
		painter.priorityDiagnostic(err)
		return
	}
	queue := make([]priorityWaiting, 0, len(waiting))
	for _, agent := range waiting {
		queue = append(queue, agent)
	}
	slices.SortFunc(queue, func(a, b priorityWaiting) int {
		if a.Workspace != b.Workspace {
			return comparePriorityString(a.Workspace, b.Workspace)
		}
		if priorityLess(a, b) {
			return -1
		}
		if priorityLess(b, a) {
			return 1
		}
		return comparePriorityString(a.Tab, b.Tab)
	})
	positions := map[string]int{}
	for _, agent := range queue {
		index := positions[agent.Workspace]
		positions[agent.Workspace]++
		order := tabs[agent.Workspace]
		current := slices.Index(order, agent.Tab)
		if current < 0 {
			positions[agent.Workspace]--
			painter.priorityDiagnostic(
				fmt.Errorf(
					"tab %s pane %s state %s seq %d missing from workspace %s tab.list",
					agent.Tab,
					agent.Pane,
					agent.State,
					agent.Seq,
					agent.Workspace,
				),
			)
			continue
		}
		if current == index {
			continue
		}
		// Fresh proof, never the classifier's successful no-op cache. Moving the
		// tab requires the very same durable-seat/bridge proof as indicators.
		if err := painter.provePane(ctx, painter.socket, agent.Pane); err != nil {
			positions[agent.Workspace]--
			if !errors.Is(err, ErrNotFleetPane) {
				painter.priorityDiagnostic(
					fmt.Errorf(
						"tab %s pane %s state %s seq %d ownership unproved before move: %w",
						agent.Tab,
						agent.Pane,
						agent.State,
						agent.Seq,
						err,
					),
				)
			}
			continue
		}
		latest, err := painter.priorityAgent(ctx, agent.Pane)
		if err != nil {
			positions[agent.Workspace]--
			painter.priorityDiagnostic(
				fmt.Errorf(
					"tab %s pane %s state %s seq %d before move: %w",
					agent.Tab,
					agent.Pane,
					agent.State,
					agent.Seq,
					err,
				),
			)
			continue
		}
		if latest != agent {
			positions[agent.Workspace]--
			continue
		}
		insert := index
		if current < index {
			insert++
		}
		moved, err := painter.movePriorityTab(ctx, agent, insert)
		if err != nil {
			painter.priorityDiagnostic(err)
			return
		}
		// Herdr's response is authoritative after each move, not our predicted
		// ordering: another client may have reordered or closed a tab meanwhile.
		for workspace, order := range moved {
			tabs[workspace] = order
		}
	}
}

func comparePriorityString(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func priorityLess(a, b priorityWaiting) bool {
	if a.State != b.State {
		return a.State == string(StateBlocked)
	}
	if a.Seq != b.Seq {
		return a.Seq < b.Seq
	}
	return a.Pane < b.Pane
}

func (painter *indicatorPainter) priorityRead(ctx context.Context, method string, params any) (json.RawMessage, error) {
	// Each call has its own connection and runs serially. A stable read ID
	// keeps requestName's error context stable across unchanged failed polls.
	line, err := json.Marshal(
		request{ID: "pfm-priority-" + method, Method: method, Params: params},
	)
	if err != nil {
		return nil, fmt.Errorf("render %s: %w", method, err)
	}
	results, err := painter.call(ctx, painter.socket, line)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	if len(results) != 1 {
		return nil, fmt.Errorf("%s answered %d results, want 1", method, len(results))
	}
	return results[0], nil
}

func (painter *indicatorPainter) priorityAgent(ctx context.Context, pane string) (priorityWaiting, error) {
	body, err := painter.priorityRead(ctx, "agent.get", map[string]string{"target": pane})
	if err != nil {
		return priorityWaiting{}, err
	}
	var reply struct {
		Type  string          `json:"type"`
		Agent priorityWaiting `json:"agent"`
	}
	if err := json.Unmarshal(body, &reply); err != nil {
		return priorityWaiting{}, fmt.Errorf("decode agent.get pane %s: %w", pane, err)
	}
	if reply.Type != "agent_info" || reply.Agent.Pane != pane {
		return priorityWaiting{}, fmt.Errorf("unexpected agent.get pane %s: %s", pane, body)
	}
	return reply.Agent, nil
}

func decodePriorityTabs(body json.RawMessage) (map[string][]string, error) {
	var listing struct {
		Type string        `json:"type"`
		Tabs []priorityTab `json:"tabs"`
	}
	if err := json.Unmarshal(body, &listing); err != nil {
		return nil, fmt.Errorf("decode tab.list: %w", err)
	}
	if listing.Type != "tab_list" || listing.Tabs == nil {
		return nil, fmt.Errorf("unexpected tab.list: %s", body)
	}
	tabs := map[string][]string{}
	seen := map[string]bool{}
	for _, tab := range listing.Tabs {
		if tab.Tab == "" || tab.Workspace == "" || seen[tab.Tab] {
			return nil, fmt.Errorf("invalid tab.list anchor %+v", tab)
		}
		seen[tab.Tab] = true
		tabs[tab.Workspace] = append(tabs[tab.Workspace], tab.Tab)
	}
	return tabs, nil
}

func (painter *indicatorPainter) movePriorityTab(
	ctx context.Context,
	agent priorityWaiting,
	index int,
) (map[string][]string, error) {
	// Authority: Herdr 0.9.5 `herdr api schema --json`, TabMoveParams;
	// https://github.com/herdrdev/herdr/blob/v0.9.5/src/api/schema/tabs.rs#L34-L37.
	// src/workspace.rs:591-613 moves the whole tab, retaining its active pane
	// and the user's active tab. insert_index addresses the pre-removal gap.
	params := struct {
		Tab   string `json:"tab_id"`
		Index int    `json:"insert_index"`
	}{agent.Tab, index}
	line, err := json.Marshal(
		request{ID: fmt.Sprintf("pfm-priority-%d", painter.nextSeq()), Method: "tab.move", Params: params},
	)
	if err != nil {
		return nil, fmt.Errorf("render tab.move tab %s: %w", agent.Tab, err)
	}
	results, err := painter.guardedCall(ctx, painter.socket, line)
	operation := fmt.Sprintf(
		"tab.move tab %s pane %s workspace %s state %s seq %d insert_index %d",
		agent.Tab,
		agent.Pane,
		agent.Workspace,
		agent.State,
		agent.Seq,
		index,
	)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	if len(results) != 1 {
		return nil, fmt.Errorf("%s: answered %d results, want 1", operation, len(results))
	}
	tabs, err := decodePriorityTabs(results[0])
	if err != nil {
		return nil, fmt.Errorf("%s: %w", operation, err)
	}
	// tab.move returns only the target workspace's tabs. Keep other workspace
	// orders by letting the caller merge this authoritative projection.
	return tabs, nil
}

func (painter *indicatorPainter) priorityPath() string {
	return filepath.Join(painter.lockDir, "indicators-"+socketKey(painter.socket)+".priority.json")
}

func (painter *indicatorPainter) loadPriority() *priorityMemo {
	if painter.priorityState != nil && painter.priorityState.Server == painter.server {
		return painter.priorityState
	}
	memo := &priorityMemo{Server: painter.server, Waiting: map[string]priorityWaiting{}}
	painter.priorityState = memo
	if !painter.persisted {
		return memo
	}
	file, err := OpenStateFile(
		painter.lockDir, filepath.Base(painter.priorityPath()), os.O_RDONLY, "priority transition memo",
	)
	if errors.Is(err, os.ErrNotExist) {
		return memo
	}
	var body []byte
	if err == nil {
		body, err = io.ReadAll(file)
		err = errors.Join(err, file.Close())
	}
	if err != nil {
		memo.unavailable = true
		painter.priorityDiagnostic(fmt.Errorf("read transition memo %s: %w", painter.priorityPath(), err))
		return memo
	}
	var stored priorityMemo
	if err := json.Unmarshal(body, &stored); err != nil {
		memo.unavailable = true
		painter.priorityDiagnostic(fmt.Errorf("decode transition memo %s: %w", painter.priorityPath(), err))
		return memo
	}
	if stored.Waiting == nil {
		memo.unavailable = true
		painter.priorityDiagnostic(fmt.Errorf("decode transition memo %s: missing waiting set", painter.priorityPath()))
		return memo
	}
	if stored.Server == painter.server && stored.Waiting != nil {
		painter.priorityState = &stored
	}
	return painter.priorityState
}

func (painter *indicatorPainter) storePriority() bool {
	if painter.priorityState != nil && painter.priorityState.unavailable {
		return false
	}
	if !painter.persisted || painter.priorityState == nil {
		return true
	}
	body, err := json.Marshal(painter.priorityState)
	if err == nil {
		err = os.MkdirAll(painter.lockDir, 0o700)
	}
	if err == nil {
		err = atomicfile.Write(painter.priorityPath(), body, 0o600)
	}
	if err != nil {
		painter.priorityState.unavailable = true
		painter.logf(
			"herdr tab priority: socket %s: store transition memo %s: %v "+
				"(priority reconciliation stopped; indicators continue)",
			painter.socket,
			painter.priorityPath(),
			err,
		)
		return false
	}
	return true
}

func (painter *indicatorPainter) priorityDiagnostic(err error) {
	memo := painter.loadPriority()
	message := err.Error()
	if memo.Diagnostics[message] {
		return
	}
	if memo.Diagnostics == nil {
		memo.Diagnostics = map[string]bool{}
	}
	memo.Diagnostics[message] = true
	painter.logf(
		"herdr tab priority: socket %s: %v (priority reconciliation stopped; indicators continue)",
		painter.socket,
		err,
	)
	painter.storePriority()
}
