package herdr

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"testing"
)

func priorityHomeCount(t *testing.T, p *indicatorPainter) int {
	t.Helper()
	body, err := os.ReadFile(p.priorityPath())
	if err != nil {
		t.Fatal(err)
	}
	var memo struct {
		Homes map[string]json.RawMessage `json:"homes"`
	}
	if err := json.Unmarshal(body, &memo); err != nil {
		t.Fatal(err)
	}
	return len(memo.Homes)
}

func TestTabPriorityReturnOnSeen(t *testing.T) {
	for _, scenario := range []string{"neighbour", "neighbour gone", "dragged", "closed", "already focused", "restart", "off", "reblocked"} {
		t.Run(scenario, func(t *testing.T) {
			f := &priorityFixture{
				order:      []string{"a", "b", "chat", "tail"},
				agents:     []priorityAgent{priorityChat("chat-pane", "chat", "blocked", 1)},
				focusedTab: "a", focusedPane: "a-pane",
			}
			p := newPriorityPainter(t, f)
			if scenario == "already focused" {
				f.focusedTab, f.focusedPane = "chat", "chat-pane"
			}
			priorityPaint(t, p)
			if scenario == "already focused" {
				if len(f.moves) != 0 {
					t.Fatalf("already-seen tab moved: %v", f.moves)
				}
				return
			}
			if !slices.Equal(f.order, []string{"chat", "a", "b", "tail"}) {
				t.Fatalf("priority setup = %v", f.order)
			}
			if priorityHomeCount(t, p) != 1 {
				t.Fatal("priority move did not persist its home")
			}
			want := []string{"a", "b", "chat", "tail"}
			switch scenario {
			case "neighbour":
				f.order = []string{"chat", "b", "a", "tail"}
				want = []string{"b", "chat", "a", "tail"}
			case "neighbour gone":
				f.order = []string{"chat", "a", "tail"}
				want = []string{"a", "tail", "chat"}
			case "dragged":
				f.order = []string{"a", "chat", "b", "tail"}
				want = slices.Clone(f.order)
			case "closed":
				f.order, f.agents = []string{"a", "b", "tail"}, []priorityAgent{}
				want = slices.Clone(f.order)
			case "restart":
				q := newPriorityPainter(t, f)
				q.lockDir = p.lockDir
				p = q
			case "off":
				p.priority = func() (bool, error) { return false, nil }
				want = slices.Clone(f.order)
			}
			// Focus a different (shell) pane in the same tab, not just its agent.
			f.focusedTab, f.focusedPane = "chat", "shell-pane"
			priorityPaint(t, p)
			if !slices.Equal(f.order, want) || f.focusedTab != "chat" || f.focusedPane != "shell-pane" {
				t.Fatalf("seen return order=%v want=%v focus=%s/%s", f.order, want, f.focusedTab, f.focusedPane)
			}
			if scenario != "off" && priorityHomeCount(t, p) != 0 {
				t.Fatal("seen/dragged/closed tab retained its home")
			}
			n := len(f.moves)
			priorityPaint(t, p)
			if len(f.moves) != n {
				t.Fatalf("same focus repeated return: %v", f.moves)
			}
			if scenario == "reblocked" {
				f.focusedTab, f.focusedPane = "a", "a-pane"
				f.agents[0].State, f.agents[0].Seq = "working", 2
				priorityPaint(t, p)
				f.agents[0].State, f.agents[0].Seq = "blocked", 3
				priorityPaint(t, p)
				if f.order[0] != "chat" || priorityHomeCount(t, p) != 1 {
					t.Fatalf("reblocked tab did not get fresh move/home: %v", f.order)
				}
			}
		})
	}
}

func TestTabPrioritySeenStaysSeenOnOtherTransition(t *testing.T) {
	f := &priorityFixture{
		order:  []string{"a", "chat", "other"},
		agents: []priorityAgent{priorityChat("p1", "chat", "blocked", 1)},
	}
	p := newPriorityPainter(t, f)
	priorityPaint(t, p)
	f.focusedTab = "chat"
	priorityPaint(t, p)
	f.focusedTab = "a"
	f.agents = append(f.agents, priorityChat("p2", "other", "blocked", 2))
	priorityPaint(t, p)
	if !slices.Equal(f.order, []string{"other", "a", "chat"}) {
		t.Fatalf("seen tab rejoined priority line on another transition: %v", f.order)
	}
}

func TestTabPriorityDragDuringProofClearsPersistedHome(t *testing.T) {
	f := &priorityFixture{
		order: []string{"a", "first", "chat"},
		agents: []priorityAgent{
			priorityChat("p1", "first", "blocked", 1),
			priorityChat("p2", "chat", "done", 2),
		},
	}
	p := newPriorityPainter(t, f)
	priorityPaint(t, p)
	// Chat now outranks first; the user drags it back during its fresh proof,
	// after the initial tab snapshot. Cancelling must also release its slot.
	f.agents[1].State, f.agents[1].Seq = "blocked", 0
	proofs := 0
	f.afterProof = func(pane string) {
		if pane == "p2" {
			proofs++
			if proofs > 1 {
				f.order = []string{"first", "a", "chat"}
			}
		}
	}
	p.prioritize(context.Background())
	if priorityHomeCount(t, p) != 1 || len(f.moves) != 2 {
		t.Fatalf("drag cancellation not durable: homes=%d moves=%v", priorityHomeCount(t, p), f.moves)
	}
}

func TestTabPriorityOwnMovesKeepOtherHomes(t *testing.T) {
	f := &priorityFixture{
		order:      []string{"a", "b", "chat", "other", "tail"},
		agents:     []priorityAgent{priorityChat("p1", "chat", "blocked", 1)},
		focusedTab: "a",
	}
	p := newPriorityPainter(t, f)
	priorityPaint(t, p)
	// A newer priority move shifts chat's placed index, but is not a drag.
	f.agents = append(f.agents, priorityChat("p2", "other", "blocked", 0))
	priorityPaint(t, p)
	if !slices.Equal(f.order, []string{"other", "chat", "a", "b", "tail"}) || priorityHomeCount(t, p) != 2 {
		t.Fatalf("promotion lost another tab's home: order=%v homes=%d", f.order, priorityHomeCount(t, p))
	}
	// Read the shared memo in another painter before either tab returns.
	q := newPriorityPainter(t, f)
	q.lockDir = p.lockDir
	f.focusedTab = "other"
	priorityPaint(t, q)
	if !slices.Equal(f.order, []string{"chat", "a", "b", "other", "tail"}) || priorityHomeCount(t, q) != 1 {
		t.Fatalf("return lost another tab's home: order=%v homes=%d", f.order, priorityHomeCount(t, q))
	}
	f.focusedTab = "chat"
	priorityPaint(t, q)
	if !slices.Equal(f.order, []string{"a", "b", "chat", "other", "tail"}) || priorityHomeCount(t, q) != 0 {
		t.Fatalf("shifted tab failed to return: order=%v homes=%d", f.order, priorityHomeCount(t, q))
	}
}

func TestTabPriorityReturnFallbackClamp(t *testing.T) {
	f := &priorityFixture{
		order:  []string{"a", "b", "chat"},
		agents: []priorityAgent{priorityChat("p", "chat", "done", 1)},
	}
	p := newPriorityPainter(t, f)
	priorityPaint(t, p)
	f.order, f.focusedTab = []string{"chat", "a"}, "chat"
	p.prioritize(context.Background())
	if !slices.Equal(f.order, []string{"a", "chat"}) {
		t.Fatalf("fallback not clamped: %v", f.order)
	}
}
