package herdr

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
)

// Herdr's socket API is newline-delimited JSON: one request line, one
// response line. A reporter must never slow its agent down, so the dial and
// the whole exchange are short.
const (
	dialTimeout = 500 * time.Millisecond
	ioTimeout   = time.Second
	// maxResponse caps the bytes read from Herdr in one exchange; a pane.list
	// of a big workspace is the largest answer.
	maxResponse = 1 << 20
	// MaxMessageRunes caps a blocked message so one huge notification never
	// becomes a huge request.
	MaxMessageRunes = 240
)

// Report is one pane.report_agent request: the state, and when the session is
// known the session reference and the command that resumes it.
type Report struct {
	Pane       string
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

type request struct {
	ID     string `json:"id"`
	Method string `json:"method"`
	Params any    `json:"params"`
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
			PaneID: report.Pane, Source: Source, Agent: report.Agent, State: report.State,
			Message: capRunes(report.Message), SessionID: report.SessionID,
			ResumeArgv: report.ResumeArgv, Seq: report.Seq,
		},
	})
}

// Release is one pane.release_agent request: the user quit.
type Release struct {
	Pane  string
	Agent string
	Seq   uint64
}

// Line renders the pane.release_agent request line (no trailing newline).
func (release Release) Line() ([]byte, error) {
	if release.Pane == "" || release.Agent == "" {
		return nil, fmt.Errorf("herdr release: pane %q and agent %q are both required", release.Pane, release.Agent)
	}
	return json.Marshal(request{
		ID:     fmt.Sprintf("pfm-%d", release.Seq),
		Method: "pane.release_agent",
		Params: releaseParams{PaneID: release.Pane, Source: Source, Agent: release.Agent, Seq: release.Seq},
	})
}

// AgentList is one agent.list request: every agent Herdr knows by name, so a
// rename can pick a name nobody holds.
type AgentList struct {
	Seq uint64
}

// Line renders the agent.list request line (no trailing newline).
func (list AgentList) Line() ([]byte, error) {
	return json.Marshal(request{
		ID:     fmt.Sprintf("pfm-%d", list.Seq),
		Method: "agent.list",
		Params: struct{}{},
	})
}

// Agent is one agent.list entry: the pane it runs in and the name it holds,
// empty when it holds none.
type Agent struct {
	PaneID string
	Name   string
}

// ParseAgentList decodes an agent.list result ({"type":"agent_list","agents":[…]});
// a null name is an unnamed agent.
func ParseAgentList(result json.RawMessage) ([]Agent, error) {
	var decoded struct {
		Type   string `json:"type"`
		Agents []struct {
			Name   *string `json:"name"`
			PaneID string  `json:"pane_id"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(result, &decoded); err != nil {
		return nil, fmt.Errorf("herdr agent.list: decode result %q: %w", result, err)
	}
	if decoded.Type != "agent_list" {
		return nil, fmt.Errorf("herdr agent.list: result type %q, want agent_list", decoded.Type)
	}
	agents := make([]Agent, 0, len(decoded.Agents))
	for _, agent := range decoded.Agents {
		entry := Agent{PaneID: agent.PaneID}
		if agent.Name != nil {
			entry.Name = *agent.Name
		}
		agents = append(agents, entry)
	}
	return agents, nil
}

// AgentRename is one agent.rename request: the agent in Target (a pane id or
// its current name) takes Name.
type AgentRename struct {
	Target string
	Name   string
	Seq    uint64
}

// Line renders the agent.rename request line (no trailing newline).
func (rename AgentRename) Line() ([]byte, error) {
	if rename.Target == "" || rename.Name == "" {
		return nil, fmt.Errorf("herdr rename: target %q and name %q are both required", rename.Target, rename.Name)
	}
	return json.Marshal(request{
		ID:     fmt.Sprintf("pfm-%d", rename.Seq),
		Method: "agent.rename",
		Params: struct {
			Target string `json:"target"`
			Name   string `json:"name"`
		}{rename.Target, rename.Name},
	})
}

// PaneList is one pane.list request with no workspace filter: every pane Herdr
// holds, each with the terminal that outlives a pane move.
type PaneList struct {
	Seq uint64
}

// Line renders the pane.list request line (no trailing newline).
func (list PaneList) Line() ([]byte, error) {
	return json.Marshal(request{ID: fmt.Sprintf("pfm-%d", list.Seq), Method: "pane.list", Params: struct{}{}})
}

// Pane is one pane.list entry: the public pane id (a move assigns a new one)
// and the terminal id (a move keeps it).
type Pane struct {
	PaneID     string
	TerminalID string
}

// ParsePanes decodes a pane.list result ({"type":"pane_list","panes":[…]}).
func ParsePanes(result json.RawMessage) ([]Pane, error) {
	var decoded struct {
		Type  string `json:"type"`
		Panes []struct {
			PaneID     string `json:"pane_id"`
			TerminalID string `json:"terminal_id"`
		} `json:"panes"`
	}
	if err := json.Unmarshal(result, &decoded); err != nil {
		return nil, fmt.Errorf("herdr pane.list: decode result %q: %w", result, err)
	}
	if decoded.Type != "pane_list" {
		return nil, fmt.Errorf("herdr pane.list: result type %q, want pane_list", decoded.Type)
	}
	panes := make([]Pane, 0, len(decoded.Panes))
	for _, pane := range decoded.Panes {
		panes = append(panes, Pane{PaneID: pane.PaneID, TerminalID: pane.TerminalID})
	}
	return panes, nil
}

// ProcessInfo is one pane.process_info request: the processes in the pane's
// foreground.
type ProcessInfo struct {
	Pane string
	Seq  uint64
}

// Line renders the pane.process_info request line (no trailing newline).
func (info ProcessInfo) Line() ([]byte, error) {
	if info.Pane == "" {
		return nil, fmt.Errorf("herdr process info: pane is required")
	}
	return json.Marshal(request{
		ID:     fmt.Sprintf("pfm-%d", info.Seq),
		Method: "pane.process_info",
		Params: struct {
			PaneID string `json:"pane_id"`
		}{info.Pane},
	})
}

// ParseProcessInfo decodes a pane.process_info result
// ({"type":"pane_process_info","process_info":{"foreground_processes":[…]}})
// into the foreground pids.
func ParseProcessInfo(result json.RawMessage) ([]int, error) {
	var decoded struct {
		Type        string `json:"type"`
		ProcessInfo struct {
			Foreground []struct {
				PID int `json:"pid"`
			} `json:"foreground_processes"`
		} `json:"process_info"`
	}
	if err := json.Unmarshal(result, &decoded); err != nil {
		return nil, fmt.Errorf("herdr pane.process_info: decode result %q: %w", result, err)
	}
	if decoded.Type != "pane_process_info" {
		return nil, fmt.Errorf("herdr pane.process_info: result type %q, want pane_process_info", decoded.Type)
	}
	pids := make([]int, 0, len(decoded.ProcessInfo.Foreground))
	for _, process := range decoded.ProcessInfo.Foreground {
		pids = append(pids, process.PID)
	}
	return pids, nil
}

// MaxTitleRunes is Herdr's cap on a pane title.
const MaxTitleRunes = 80

// Metadata is one pane.report_metadata request: the chat's title on its pane,
// or with ClearTitle the title withdrawn.
type Metadata struct {
	Pane       string
	Title      string
	ClearTitle bool
	Seq        uint64
}

// Line renders the pane.report_metadata request line (no trailing newline).
// The title loses control characters and surrounding space, is cut at
// MaxTitleRunes on a rune boundary, and an empty one is an error.
func (metadata Metadata) Line() ([]byte, error) {
	if metadata.Pane == "" {
		return nil, fmt.Errorf("herdr metadata: pane is required")
	}
	params := struct {
		PaneID     string `json:"pane_id"`
		Source     string `json:"source"`
		Title      string `json:"title,omitempty"`
		ClearTitle bool   `json:"clear_title,omitempty"`
	}{PaneID: metadata.Pane, Source: Source}
	if metadata.ClearTitle {
		params.ClearTitle = true
	} else {
		params.Title = capTitle(metadata.Title)
		if params.Title == "" {
			return nil, fmt.Errorf("herdr metadata: title %q is empty once cleaned", metadata.Title)
		}
	}
	return json.Marshal(
		request{ID: fmt.Sprintf("pfm-%d", metadata.Seq), Method: "pane.report_metadata", Params: params},
	)
}

func capTitle(title string) string {
	title = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, title))
	if runes := []rune(title); len(runes) > MaxTitleRunes {
		title = strings.TrimSpace(string(runes[:MaxTitleRunes]))
	}
	return title
}

func capRunes(message string) string {
	message = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, message)
	runes := []rune(message)
	if len(runes) <= MaxMessageRunes {
		return message
	}
	return string(runes[:MaxMessageRunes-1]) + "…"
}

// Refusal is Herdr answering a request with an error response.
type Refusal struct {
	Request string
	Code    string
	Message string
}

func (refusal *Refusal) Error() string {
	return fmt.Sprintf("herdr refused %s: %s: %s", refusal.Request, refusal.Code, refusal.Message)
}

// RefusedResume reports whether err is Herdr refusing the resume command
// (invalid_resume_argv or resume_not_accepted). Herdr applies none of such a
// report, so the caller resends the state without the command.
func RefusedResume(err error) bool {
	var refusal *Refusal
	return errors.As(err, &refusal) && (refusal.Code == "invalid_resume_argv" || refusal.Code == "resume_not_accepted")
}

// Send writes each request line to the Herdr socket in turn and reads its
// response line. A Herdr error response (invalid_resume_argv,
// resume_not_accepted, ...) is returned with the request method and the code,
// so a refusal never reads as success.
func Send(ctx context.Context, socket string, lines ...[]byte) error {
	_, err := Call(ctx, socket, lines...)
	return err
}

// Call is Send that also returns each response's "result" object, in request
// order, so a query (agent.list) can be answered over the same connection that
// then renames or reports.
func Call(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error) {
	conn, err := deps.DialUnix(ctx, socket, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("herdr socket %s: dial: %w", socket, err)
	}
	defer func() { _ = conn.Close() }()
	// The exchange ends at the I/O timeout or the caller's deadline, whichever
	// comes first, and a response is read up to maxResponse bytes.
	deadline := clock.Real.Now().Add(ioTimeout)
	if bound, ok := ctx.Deadline(); ok && bound.Before(deadline) {
		deadline = bound
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("herdr socket %s: set deadline: %w", socket, err)
	}
	reader := bufio.NewReader(io.LimitReader(conn, maxResponse))
	results := make([]json.RawMessage, 0, len(lines))
	for _, line := range lines {
		if _, err := conn.Write(append(append([]byte{}, line...), '\n')); err != nil {
			return nil, fmt.Errorf("herdr socket %s: write %s: %w", socket, line, err)
		}
		response, err := reader.ReadBytes('\n')
		if err != nil {
			return nil, fmt.Errorf("herdr socket %s: read response to %s: %w", socket, line, err)
		}
		var decoded struct {
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(response, &decoded); err != nil {
			return nil, fmt.Errorf(
				"herdr socket %s: decode response %q to %s: %w",
				socket,
				strings.TrimSpace(string(response)),
				line,
				err,
			)
		}
		if decoded.Error != nil {
			return nil, &Refusal{Request: string(line), Code: decoded.Error.Code, Message: decoded.Error.Message}
		}
		results = append(results, decoded.Result)
	}
	return results, nil
}
