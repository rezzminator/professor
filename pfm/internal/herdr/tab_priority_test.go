package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

type priorityAgent struct {
	Pane      string `json:"pane_id"`
	Tab       string `json:"tab_id"`
	Workspace string `json:"workspace_id"`
	State     string `json:"agent_status"`
	Seq       uint64 `json:"state_change_seq"`
}

type priorityFixture struct {
	agents                  []priorityAgent
	order                   []string
	moves                   []string
	lines                   []string
	focusedTab, focusedPane string
	proof                   map[string]error
	failure                 error
	afterProof              func(string)
	afterMove               func(string)
}

func (f *priorityFixture) call(_ context.Context, _ string, lines ...[]byte) ([]json.RawMessage, error) {
	var results []json.RawMessage
	for _, line := range lines {
		var req struct {
			Method string                     `json:"method"`
			Params map[string]json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(line, &req); err != nil {
			return nil, err
		}
		var body any
		switch req.Method {
		case "pane.list":
			panes := []map[string]any{}
			for _, a := range f.agents {
				panes = append(
					panes,
					map[string]any{
						"pane_id":      a.Pane,
						"terminal_id":  "term-" + a.Pane,
						"tab_id":       a.Tab,
						"workspace_id": a.Workspace,
						"agent_status": a.State,
						"focused":      a.Pane == f.focusedPane,
						"revision":     1,
					},
				)
			}
			body = map[string]any{"type": "pane_list", "panes": panes}
		case "workspace.list":
			body = map[string]any{
				"type":       "workspace_list",
				"workspaces": []map[string]any{{"workspace_id": "w1", "agent_status": "idle"}},
			}
		case "agent.list":
			body = map[string]any{"type": "agent_list", "agents": f.agents}
		case "agent.get":
			var pane string
			if err := json.Unmarshal(req.Params["target"], &pane); err != nil {
				return nil, err
			}
			for _, agent := range f.agents {
				if agent.Pane == pane {
					body = map[string]any{"type": "agent_info", "agent": agent}
					break
				}
			}
			if body == nil {
				return nil, fmt.Errorf("missing agent %s", pane)
			}
		case "tab.list":
			body = f.tabs()
		case "tab.move":
			f.lines = append(f.lines, string(line))
			var tab string
			var index int
			if err := json.Unmarshal(req.Params["tab_id"], &tab); err != nil {
				return nil, err
			}
			if err := json.Unmarshal(req.Params["insert_index"], &index); err != nil {
				return nil, err
			}
			f.moves = append(f.moves, tab)
			if len(req.Params) != 2 {
				return nil, fmt.Errorf("tab.move params %s", line)
			}
			if f.failure != nil {
				return nil, f.failure
			}
			// Herdr v0.9.5 src/workspace.rs:591-613: insertion refers to the
			// pre-removal gap; the tab object (layout/focus included) moves intact.
			source := slices.Index(f.order, tab)
			if source < 0 || index < 0 || index > len(f.order) {
				return nil, fmt.Errorf("bad tab.move %s", line)
			}
			if source < index {
				index--
			}
			f.order = slices.Delete(f.order, source, source+1)
			f.order = slices.Insert(f.order, index, tab)
			if f.afterMove != nil {
				f.afterMove(tab)
			}
			body = f.tabs()
		case "pane.report_metadata", "workspace.report_metadata":
			body = map[string]any{"type": "ok"}
		default:
			return nil, fmt.Errorf("unexpected priority method %s", req.Method)
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		results = append(results, raw)
	}
	return results, nil
}

func (f *priorityFixture) tabs() any {
	tabs := []map[string]any{}
	for i, id := range f.order {
		count := 0
		for _, a := range f.agents {
			if a.Tab == id {
				count++
			}
		}
		tabs = append(
			tabs,
			map[string]any{
				"tab_id":       id,
				"workspace_id": "w1",
				"number":       i + 1,
				"pane_count":   count,
				"focused":      id == f.focusedTab,
				"agent_status": "idle",
				"label":        id,
			},
		)
	}
	return map[string]any{"type": "tab_list", "tabs": tabs}
}

func priorityChat(pane, tab, state string, seq uint64) priorityAgent {
	return priorityAgent{Pane: pane, Tab: tab, Workspace: "w1", State: state, Seq: seq}
}

func newPriorityPainter(t *testing.T, f *priorityFixture) *indicatorPainter {
	t.Helper()
	p, _, _ := newTestPainter(t, &fakeIndicatorHerdr{}, phaseOn)
	p.call = f.call
	p.priority = func() (bool, error) { return true, nil }
	p.provePane = func(_ context.Context, _, pane string) error {
		if f.afterProof != nil {
			f.afterProof(pane)
		}
		return f.proof[pane]
	}
	p.serverStart = func() (int64, error) { return 123, nil }
	return p
}

func priorityPaint(t *testing.T, p *indicatorPainter) {
	t.Helper()
	if _, _, err := p.paint(context.Background()); err != nil {
		t.Fatalf("priority must not stop indicator bridge: %v", err)
	}
}

func TestTabPriorityOrderAndTransitions(t *testing.T) {
	f := &priorityFixture{
		order: []string{"work", "new-done", "old-done", "new-blocked", "old-blocked"},
		agents: []priorityAgent{
			priorityChat(
				"p0",
				"work",
				"working",
				1,
			),
			priorityChat("p1", "new-done", "done", 9),
			priorityChat("p2", "old-done", "done", 2),
			priorityChat("p3", "new-blocked", "blocked", 8),
			priorityChat("p4", "old-blocked", "blocked", 3),
		},
	}
	p := newPriorityPainter(t, f)
	priorityPaint(t, p)
	want := []string{"old-blocked", "new-blocked", "old-done", "new-done", "work"}
	if !reflect.DeepEqual(f.order, want) {
		t.Fatalf("priority order = %v, want %v (moves %v)", f.order, want, f.moves)
	}
	n := len(f.moves)
	priorityPaint(t, p)
	if len(f.moves) != n {
		t.Fatalf("steady state repeated moves: %v", f.moves)
	}
	f.order = append(f.order, "later")
	f.agents = append(f.agents, priorityChat("p5", "later", "blocked", 20))
	priorityPaint(t, p)
	want = []string{"old-blocked", "new-blocked", "later", "old-done", "new-done", "work"}
	if !reflect.DeepEqual(f.order, want) {
		t.Fatalf("new blocked order = %v, want %v", f.order, want)
	}
	if slices.Contains(f.moves, "work") {
		t.Fatalf("moved working chat: %v", f.moves)
	}
}

func TestTabPrioritySafety(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		proof       error
		off         bool
	}{
		{name: "plain shell", state: "blocked", proof: ErrNotFleetPane},
		{name: "pfm menu", state: "done", proof: ErrNotFleetPane},
		{name: "nonfleet", state: "blocked", proof: ErrNotFleetPane},
		{name: "ownership unavailable", state: "blocked", proof: errors.New("owner read failed")},
		{name: "working", state: "working"},
		{name: "idle", state: "idle"},
		{name: "unknown", state: "unknown"},
		{name: "off", state: "blocked", off: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &priorityFixture{
				order:  []string{"shell", "chat"},
				agents: []priorityAgent{priorityChat("p1", "chat", tc.state, 7)},
				proof:  map[string]error{"p1": tc.proof},
			}
			p := newPriorityPainter(t, f)
			p.priority = func() (bool, error) { return !tc.off, nil }
			priorityPaint(t, p)
			if len(f.moves) != 0 {
				t.Fatalf("unsafe tab moves: %v", f.moves)
			}
			if errors.Is(tc.proof, ErrNotFleetPane) && strings.Contains(fmt.Sprint(p.stderr), "anchor") {
				t.Fatalf("foreign tab counted as anchor error: %v", p.stderr)
			}
		})
	}
}

func TestTabPriorityWorkingTransitionAndAlreadyPlaced(t *testing.T) {
	f := &priorityFixture{
		order:  []string{"wait", "work"},
		agents: []priorityAgent{priorityChat("p1", "wait", "blocked", 1), priorityChat("p2", "work", "idle", 2)},
	}
	p := newPriorityPainter(t, f)
	priorityPaint(t, p)
	if len(f.moves) != 0 {
		t.Fatalf("already placed moved: %v", f.moves)
	}
	f.agents[1].State = "working"
	f.agents[1].Seq = 3
	priorityPaint(t, p)
	f.agents[0].State = "working"
	f.agents[0].Seq = 4
	priorityPaint(t, p)
	if len(f.moves) != 0 {
		t.Fatalf("working transition moved tabs: %v", f.moves)
	}
}

func TestTabPriorityWholeTabAndFocus(t *testing.T) {
	f := &priorityFixture{
		order:       []string{"user", "split"},
		focusedTab:  "user",
		focusedPane: "user-pane",
		agents: []priorityAgent{
			priorityChat(
				"user-pane",
				"user",
				"idle",
				1,
			),
			priorityChat("chat-pane", "split", "blocked", 2),
			priorityChat("shell-pane", "split", "unknown", 3),
		},
		proof: map[string]error{"user-pane": ErrNotFleetPane, "shell-pane": ErrNotFleetPane},
	}
	p := newPriorityPainter(t, f)
	priorityPaint(t, p)
	if !reflect.DeepEqual(f.moves, []string{"split"}) {
		t.Fatalf("whole split tab moves = %v, want [split]", f.moves)
	}
	if f.focusedTab != "user" || f.focusedPane != "user-pane" {
		t.Fatalf("user focus changed: %+v", f)
	}
	if f.agents[1].Tab != "split" || f.agents[2].Tab != "split" {
		t.Fatalf("split tab lost panes: %+v", f.agents)
	}
	// Verified with `herdr api schema --json`, request/$defs/TabMoveParams,
	// and https://github.com/herdrdev/herdr/blob/v0.9.5/src/api/schema/tabs.rs#L34-L37.
	// No focus flag or pane.move: src/workspace.rs#L607-L612 preserves active tab.
	var req struct {
		Method string         `json:"method"`
		Params map[string]any `json:"params"`
	}
	if err := json.Unmarshal([]byte(f.lines[0]), &req); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"tab_id": "split", "insert_index": float64(0)}
	if req.Method != "tab.move" || !reflect.DeepEqual(req.Params, want) {
		t.Fatalf("tab.move contract = %s", f.lines[0])
	}
}

func TestTabPriorityFailureOncePerChangeAndAcrossPainters(t *testing.T) {
	for _, failure := range []error{&Refusal{Code: "method_not_found", Message: "tab.move unsupported"}, errors.New("socket write failed")} {
		t.Run(failure.Error(), func(t *testing.T) {
			f := &priorityFixture{
				order:   []string{"user", "chat"},
				agents:  []priorityAgent{priorityChat("p1", "chat", "blocked", 1)},
				failure: failure,
			}
			p := newPriorityPainter(t, f)
			priorityPaint(t, p)
			priorityPaint(t, p)
			log := fmt.Sprint(p.stderr)
			if len(f.moves) != 1 || strings.Count(log, "herdr tab priority:") != 1 || !strings.Contains(log, "chat") ||
				!strings.Contains(log, p.socket) ||
				!strings.Contains(log, failure.Error()) {
				t.Fatalf("moves=%v diagnostic=%s", f.moves, log)
			}
			q := newPriorityPainter(t, f)
			q.lockDir = p.lockDir
			priorityPaint(t, q)
			if len(f.moves) != 1 {
				t.Fatalf("new hook retried unchanged failure: %v", f.moves)
			}
			f.agents[0].State = "done"
			f.agents[0].Seq = 2
			priorityPaint(t, q)
			if len(f.moves) != 2 {
				t.Fatalf("new state did not retry: %v", f.moves)
			}
		})
	}
}

func TestTabPriorityRejectsChangedStateBeforeMove(t *testing.T) {
	f := &priorityFixture{
		order:  []string{"user", "chat"},
		agents: []priorityAgent{priorityChat("p1", "chat", "blocked", 1)},
	}
	p := newPriorityPainter(t, f)
	proofs := 0
	f.afterProof = func(_ string) {
		proofs++
		if proofs > 1 {
			f.agents[0].State = "working"
			f.agents[0].Seq = 2
		}
	}
	priorityPaint(t, p)
	if proofs < 2 || len(f.moves) != 0 {
		t.Fatalf("state recheck proof calls=%d moves=%v", proofs, f.moves)
	}
}

func TestTabPriorityChangedPaneDoesNotStrandOtherWaitingTabs(t *testing.T) {
	f := &priorityFixture{
		order: []string{"user", "changing", "waiting"},
		agents: []priorityAgent{
			priorityChat("p1", "changing", "blocked", 1),
			priorityChat("p2", "waiting", "blocked", 2),
		},
	}
	p := newPriorityPainter(t, f)
	proofs := 0
	f.afterProof = func(pane string) {
		if pane == "p1" {
			proofs++
			if proofs > 1 {
				f.agents[0].State = "working"
				f.agents[0].Seq = 3
			}
		}
	}
	priorityPaint(t, p)
	priorityPaint(t, p)
	if !slices.Equal(f.moves, []string{"waiting"}) || f.order[0] != "waiting" {
		t.Fatalf("changed pane stranded waiting tab: moves=%v order=%v", f.moves, f.order)
	}
}

func TestTabPriorityForeignPaneNeedsNoTabAnchor(t *testing.T) {
	f := &priorityFixture{
		order:  []string{"user", "chat"},
		agents: []priorityAgent{priorityChat("p1", "", "blocked", 1)},
		proof:  map[string]error{"p1": ErrNotFleetPane},
	}
	p := newPriorityPainter(t, f)
	priorityPaint(t, p)
	if len(f.moves) != 0 || strings.Contains(fmt.Sprint(p.stderr), "anchor") {
		t.Fatalf("foreign pane became a priority anchor: moves=%v diagnostic=%v", f.moves, p.stderr)
	}
}

func TestTabPriorityRespectsManualReorderUntilNextWaitingChange(t *testing.T) {
	f := &priorityFixture{
		order:  []string{"user", "chat"},
		agents: []priorityAgent{priorityChat("p1", "chat", "blocked", 1)},
	}
	p := newPriorityPainter(t, f)
	priorityPaint(t, p)
	f.order = []string{"user", "chat"}
	priorityPaint(t, p)
	if !reflect.DeepEqual(f.order, []string{"user", "chat"}) || len(f.moves) != 1 {
		t.Fatalf("unchanged waiting state overrode manual ordering: order=%v moves=%v", f.order, f.moves)
	}
}

func TestDecodePriorityTabs(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		invalid    bool
	}{
		{"workspaces", `{"type":"tab_list","tabs":[{"tab_id":"b","workspace_id":"w2"},{"tab_id":"a","workspace_id":"w1"},{"tab_id":"c","workspace_id":"w2"}]}`, false},
		{"empty", `{"type":"tab_list","tabs":[]}`, false},
		{"missing", `{"type":"tab_list"}`, true},
		{"wrong type", `{"type":"ok","tabs":[]}`, true},
		{"duplicate", `{"type":"tab_list","tabs":[{"tab_id":"a","workspace_id":"w1"},{"tab_id":"a","workspace_id":"w1"}]}`, true},
		{"no workspace", `{"type":"tab_list","tabs":[{"tab_id":"a"}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := decodePriorityTabs(json.RawMessage(tc.body))
			if (err != nil) != tc.invalid {
				t.Fatalf("tabs=%v error=%v", got, err)
			}
			if tc.name == "workspaces" &&
				(!slices.Equal(got["w2"], []string{"b", "c"}) || !slices.Equal(got["w1"], []string{"a"})) {
				t.Fatalf("workspace-local order lost: %v", got)
			}
		})
	}
}

func TestTabPriorityReadFailuresHaveStableDiagnostics(t *testing.T) {
	f := &priorityFixture{order: []string{"user"}, agents: []priorityAgent{}}
	p := newPriorityPainter(t, f)
	p.call = func(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error) {
		for _, line := range lines {
			var req request
			if err := json.Unmarshal(line, &req); err != nil {
				return nil, err
			}
			if req.Method == "agent.list" {
				return nil, &Refusal{Request: requestName(line), Code: "method_not_found", Message: "unsupported"}
			}
		}
		return f.call(ctx, socket, lines...)
	}
	priorityPaint(t, p)
	priorityPaint(t, p)
	if got := strings.Count(fmt.Sprint(p.stderr), "herdr tab priority:"); got != 1 {
		t.Fatalf("unchanged read failure logged %d times: %v", got, p.stderr)
	}
}

func TestTabPriorityOwnershipFailurePreservesTransitionMemo(t *testing.T) {
	f := &priorityFixture{
		order:  []string{"user", "chat"},
		agents: []priorityAgent{priorityChat("p1", "chat", "blocked", 1)},
		proof:  map[string]error{},
	}
	p := newPriorityPainter(t, f)
	priorityPaint(t, p)
	f.order = []string{"user", "chat"}
	f.proof["p1"] = errors.New("transient ownership read failure")
	priorityPaint(t, p)
	delete(f.proof, "p1")
	p.clock.(*steppingClock).now = p.clock.Now().Add(unprovedReprove)
	priorityPaint(t, p)
	if len(f.moves) != 1 || !slices.Equal(f.order, []string{"user", "chat"}) {
		t.Fatalf("ownership recovery repeated unchanged move: moves=%v order=%v", f.moves, f.order)
	}
}

func TestTabPriorityMemoFailuresStopMoves(t *testing.T) {
	for _, kind := range []string{"unreadable", "corrupt", "unwritable"} {
		t.Run(kind, func(t *testing.T) {
			f := &priorityFixture{
				order:  []string{"user", "chat"},
				agents: []priorityAgent{priorityChat("p1", "chat", "blocked", 1)},
			}
			p := newPriorityPainter(t, f)
			if err := os.MkdirAll(p.lockDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if kind == "corrupt" {
				if err := os.WriteFile(p.priorityPath(), []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Mkdir(p.priorityPath(), 0o700); err != nil {
				t.Fatal(err)
			}
			if kind == "unwritable" {
				p.priorityState = &priorityMemo{Server: 123, Waiting: map[string]priorityWaiting{}}
			}
			priorityPaint(t, p)
			priorityPaint(t, p)
			if len(f.moves) != 0 || !strings.Contains(fmt.Sprint(p.stderr), "transition memo") {
				t.Fatalf("memo %s manufactured an unhandled state: moves=%v diagnostic=%v", kind, f.moves, p.stderr)
			}
		})
	}
}

func TestTabPriorityForwardGapAfterConcurrentReorder(t *testing.T) {
	f := &priorityFixture{
		order: []string{"user", "older", "newer"},
		agents: []priorityAgent{
			priorityChat("p1", "older", "blocked", 1),
			priorityChat("p2", "newer", "blocked", 2),
		},
	}
	f.afterMove = func(tab string) {
		if tab == "older" {
			f.order = []string{"newer", "older", "user"}
		}
	}
	priorityPaint(t, newPriorityPainter(t, f))
	if !slices.Equal(f.order, []string{"older", "newer", "user"}) {
		t.Fatalf("forward pre-removal gap used wrong index: order=%v moves=%v", f.order, f.moves)
	}
}

func TestTabPriorityWorkspaceLocalMoves(t *testing.T) {
	first := &priorityFixture{
		order: []string{"user1", "done1", "blocked1"},
		agents: []priorityAgent{
			priorityChat("p1", "done1", "done", 1),
			priorityChat("p2", "blocked1", "blocked", 2),
			{Pane: "p3", Tab: "done2", Workspace: "w2", State: "done", Seq: 3},
			{Pane: "p4", Tab: "blocked2", Workspace: "w2", State: "blocked", Seq: 4},
		},
	}
	second := &priorityFixture{order: []string{"user2", "done2", "blocked2"}}
	projection := func(f *priorityFixture, workspace string) []map[string]any {
		tabs := f.tabs().(map[string]any)["tabs"].([]map[string]any)
		for _, tab := range tabs {
			tab["workspace_id"] = workspace
		}
		return tabs
	}
	p := newPriorityPainter(t, first)
	p.call = func(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error) {
		var req struct {
			Method string `json:"method"`
			Params struct {
				Tab string `json:"tab_id"`
			} `json:"params"`
		}
		if err := json.Unmarshal(lines[0], &req); err != nil {
			return nil, err
		}
		var tabs []map[string]any
		switch req.Method {
		case "tab.list":
			tabs = append(projection(first, "w1"), projection(second, "w2")...)
		case "tab.move":
			fixture, workspace := first, "w1"
			if slices.Contains(second.order, req.Params.Tab) {
				fixture, workspace = second, "w2"
			}
			if _, err := fixture.call(ctx, socket, lines...); err != nil {
				return nil, err
			}
			// The real server returns only the moved tab's workspace.
			tabs = projection(fixture, workspace)
		default:
			return first.call(ctx, socket, lines...)
		}
		body, err := json.Marshal(map[string]any{"type": "tab_list", "tabs": tabs})
		return []json.RawMessage{body}, err
	}
	priorityPaint(t, p)
	priorityPaint(t, p)
	if !slices.Equal(first.order, []string{"blocked1", "done1", "user1"}) ||
		!slices.Equal(second.order, []string{"blocked2", "done2", "user2"}) ||
		len(first.moves) != 2 || len(second.moves) != 2 {
		t.Fatalf("workspace order/moves: w1=%v/%v w2=%v/%v", first.order, first.moves, second.order, second.moves)
	}
}

func TestTabPriorityReprovesBeforeMove(t *testing.T) {
	f := &priorityFixture{
		order:  []string{"user", "chat"},
		agents: []priorityAgent{priorityChat("p1", "chat", "blocked", 1)},
		proof:  map[string]error{},
	}
	p := newPriorityPainter(t, f)
	proofs := 0
	f.afterProof = func(pane string) {
		proofs++
		if proofs > 1 {
			f.proof[pane] = ErrNotFleetPane
		}
	}
	priorityPaint(t, p)
	if proofs < 2 || len(f.moves) != 0 {
		t.Fatalf("fresh proof calls=%d moves=%v", proofs, f.moves)
	}
}
