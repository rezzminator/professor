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
			PaneID: report.Pane, Source: sourceOr(report.Source), Agent: report.Agent, State: report.State,
			Message: capRunes(report.Message), SessionID: report.SessionID,
			ResumeArgv: report.ResumeArgv, Seq: report.Seq,
		},
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

// Agent is one agent.list entry: the pane it runs in, its workspace and tab,
// the agent label and state Herdr shows, and the name it holds, empty when it
// holds none.
type Agent struct {
	PaneID      string
	WorkspaceID string
	TabID       string
	Agent       string
	Status      string
	Name        string
}

// ParseAgentList decodes an agent.list result ({"type":"agent_list","agents":[…]});
// a null name is an unnamed agent.
func ParseAgentList(result json.RawMessage) ([]Agent, error) {
	var decoded struct {
		Type   string `json:"type"`
		Agents []struct {
			Name        *string `json:"name"`
			PaneID      string  `json:"pane_id"`
			WorkspaceID string  `json:"workspace_id"`
			TabID       string  `json:"tab_id"`
			Agent       string  `json:"agent"`
			Status      string  `json:"agent_status"`
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
		entry := Agent{
			PaneID:      agent.PaneID,
			WorkspaceID: agent.WorkspaceID,
			TabID:       agent.TabID,
			Agent:       agent.Agent,
			Status:      agent.Status,
		}
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

// TabGet is one tab.get request: the tab's label and how many panes it holds.
type TabGet struct {
	Tab string
	Seq uint64
}

// Line renders the tab.get request line (no trailing newline).
func (get TabGet) Line() ([]byte, error) {
	if get.Tab == "" {
		return nil, fmt.Errorf("herdr tab.get: tab is required")
	}
	return json.Marshal(request{
		ID:     fmt.Sprintf("pfm-%d", get.Seq),
		Method: "tab.get",
		Params: struct {
			TabID string `json:"tab_id"`
		}{get.Tab},
	})
}

// Tab is one tab.get answer: the tab, the label Herdr's tab bar shows, and
// how many panes it holds.
type Tab struct {
	TabID     string
	Label     string
	PaneCount int
}

// ParseTab decodes a tab.get result ({"type":"tab_info","tab":{…}}).
func ParseTab(result json.RawMessage) (Tab, error) {
	var decoded struct {
		Type string `json:"type"`
		Tab  struct {
			TabID     string `json:"tab_id"`
			Label     string `json:"label"`
			PaneCount int    `json:"pane_count"`
		} `json:"tab"`
	}
	if err := json.Unmarshal(result, &decoded); err != nil {
		return Tab{}, fmt.Errorf("herdr tab.get: decode result %q: %w", result, err)
	}
	if decoded.Type != "tab_info" {
		return Tab{}, fmt.Errorf("herdr tab.get: result type %q, want tab_info", decoded.Type)
	}
	if decoded.Tab.TabID == "" {
		return Tab{}, fmt.Errorf("herdr tab.get: result %q names no tab", result)
	}
	return Tab{TabID: decoded.Tab.TabID, Label: decoded.Tab.Label, PaneCount: decoded.Tab.PaneCount}, nil
}

// TabLabel is the label TabRename sends for label: the label a tab carries
// once renamed to it.
func TabLabel(label string) string {
	return capTitle(label)
}

// TabRename is one tab.rename request: the tab's bar label becomes Label.
type TabRename struct {
	Tab   string
	Label string
	Seq   uint64
}

// Line renders the tab.rename request line (no trailing newline). The label is
// cleaned as a title is (control characters and surrounding space gone, cut at
// MaxTitleRunes), and an empty one is an error.
func (rename TabRename) Line() ([]byte, error) {
	if rename.Tab == "" {
		return nil, fmt.Errorf("herdr tab.rename: tab is required")
	}
	label := TabLabel(rename.Label)
	if label == "" {
		return nil, fmt.Errorf("herdr tab.rename: label %q is empty once cleaned", rename.Label)
	}
	return json.Marshal(request{
		ID:     fmt.Sprintf("pfm-%d", rename.Seq),
		Method: "tab.rename",
		Params: struct {
			TabID string `json:"tab_id"`
			Label string `json:"label"`
		}{rename.Tab, label},
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

// ParseShellPID decodes a pane.process_info result into the pid of the pane's
// own shell (0 when Herdr names none).
func ParseShellPID(result json.RawMessage) (int, error) {
	var decoded struct {
		Type        string `json:"type"`
		ProcessInfo struct {
			ShellPID int `json:"shell_pid"`
		} `json:"process_info"`
	}
	if err := json.Unmarshal(result, &decoded); err != nil {
		return 0, fmt.Errorf("herdr pane.process_info: decode result %q: %w", result, err)
	}
	if decoded.Type != "pane_process_info" {
		return 0, fmt.Errorf("herdr pane.process_info: result type %q, want pane_process_info", decoded.Type)
	}
	return decoded.ProcessInfo.ShellPID, nil
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
	Pane string
	// Source is the reporter whose title this is; empty is Source.
	Source     string
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
	}{PaneID: metadata.Pane, Source: sourceOr(metadata.Source)}
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

// Send writes each request line to the Herdr socket in turn, one connection
// per line, and reads its response line. A Herdr error response (invalid_resume_argv,
// resume_not_accepted, ...) is returned with the request method and the code,
// so a refusal never reads as success.
func Send(ctx context.Context, socket string, lines ...[]byte) error {
	_, err := Call(ctx, socket, lines...)
	return err
}

// Call is Send that also returns each response's "result" object, in request
// order. Herdr's socket answers one request per connection and then closes it
// (a second line on the same connection is never read: EOF or a broken pipe),
// so every line is its own dial, write and read; a failure on line k stops
// there and names line k.
func Call(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error) {
	results := make([]json.RawMessage, 0, len(lines))
	for _, line := range lines {
		result, err := callOne(ctx, socket, line)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, nil
}

// callOne is one request over one connection.
func callOne(ctx context.Context, socket string, line []byte) (json.RawMessage, error) {
	return callOneWithin(ctx, socket, line, ioTimeout)
}

func callOneWithin(ctx context.Context, socket string, line []byte, limit time.Duration) (json.RawMessage, error) {
	conn, err := deps.DialUnix(ctx, socket, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("herdr socket %s: dial for %s: %w", socket, requestName(line), err)
	}
	defer func() { _ = conn.Close() }()
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopCancel()
	// The exchange ends at the I/O timeout or the caller's deadline, whichever
	// comes first, and a response is read up to maxResponse bytes.
	deadline := clock.Real.Now().Add(limit)
	if bound, ok := ctx.Deadline(); ok && bound.Before(deadline) {
		deadline = bound
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, fmt.Errorf("herdr socket %s: set deadline: %w", socket, err)
	}
	if _, err := conn.Write(append(append([]byte{}, line...), '\n')); err != nil {
		return nil, fmt.Errorf("herdr socket %s: write %s: %w", socket, requestName(line), err)
	}
	response, err := bufio.NewReader(io.LimitReader(conn, maxResponse)).ReadBytes('\n')
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		return nil, fmt.Errorf("herdr socket %s: read response to %s: %w", socket, requestName(line), err)
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
			requestName(line),
			err,
		)
	}
	if decoded.Error != nil {
		return nil, &Refusal{Request: requestName(line), Code: decoded.Error.Code, Message: decoded.Error.Message}
	}
	return decoded.Result, nil
}

// requestName names a request in an error by its method, id and pane, never
// its params: a report's message can carry a command line the user typed, and
// every error lands in a log on disk that pfm doctor prints.
func requestName(line []byte) string {
	var request struct {
		ID     string `json:"id"`
		Method string `json:"method"`
		Params struct {
			PaneID string `json:"pane_id"`
		} `json:"params"`
	}
	if err := json.Unmarshal(line, &request); err != nil {
		return fmt.Sprintf("a request of %d bytes pfm cannot decode (%v)", len(line), err)
	}
	name := strings.TrimSpace(request.Method + " " + request.ID)
	if request.Params.PaneID != "" {
		name += " pane " + request.Params.PaneID
	}
	return name
}
