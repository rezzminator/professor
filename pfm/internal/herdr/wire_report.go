package herdr

import (
	"encoding/json"
	"fmt"
)

// This file renders the requests that publish a pane's agent to Herdr:
// pane.report_agent (its state), pane.report_agent_session (its session) and
// pane.release_agent (the user quit).

// Report is one pane.report_agent request: the state, and when the session is
// known the session reference and the command that resumes it.
type Report struct {
	Pane string
	// Source is the reporter Herdr keys the pane's state by; empty is Source.
	Source     string
	Agent      string
	State      State
	Message    string
	SessionID  string
	ResumeArgv []string
	Seq        uint64
}

type reportParams struct {
	PaneID     string   `json:"pane_id"`
	Source     string   `json:"source"`
	Agent      string   `json:"agent"`
	State      State    `json:"state"`
	Message    string   `json:"message,omitempty"`
	SessionID  string   `json:"agent_session_id,omitempty"`
	ResumeArgv []string `json:"resume_argv,omitempty"`
	Seq        uint64   `json:"seq"`
}

type releaseParams struct {
	PaneID string `json:"pane_id"`
	Source string `json:"source"`
	Agent  string `json:"agent"`
	Seq    uint64 `json:"seq"`
}

// Line renders the pane.report_agent request line (no trailing newline).
func (report Report) Line() ([]byte, error) {
	if report.Pane == "" || report.Agent == "" || report.State == "" {
		return nil, fmt.Errorf(
			"herdr report: pane %q, agent %q and state %q are all required",
			report.Pane,
			report.Agent,
			report.State,
		)
	}
	return json.Marshal(request{
		ID:     fmt.Sprintf("pfm-%d", report.Seq),
		Method: "pane.report_agent",
		Params: reportParams{
			PaneID: report.Pane, Source: sourceOr(report.Source), Agent: report.Agent, State: report.State,
			Message: capRunes(report.Message), SessionID: report.SessionID,
			ResumeArgv: report.ResumeArgv, Seq: report.Seq,
		},
	})
}

// SessionReport publishes a pane's session without taking ownership of its
// lifecycle state, which a native Claude pane leaves to Herdr's screen detection.
type SessionReport struct {
	Pane, Source, Agent, SessionID string
	// StartSource is Claude's SessionStart source ("startup", "resume");
	// Herdr takes a new session on a pane already tracking Claude only from
	// a "resume" report. Empty leaves it off the wire.
	StartSource string
	Seq         uint64
}

// Line renders pane.report_agent_session; state and message are not in its schema.
func (report SessionReport) Line() ([]byte, error) {
	if report.Pane == "" || report.Agent == "" {
		return nil, fmt.Errorf(
			"herdr session report: pane %q and agent %q are both required",
			report.Pane,
			report.Agent,
		)
	}
	return json.Marshal(request{
		ID: fmt.Sprintf("pfm-%d", report.Seq), Method: "pane.report_agent_session",
		Params: struct {
			Pane        string `json:"pane_id"`
			Source      string `json:"source"`
			Agent       string `json:"agent"`
			SessionID   string `json:"agent_session_id,omitempty"`
			StartSource string `json:"session_start_source,omitempty"`
			Seq         uint64 `json:"seq"`
		}{report.Pane, sourceOr(report.Source), report.Agent, report.SessionID, report.StartSource, report.Seq},
	})
}

// Release is one pane.release_agent request: the user quit.
type Release struct {
	Pane string
	// Source is the reporter whose state is released; empty is Source.
	Source string
	Agent  string
	Seq    uint64
}

// Line renders the pane.release_agent request line (no trailing newline).
func (release Release) Line() ([]byte, error) {
	if release.Pane == "" || release.Agent == "" {
		return nil, fmt.Errorf("herdr release: pane %q and agent %q are both required", release.Pane, release.Agent)
	}
	return json.Marshal(request{
		ID:     fmt.Sprintf("pfm-%d", release.Seq),
		Method: "pane.release_agent",
		Params: releaseParams{
			PaneID: release.Pane, Source: sourceOr(release.Source), Agent: release.Agent, Seq: release.Seq,
		},
	})
}

// sourceOr is source, or the chat reporter's Source when empty.
func sourceOr(source string) string {
	if source == "" {
		return Source
	}
	return source
}
