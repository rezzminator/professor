package report

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
)

// TopGrowths is how many largest-growth requests a context row names.
const TopGrowths = 3

// maxGrowthLabels is how many distinct call labels name one growth.
const maxGrowthLabels = 3

// mainChatAgent is the agent column of the main chat, which has no agent id.
const mainChatAgent = "-"

// contextRequest is one sized request of an agent.
type contextRequest struct {
	id     string
	tokens int64
}

// contextAgent is one agent's sized requests, in ts order.
type contextAgent struct {
	session, agent, agentType string
	requests                  []contextRequest
}

type requestGrowth struct {
	after int // index of the request that grew
	delta int64
}

// growthCallLabel names one call the way a context row reads it: a Bash
// call by its command shape, a file call by the file's base name.
func growthCallLabel(tool, filePath string, parts []storedPart) string {
	switch {
	case tool == bashTool:
		return bashTool + " " + callShape(parts)
	case filePath != "":
		return tool + " " + filepath.Base(filePath)
	default:
		return tool
	}
}

// growthCellText is one growth cell: the delta, then the calls of the
// predecessor request, whose results the grown request carried.
func growthCellText(delta int64, labels []string) string {
	seen := map[string]bool{}
	var kept []string
	for _, label := range labels {
		if !seen[label] {
			seen[label] = true
			kept = append(kept, label)
		}
	}
	if len(kept) == 0 {
		return fmt.Sprintf("+%d (no calls recorded)", delta)
	}
	text := strings.Join(kept[:min(len(kept), maxGrowthLabels)], ", ")
	if len(kept) > maxGrowthLabels {
		text += fmt.Sprintf(", +%d more", len(kept)-maxGrowthLabels)
	}
	return fmt.Sprintf("+%d %s", delta, text)
}

// Context ranks agents by peak context: per agent (session + agent id, the
// main chat is agent "-") its sized requests, start and peak context, mean
// growth per request and the TopGrowths requests that grew most over their
// predecessor, each named by the predecessor's calls.
func Context(ctx context.Context, store *callmeter.Store, f Filter, nameOf NameOf) (*Table, error) {
	n := newNames(nameOf)
	where, args := f.where()
	// A request is kept when it is sized and one of its calls passes the
	// filter: requests carry no cwd or agent type of their own.
	kept := `COALESCE(r.pending, 0) = 0 AND r.context_tokens IS NOT NULL
		AND EXISTS (SELECT 1 FROM calls c WHERE c.request_id = r.request_id AND ` + where + `)`
	agents := map[string]*contextAgent{}
	var order []*contextAgent
	err := query(ctx, store, "sized requests",
		`SELECT r.request_id, COALESCE(r.session_id, ''), COALESCE(r.agent_id, ''), r.context_tokens
		FROM requests r WHERE `+kept+` ORDER BY r.ts, r.request_id`, args,
		func(r rowSource) error {
			var id, session, agent string
			var tokens int64
			if err := r.Scan(&id, &session, &agent, &tokens); err != nil {
				return err
			}
			key := agentKey(session, agent)
			a, ok := agents[key]
			if !ok {
				a = &contextAgent{session: session, agent: agent}
				agents[key] = a
				order = append(order, a)
			}
			a.requests = append(a.requests, contextRequest{id: id, tokens: tokens})
			return nil
		})
	if err != nil {
		return nil, err
	}
	parts, err := loadParts(ctx, store, f)
	if err != nil {
		return nil, err
	}
	labels := map[string][]string{}
	err = query(ctx, store, "calls of sized requests",
		`SELECT c.request_id, c.tool_use_id, COALESCE(c.tool, ''), COALESCE(c.file_path, ''),
		COALESCE(c.session_id, ''), COALESCE(c.agent_id, ''), COALESCE(c.agent_type, '')
		FROM calls c WHERE c.request_id IN (SELECT r.request_id FROM requests r WHERE `+kept+`)
		ORDER BY c.ts, c.tool_use_id`, args,
		func(r rowSource) error {
			var request, id, tool, path, session, agent, agentType string
			if err := r.Scan(&request, &id, &tool, &path, &session, &agent, &agentType); err != nil {
				return err
			}
			labels[request] = append(labels[request], growthCallLabel(tool, path, parts[id]))
			if a, ok := agents[agentKey(session, agent)]; ok && a.agentType == "" {
				a.agentType = agentType
			}
			return nil
		})
	if err != nil {
		return nil, err
	}
	peak := func(a *contextAgent) int64 {
		var p int64
		for _, req := range a.requests {
			p = max(p, req.tokens)
		}
		return p
	}
	sort.SliceStable(order, func(i, j int) bool {
		if pi, pj := peak(order[i]), peak(order[j]); pi != pj {
			return pi > pj
		}
		return agentKey(order[i].session, order[i].agent) < agentKey(order[j].session, order[j].agent)
	})
	t := &Table{
		Title: f.title("context", n),
		Header: []string{
			"CHAT", "SESSION", "AGENT", "TYPE", "REQUESTS", "START", "PEAK", "MEAN GROWTH",
			"GROWTH 1", "GROWTH 2", "GROWTH 3",
		},
	}
	for _, a := range order[:min(len(order), f.limit())] {
		t.Rows = append(t.Rows, contextRow(a, peak(a), labels, n))
	}
	notes, err := gapNotes(ctx, store, f)
	if err != nil {
		return nil, err
	}
	t.Notes = notes
	t.Notes = append(t.Notes, n.notes()...)
	return t, nil
}

// contextRow renders one agent: growths are positive deltas over the
// predecessor request, largest first, earlier first on a tie.
func contextRow(a *contextAgent, peak int64, labels map[string][]string, n *names) []string {
	reqs := a.requests
	agent, agentType := a.agent, a.agentType
	if agent == "" {
		agent = mainChatAgent
	}
	if agentType == "" {
		agentType = "-"
	}
	mean := "-"
	if len(reqs) > 1 {
		mean = itoa((reqs[len(reqs)-1].tokens - reqs[0].tokens) / int64(len(reqs)-1))
	}
	var growths []requestGrowth
	for i := 1; i < len(reqs); i++ {
		if delta := reqs[i].tokens - reqs[i-1].tokens; delta > 0 {
			growths = append(growths, requestGrowth{after: i, delta: delta})
		}
	}
	sort.SliceStable(growths, func(i, j int) bool { return growths[i].delta > growths[j].delta })
	row := []string{
		n.of(a.session), a.session, agent, agentType, itoa(int64(len(reqs))), itoa(reqs[0].tokens), itoa(peak), mean,
	}
	for i := range TopGrowths {
		if i >= len(growths) {
			row = append(row, "-")
			continue
		}
		g := growths[i]
		row = append(row, growthCellText(g.delta, labels[reqs[g.after-1].id]))
	}
	return row
}
