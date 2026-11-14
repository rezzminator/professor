package herdr

import (
	"encoding/json"
	"fmt"
	"maps"
	"time"
)

// The two kinds of target an indicator is painted on, named as Herdr names the
// listing and the report that carry them.
const (
	targetPane      = "pane"
	targetWorkspace = "workspace"
)

// indicatorTarget is one pane or workspace as Herdr lists it: its id, its
// agent_status and the custom tokens it holds, all sources' alike (Herdr retains
// one token map per resource).
type indicatorTarget struct {
	kind   string
	id     string
	status string
	tokens map[string]string
}

// decodeIndicatorTargets reads a pane.list result and a workspace.list result
// into their targets, panes first. A workspace's tokens are omitted by Herdr
// when it holds none, so an absent map is an empty one; an entry with no id
// cannot be painted: it is skipped and named in skipped, and the rest paint.
func decodeIndicatorTargets(
	paneResult, workspaceResult json.RawMessage,
) (targets []indicatorTarget, skipped []string, err error) {
	// The workspace list's result type and envelope are its own parser's to
	// judge; the fields it drops are read here.
	panes, err := ParsePanes(paneResult)
	if err != nil {
		return nil, nil, err
	}
	if _, err := ParseWorkspaceList(workspaceResult); err != nil {
		return nil, nil, err
	}
	var workspaces struct {
		Workspaces []struct {
			WorkspaceID string            `json:"workspace_id"`
			Status      string            `json:"agent_status"`
			Tokens      map[string]string `json:"tokens"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(workspaceResult, &workspaces); err != nil {
		return nil, nil, fmt.Errorf("herdr workspace.list: decode result %q: %w", workspaceResult, err)
	}
	targets = make([]indicatorTarget, 0, len(panes)+len(workspaces.Workspaces))
	for index := range panes {
		pane := &panes[index]
		if pane.PaneID == "" {
			skipped = append(
				skipped,
				fmt.Sprintf("herdr pane.list: an entry has no pane_id (status %q)", pane.AgentStatus),
			)
			continue
		}
		targets = append(
			targets,
			indicatorTarget{kind: targetPane, id: pane.PaneID, status: pane.AgentStatus, tokens: pane.Tokens},
		)
	}
	for _, workspace := range workspaces.Workspaces {
		if workspace.WorkspaceID == "" {
			skipped = append(skipped, fmt.Sprintf(
				"herdr workspace.list: an entry has no workspace_id (status %q)", workspace.Status,
			))
			continue
		}
		targets = append(targets, indicatorTarget{
			kind: targetWorkspace, id: workspace.WorkspaceID, status: workspace.Status, tokens: workspace.Tokens,
		})
	}
	return targets, skipped, nil
}

// wantedIndicators is the indicator tokens a target in status carries: none
// for idle, and for blocked none in the blink's off-phase. known is false for
// a status Herdr does not document, which is painted as unknown.
func wantedIndicators(status string, blinkOn bool) (tokens map[string]string, known bool) {
	switch status {
	case "idle":
		return nil, true
	case "working":
		return map[string]string{IndicatorWorking: GlyphWorking}, true
	case "blocked":
		if blinkOn {
			return map[string]string{IndicatorBlocked: GlyphBlocked}, true
		}
		return nil, true
	case agentStatusDone:
		return map[string]string{IndicatorDone: GlyphDone}, true
	case "unknown":
		return map[string]string{IndicatorUnknown: GlyphUnknown}, true
	}
	return map[string]string{IndicatorUnknown: GlyphUnknown}, false
}

// indicatorPatch is the token patch that takes current to want over the four
// indicator tokens alone: a wanted token that is absent or differs is set, a
// held one that is not wanted is cleared (nil), every other token — another
// source's — is never named. nil when nothing differs.
func indicatorPatch(current, want map[string]string) map[string]*string {
	var patch map[string]*string
	for _, token := range IndicatorTokens() {
		glyph, wanted := want[token]
		held, has := current[token]
		switch {
		case wanted && (!has || held != glyph):
			if patch == nil {
				patch = map[string]*string{}
			}
			patch[token] = &glyph
		case !wanted && has:
			if patch == nil {
				patch = map[string]*string{}
			}
			patch[token] = nil
		}
	}
	return patch
}

// tokenReport is one pane.report_metadata or workspace.report_metadata request
// carrying a token patch under IndicatorSource, without a seq (Herdr ignores
// no report of it) and without a ttl (the token stays until the next paint).
type tokenReport struct {
	target indicatorTarget
	tokens map[string]*string
	seq    uint64
}

// Line renders the report request line (no trailing newline); a nil value in
// tokens is JSON null, which clears the token.
func (report tokenReport) Line() ([]byte, error) {
	if report.target.id == "" || len(report.tokens) == 0 {
		return nil, fmt.Errorf(
			"herdr token report: %s id %q and %d tokens, both are required",
			report.target.kind,
			report.target.id,
			len(report.tokens),
		)
	}
	id := fmt.Sprintf("pfm-%d", report.seq)
	switch report.target.kind {
	case targetPane:
		return json.Marshal(request{ID: id, Method: "pane.report_metadata", Params: struct {
			PaneID string             `json:"pane_id"`
			Source string             `json:"source"`
			Tokens map[string]*string `json:"tokens"`
		}{report.target.id, IndicatorSource, report.tokens}})
	case targetWorkspace:
		return json.Marshal(request{ID: id, Method: "workspace.report_metadata", Params: struct {
			WorkspaceID string             `json:"workspace_id"`
			Source      string             `json:"source"`
			Tokens      map[string]*string `json:"tokens"`
		}{report.target.id, IndicatorSource, report.tokens}})
	}
	return nil, fmt.Errorf(
		"herdr token report: target kind %q is neither %s nor %s",
		report.target.kind,
		targetPane,
		targetWorkspace,
	)
}

// agentStatusDone is the agent_status of a Herdr pane holding an unseen completion.
const agentStatusDone = "done"

// classifiedAs says whether the pane's last proof stands for this paint (its
// status and Herdr server unchanged, unprovedReprove not passed), and whether
// it proved the pane no fleet pane's. A clear proves every pane afresh.
func (painter *indicatorPainter) classifiedAs(target indicatorTarget) (cached, notFleet bool) {
	last, ok := painter.classified[target.id]
	if !ok || painter.clearing || last.Status != target.status || last.Server != painter.server ||
		painter.clock.Now().Sub(time.Unix(0, last.At)) >= unprovedReprove {
		return false, false
	}
	return true, last.NotFleet
}

// beginPaint readies a paint: a fresh one (the first, a nudge, the idle
// refresh) forgets every not-fleet proof, so a chat started in such a pane is
// proved at its hook's nudge; then it loads the unproved-pane memo.
func (painter *indicatorPainter) beginPaint(fresh bool) {
	if fresh {
		maps.DeleteFunc(painter.classified, func(_ string, last unprovedPane) bool { return last.NotFleet })
	}
	painter.loadUnproved()
}

// classify keeps the pane's proof for the paints classifiedAs serves.
func (painter *indicatorPainter) classify(target indicatorTarget, notFleet bool) {
	if painter.classified == nil {
		painter.classified = map[string]unprovedPane{}
	}
	painter.classified[target.id] = unprovedPane{
		Status: target.status, At: painter.clock.Now().UnixNano(), Server: painter.server, NotFleet: notFleet,
	}
}
