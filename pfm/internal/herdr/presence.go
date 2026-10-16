package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

// What `pfm chat ls` and the chat MCP's ls show of a chat's Herdr presence,
// when it is not a pane: NoHerdr on a host without Herdr, NotInHerdr for a chat
// no Herdr pane holds. A failed lookup is never either: it reads
// "herdr error: {cause}" (PresenceError).
const (
	PresenceNoHerdr    = "—"
	PresenceNotInHerdr = "not in herdr"
	presenceErrorLead  = "herdr error: "
)

// PresenceError is the presence of a chat whose Herdr lookup failed.
func PresenceError(cause error) string { return presenceErrorLead + cause.Error() }

// Seat is one chat whose presence is asked: the tmux server socket path and
// the session of the chat (Pane when the row names no session). An empty
// SocketPath is a chat with no live tmux server. Err, when set, is why the
// seat could not be addressed at all.
type Seat struct {
	SocketPath string
	Session    string
	Pane       string
	Err        error
}

// Probe reads chats' Herdr presence. Getenv, LookPath, Tmux and Call are the
// seams the real tmux and Herdr sit behind; Warn receives the full context of
// every failed lookup.
type Probe struct {
	Getenv   func(name string) string
	LookPath func(name string) (string, error)
	// Tmux runs one tmux command against the server at socketPath.
	Tmux func(ctx context.Context, socketPath string, args ...string) (string, error)
	Call func(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error)
	Warn io.Writer
}

// hostHasHerdr is the reporter's rule: HERDR_BIN_PATH set, or herdr on PATH.
func (probe Probe) hostHasHerdr() bool {
	if probe.Getenv != nil && probe.Getenv(EnvBin) != "" {
		return true
	}
	if probe.LookPath == nil {
		return false
	}
	_, err := probe.LookPath("herdr")
	return err == nil
}

// Presence returns one presence string per seat, in order. On a host with no
// Herdr every seat reads PresenceNoHerdr and no tmux or socket call is made.
// Otherwise a seat reads PresenceNotInHerdr (no live tmux server, or no
// registered client pane left), or "{pane id} {workspace} {agent status}
// {agent name}" per registered client joined with ", " ("{pane id}
// {workspace} no-agent" for a pane Herdr lists no agent on), or its failure. Each
// Herdr socket is asked once for all seats.
func (probe Probe) Presence(ctx context.Context, seats []Seat) []string {
	presence := make([]string, len(seats))
	if !probe.hostHasHerdr() {
		for index := range presence {
			presence[index] = PresenceNoHerdr
		}
		return presence
	}
	sockets := make(map[string]*socketView)
	for index, seat := range seats {
		text, err := probe.seatPresence(ctx, seat, sockets)
		if err != nil {
			probe.warn("pfm chat ls: herdr presence of %+v: %v", seat, err)
			text = PresenceError(err)
		}
		presence[index] = text
	}
	return presence
}

func (probe Probe) warn(format string, args ...any) {
	if probe.Warn != nil {
		_, _ = fmt.Fprintf(probe.Warn, format+"\n", args...)
	}
}

func (probe Probe) seatPresence(ctx context.Context, seat Seat, sockets map[string]*socketView) (string, error) {
	if seat.Err != nil {
		return "", seat.Err
	}
	if seat.SocketPath == "" {
		return PresenceNotInHerdr, nil
	}
	target := seat.Session
	if target == "" {
		target = seat.Pane
	}
	if target == "" {
		return "", fmt.Errorf(
			"tmux server %s: the row names no session or pane to read %s from",
			seat.SocketPath,
			StoredOption,
		)
	}
	if probe.Tmux == nil {
		return "", fmt.Errorf("no tmux runner to read %s of %s", StoredOption, target)
	}
	output, err := probe.Tmux(ctx, seat.SocketPath, "show-options", "-qv", "-t", target, StoredOption)
	if err != nil {
		return "", fmt.Errorf("read %s of %s: %w", StoredOption, target, err)
	}
	raw := strings.TrimSpace(output)
	if raw == "" {
		return PresenceNotInHerdr, nil
	}
	var stored Stored
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return "", fmt.Errorf("decode %s of %s (%q): %w", StoredOption, target, raw, err)
	}
	pids := make([]string, 0, len(stored.Clients))
	for pid := range stored.Clients {
		pids = append(pids, pid)
	}
	sort.Strings(pids)
	var shown []string
	for _, pid := range pids {
		client := stored.Clients[pid]
		if client.Socket == "" || (client.Pane == "" && client.Terminal == "") {
			return "", fmt.Errorf(
				"%s of %s: client %s names no pane or no Herdr socket: %+v",
				StoredOption,
				target,
				pid,
				client,
			)
		}
		view, ok := sockets[client.Socket]
		if !ok {
			view = probe.look(ctx, client.Socket)
			sockets[client.Socket] = view
		}
		if view.err != nil {
			return "", view.err
		}
		if text, found := view.describe(client); found {
			shown = append(shown, text)
		}
	}
	if len(shown) == 0 {
		return PresenceNotInHerdr, nil
	}
	return strings.Join(shown, ", "), nil
}

// socketView is what one Herdr socket answered for a listing: its panes, the
// agents on them and the workspace labels, or why it could not.
type socketView struct {
	panes      []presencePane
	agents     map[string]Agent
	workspaces map[string]string
	err        error
}

type presencePane struct {
	PaneID      string `json:"pane_id"`
	TerminalID  string `json:"terminal_id"`
	WorkspaceID string `json:"workspace_id"`
}

// look asks one Herdr socket for pane.list, agent.list and workspace.list.
func (probe Probe) look(ctx context.Context, socket string) *socketView {
	view := &socketView{agents: map[string]Agent{}, workspaces: map[string]string{}}
	fail := func(err error) *socketView {
		view.err = fmt.Errorf("herdr socket %s: %w", socket, err)
		return view
	}
	if probe.Call == nil {
		return fail(fmt.Errorf("no Herdr caller"))
	}
	paneLine, err := PaneList{Seq: 1}.Line()
	if err != nil {
		return fail(err)
	}
	agentLine, err := AgentList{Seq: 2}.Line()
	if err != nil {
		return fail(err)
	}
	workspaceLine, err := WorkspaceList{Seq: 3}.Line()
	if err != nil {
		return fail(err)
	}
	results, err := probe.Call(ctx, socket, paneLine, agentLine, workspaceLine)
	if err != nil {
		return fail(err)
	}
	if len(results) != 3 {
		return fail(fmt.Errorf("answered %d of 3 requests", len(results)))
	}
	if view.panes, err = parsePresencePanes(results[0]); err != nil {
		return fail(err)
	}
	agents, err := ParseAgentList(results[1])
	if err != nil {
		return fail(err)
	}
	for _, agent := range agents {
		view.agents[agent.PaneID] = agent
	}
	if view.workspaces, err = ParseWorkspaceList(results[2]); err != nil {
		return fail(err)
	}
	return view
}

// describe renders one registered client: its current pane (found by the
// terminal id, which survives a pane move), workspace, agent status and name;
// found is false when no such pane exists any more.
func (view *socketView) describe(client Client) (text string, found bool) {
	for _, pane := range view.panes {
		matches := pane.PaneID == client.Pane
		if client.Terminal != "" {
			matches = pane.TerminalID == client.Terminal
		}
		if !matches {
			continue
		}
		workspace := pane.WorkspaceID
		if label := view.workspaces[workspace]; label != "" {
			workspace = label
		}
		fields := []string{pane.PaneID, workspace}
		agent, hasAgent := view.agents[pane.PaneID]
		if !hasAgent {
			fields = append(fields, "no-agent")
		} else {
			fields = append(fields, agent.Status)
			if agent.Name != "" {
				fields = append(fields, agent.Name)
			}
		}
		return strings.Join(fields, " "), true
	}
	return "", false
}

func parsePresencePanes(result json.RawMessage) ([]presencePane, error) {
	var decoded struct {
		Type  string         `json:"type"`
		Panes []presencePane `json:"panes"`
	}
	if err := json.Unmarshal(result, &decoded); err != nil {
		return nil, fmt.Errorf("herdr pane.list: decode result %q: %w", result, err)
	}
	if decoded.Type != "pane_list" {
		return nil, fmt.Errorf("herdr pane.list: result type %q, want pane_list", decoded.Type)
	}
	return decoded.Panes, nil
}

// WorkspaceList is one workspace.list request: every workspace Herdr holds.
type WorkspaceList struct {
	Seq uint64
}

// Line renders the workspace.list request line (no trailing newline).
func (list WorkspaceList) Line() ([]byte, error) {
	return json.Marshal(request{ID: fmt.Sprintf("pfm-%d", list.Seq), Method: "workspace.list", Params: struct{}{}})
}

// ParseWorkspaceList decodes a workspace.list result
// ({"type":"workspace_list","workspaces":[{"workspace_id","label"}…]}) into
// the label of each workspace id.
func ParseWorkspaceList(result json.RawMessage) (map[string]string, error) {
	var decoded struct {
		Type       string `json:"type"`
		Workspaces []struct {
			WorkspaceID string `json:"workspace_id"`
			Label       string `json:"label"`
		} `json:"workspaces"`
	}
	if err := json.Unmarshal(result, &decoded); err != nil {
		return nil, fmt.Errorf("herdr workspace.list: decode result %q: %w", result, err)
	}
	if decoded.Type != "workspace_list" {
		return nil, fmt.Errorf("herdr workspace.list: result type %q, want workspace_list", decoded.Type)
	}
	labels := make(map[string]string, len(decoded.Workspaces))
	for _, workspace := range decoded.Workspaces {
		labels[workspace.WorkspaceID] = workspace.Label
	}
	return labels, nil
}
