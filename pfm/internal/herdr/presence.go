package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// What `pfm chat ls` and the chat MCP's ls show of a chat's Herdr presence,
// when it is not a pane: NoHerdr on a host without Herdr, NotInHerdr for a chat
// no tmux client of it sits in a Herdr pane, AttachedOutside for a chat whose
// every tmux client runs outside Herdr. A failed lookup is never any of them:
// it reads "herdr unreachable: {cause}" when the Herdr socket could not be read
// (PresenceUnreachable) and "herdr error: {cause}" otherwise (PresenceError).
const (
	PresenceNoHerdr         = "—"
	PresenceNotInHerdr      = "not in herdr"
	PresenceAttachedOutside = "attached outside Herdr"
	presenceErrorLead       = "herdr error: "
	presenceUnreachableLead = "herdr unreachable: "
	// hooklessLead leads the detail a pane whose agent Herdr reports `unknown`
	// carries when its chat was launched without the Herdr reporter hooks.
	hooklessLead = " · launched without Herdr hooks: pfm chat reload "
)

// PresenceError is the presence of a chat whose Herdr lookup failed.
func PresenceError(cause error) string { return presenceErrorLead + cause.Error() }

// PresenceUnreachable is the presence of a chat whose Herdr socket answered
// nothing usable.
func PresenceUnreachable(cause error) string { return presenceUnreachableLead + cause.Error() }

// unreachableError is a Herdr socket that could not be read: the cause of a
// PresenceUnreachable text, kept distinct through every wrap of the resolver.
type unreachableError struct{ err error }

func (unreachable *unreachableError) Error() string { return unreachable.err.Error() }

func (unreachable *unreachableError) Unwrap() error { return unreachable.err }

// Seat is one chat whose presence is asked: the tmux server socket path and
// the session of the chat (Pane when the row names no session), its name and
// the pids of the processes in its tmux pane. An empty SocketPath is a chat
// with no live tmux server. Err, when set, is why the seat could not be
// addressed at all.
type Seat struct {
	SocketPath string
	Session    string
	Pane       string
	Name       string
	PanePIDs   []int
	Err        error
}

// Probe reads chats' Herdr presence. Getenv, LookPath, Tmux, Call and Environ
// are the seams the real tmux, Herdr and process table sit behind; Warn
// receives the full context of every failed lookup.
type Probe struct {
	Getenv   func(name string) string
	LookPath func(name string) (string, error)
	// Tmux runs one tmux command against the server at socketPath.
	Tmux func(ctx context.Context, socketPath string, args ...string) (string, error)
	Call func(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error)
	// Environ reads another process's environment (gather's ProcFS.Environ):
	// how a tmux client no hook registered is found to run in Herdr.
	Environ func(pid int) (map[string]string, error)
	// Hookless says whether the chat of a seat was launched without the Herdr
	// reporter hooks; known is false when its launch cannot be read, and the
	// detail is then left out, never guessed.
	Hookless func(seat Seat) (hookless, known bool)
	Warn     io.Writer
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
// Otherwise a seat reads, for its registered clients plus every live tmux
// client of its session, "{pane id} {workspace} {agent status} {agent name}"
// per client joined with ", " ("{pane id} {workspace} no-agent" for a pane
// Herdr lists no agent on; an `unknown` agent of a chat launched without the
// reporter hooks gains the reload hint; a live client Herdr cannot place is
// named after the panes shown as its failure text); else PresenceAttachedOutside when its
// clients run outside Herdr, PresenceNotInHerdr when no client sits in a pane,
// or its failure. Each Herdr socket is asked once for all seats.
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
			text = presenceFailure(err)
		}
		presence[index] = text
	}
	return presence
}

// presenceFailure is the text of a failed presence read: unreachable when a
// Herdr socket answered nothing usable, else the error.
func presenceFailure(err error) string {
	var unreachable *unreachableError
	if errors.As(err, &unreachable) {
		return PresenceUnreachable(err)
	}
	return PresenceError(err)
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
	var stored Stored
	if raw := strings.TrimSpace(output); raw != "" {
		if err := json.Unmarshal([]byte(raw), &stored); err != nil {
			return "", fmt.Errorf("decode %s of %s (%q): %w", StoredOption, target, raw, err)
		}
	}
	live, err := probe.liveClients(ctx, seat, target)
	if err != nil {
		return "", err
	}
	pids := clientPIDs(stored, live)
	if len(pids) == 0 {
		return PresenceNotInHerdr, nil
	}
	resolver := probe.resolver(sockets)
	var shown []string
	var unplaced []error
	outside := 0
	hooklessKnown, hookless := false, false
	for _, pid := range pids {
		var hit presenceHit
		var found bool
		if client, registered := stored.Clients[pid]; registered {
			if client.Socket == "" || (client.Pane == "" && client.Terminal == "") {
				return "", fmt.Errorf(
					"%s of %s: client %s names no pane or no Herdr socket: %+v",
					StoredOption,
					target,
					pid,
					client,
				)
			}
			view := probe.socketViewOf(ctx, client.Socket, sockets)
			if view.err != nil {
				return "", view.err
			}
			hit, found = view.describe(client)
			if !found && live[pid] {
				// The registered pane is gone, but the client is still attached:
				// find where it runs now.
				hit, found, outside, err = probe.resolveLive(ctx, resolver, sockets, pid, outside)
				if err != nil {
					unplaced = append(unplaced, err)
					continue
				}
			}
		} else {
			hit, found, outside, err = probe.resolveLive(ctx, resolver, sockets, pid, outside)
			if err != nil {
				unplaced = append(unplaced, err)
				continue
			}
		}
		if !found {
			continue
		}
		text := hit.text
		if hit.status == "unknown" && seat.Name != "" && probe.Hookless != nil {
			if !hooklessKnown {
				hookless, hooklessKnown = probe.Hookless(seat)
			}
			if hooklessKnown && hookless {
				text += hooklessLead + seat.Name
			}
		}
		shown = append(shown, text)
	}
	// A live client Herdr cannot place fails the seat alone; beside a placed
	// one it is named after the panes shown, as the reporter still serves them.
	if len(unplaced) > 0 {
		failure := errors.Join(unplaced...)
		if len(shown) == 0 {
			return "", failure
		}
		probe.warn("pfm chat ls: herdr presence of %+v: %v", seat, failure)
		shown = append(shown, presenceFailure(failure))
	}
	switch {
	case len(shown) > 0:
		return strings.Join(shown, ", "), nil
	case outside > 0:
		return PresenceAttachedOutside, nil
	default:
		return PresenceNotInHerdr, nil
	}
}

// resolveLive finds the Herdr pane the live tmux client pid runs in and
// describes it; a client running outside Herdr is counted in outside, a client
// Herdr cannot place is the error.
func (probe Probe) resolveLive(
	ctx context.Context,
	resolver Resolver,
	sockets map[string]*socketView,
	pid string,
	outside int,
) (hit presenceHit, found bool, outsideNow int, err error) {
	number, err := strconv.Atoi(pid)
	if err != nil {
		return presenceHit{}, false, outside, fmt.Errorf("tmux client pid %q is not a number: %w", pid, err)
	}
	if resolver.Environ == nil {
		return presenceHit{}, false, outside, fmt.Errorf("no process reader to place tmux client %s", pid)
	}
	location, inHerdr, err := resolver.Resolve(ctx, number)
	if err != nil {
		return presenceHit{}, false, outside, fmt.Errorf("place tmux client %s: %w", pid, err)
	}
	if !inHerdr {
		return presenceHit{}, false, outside + 1, nil
	}
	view := probe.socketViewOf(ctx, location.Socket, sockets)
	if view.err != nil {
		return presenceHit{}, false, outside, view.err
	}
	hit, found = view.describe(Client{Pane: location.Pane, Terminal: location.Terminal, Socket: location.Socket})
	return hit, found, outside, nil
}

// liveClients is the pids of the tmux clients attached to target now.
func (probe Probe) liveClients(ctx context.Context, seat Seat, target string) (map[string]bool, error) {
	output, err := probe.Tmux(ctx, seat.SocketPath, "list-clients", "-t", target, "-F", "#{client_pid}")
	if err != nil {
		return nil, fmt.Errorf("list tmux clients of %s: %w", target, err)
	}
	live := map[string]bool{}
	for _, word := range strings.Fields(output) {
		if _, err := strconv.Atoi(word); err != nil {
			return nil, fmt.Errorf("list tmux clients of %s: client pid %q is not a number: %w", target, word, err)
		}
		live[word] = true
	}
	return live, nil
}

// clientPIDs is the registered clients' keys plus the live clients' pids, each
// once, in pid order.
func clientPIDs(stored Stored, live map[string]bool) []string {
	seen := map[string]bool{}
	var pids []string
	for pid := range stored.Clients {
		seen[pid] = true
		pids = append(pids, pid)
	}
	for pid := range live {
		if !seen[pid] {
			pids = append(pids, pid)
		}
	}
	sort.Slice(pids, func(left, right int) bool {
		leftNumber, leftErr := strconv.Atoi(pids[left])
		rightNumber, rightErr := strconv.Atoi(pids[right])
		if leftErr == nil && rightErr == nil {
			return leftNumber < rightNumber
		}
		return pids[left] < pids[right]
	})
	return pids
}

// resolver is the shared pane rule over this probe's seams: the pane listing
// comes from the cached socket view, the process info from the Herdr socket.
func (probe Probe) resolver(sockets map[string]*socketView) Resolver {
	var sequence uint64
	next := func() uint64 {
		sequence++
		return sequence
	}
	wire := WireResolver(probe.Environ, probe.Call, next)
	return Resolver{
		Environ: probe.Environ,
		Panes: func(ctx context.Context, socket string) ([]Pane, error) {
			view := probe.socketViewOf(ctx, socket, sockets)
			if view.err != nil {
				return nil, view.err
			}
			panes := make([]Pane, len(view.panes))
			for index, pane := range view.panes {
				panes[index] = Pane{PaneID: pane.PaneID, TerminalID: pane.TerminalID}
			}
			return panes, nil
		},
		Foreground: wire.Foreground,
	}
}

// socketViewOf is the cached view of one Herdr socket.
func (probe Probe) socketViewOf(ctx context.Context, socket string, sockets map[string]*socketView) *socketView {
	view, ok := sockets[socket]
	if !ok {
		view = probe.look(ctx, socket)
		sockets[socket] = view
	}
	return view
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
		view.err = &unreachableError{fmt.Errorf("herdr socket %s: %w", socket, err)}
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

// presenceHit is one client's pane as shown: its text and the agent status the
// text carries ("" for a pane with no agent).
type presenceHit struct {
	text   string
	status string
}

// describe renders one client's pane (found by the terminal id, which survives
// a pane move): pane, workspace, agent status and name; found is false when no
// such pane exists any more.
func (view *socketView) describe(client Client) (hit presenceHit, found bool) {
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
		status := ""
		if !hasAgent {
			fields = append(fields, "no-agent")
		} else {
			status = agent.Status
			fields = append(fields, agent.Status)
			if agent.Name != "" {
				fields = append(fields, agent.Name)
			}
		}
		return presenceHit{text: strings.Join(fields, " "), status: status}, true
	}
	return presenceHit{}, false
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
	if decoded.Panes == nil {
		return nil, fmt.Errorf("herdr pane.list: panes missing or null in result %q", result)
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
	if decoded.Workspaces == nil {
		return nil, fmt.Errorf("herdr workspace.list: workspaces missing or null in result %q", result)
	}
	labels := make(map[string]string, len(decoded.Workspaces))
	for _, workspace := range decoded.Workspaces {
		labels[workspace.WorkspaceID] = workspace.Label
	}
	return labels, nil
}
