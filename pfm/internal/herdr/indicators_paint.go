package herdr

import (
	"encoding/json"
	"fmt"
)

// The two kinds of target an indicator is painted on, named as Herdr names the
// listing and the report that carry them.
const (
	targetPane      = "pane"
	targetWorkspace = "workspace"
)

// indicatorTarget is one pane or workspace as Herdr lists it: its id, its
// agent_status and the custom tokens it holds, all sources' alike (Herdr keeps
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
// cannot be painted and is an error, never skipped.
func decodeIndicatorTargets(paneResult, workspaceResult json.RawMessage) ([]indicatorTarget, error) {
	// The result type and the shape of the envelope are the lists' own parsers'
	// to judge; the fields they drop are read here.
	if _, err := ParsePanes(paneResult); err != nil {
		return nil, err
	}
	if _, err := ParseWorkspaceList(workspaceResult); err != nil {
		return nil, err
	}
	var panes struct {
		Panes []struct {
			PaneID string            `json:"pane_id"`
			Status string            `json:"agent_status"`
			Tokens map[string]string `json:"tokens"`
		} `json:"panes"`
	}
	if err := json.Unmarshal(paneResult, &panes); err != nil {
		return nil, fmt.Errorf("herdr pane.list: decode result %q: %w", paneResult, err)
	}
	var workspaces struct {
		Workspaces []struct {
			WorkspaceID string            `json:"workspace_id"`
			Status      string            `json:"agent_status"`
			Tokens      map[string]string `json:"tokens"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(workspaceResult, &workspaces); err != nil {
		return nil, fmt.Errorf("herdr workspace.list: decode result %q: %w", workspaceResult, err)
	}
	targets := make([]indicatorTarget, 0, len(panes.Panes)+len(workspaces.Workspaces))
	for _, pane := range panes.Panes {
		if pane.PaneID == "" {
			return nil, fmt.Errorf("herdr pane.list: an entry has no pane_id (status %q)", pane.Status)
		}
		targets = append(
			targets,
			indicatorTarget{kind: targetPane, id: pane.PaneID, status: pane.Status, tokens: pane.Tokens},
		)
	}
	for _, workspace := range workspaces.Workspaces {
		if workspace.WorkspaceID == "" {
			return nil, fmt.Errorf("herdr workspace.list: an entry has no workspace_id (status %q)", workspace.Status)
		}
		targets = append(targets, indicatorTarget{
			kind: targetWorkspace, id: workspace.WorkspaceID, status: workspace.Status, tokens: workspace.Tokens,
		})
	}
	return targets, nil
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
	case "done":
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
