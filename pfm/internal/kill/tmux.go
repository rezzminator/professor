package kill

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/gather"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// TmuxKiller invokes tmux only through an explicit socket pathname.
type TmuxKiller struct {
	Binary string
}

// PaneExists asks the one tmux-server probe (gather.ProbeServer) whether paneID
// is still live. Only tmux's word that the server is dead is "gone" — the
// ordinary shape of the pane's last server closing behind it, the exact moment
// a graceful /exit succeeds. A probe that could not run, a server that did not
// answer within the probe's bound, or any other failure is an error, never
// "false": the kill-exit grace window must not read it as an exit.
func (tmux TmuxKiller) PaneExists(
	ctx context.Context,
	socketPath, paneID string,
) (bool, error) {
	state, panes, err := gather.ProbeServer(ctx, tmux.Binary, socketPath)
	switch state {
	case gather.ServerDead:
		return false, nil
	case gather.ServerAlive:
		return slices.ContainsFunc(panes, func(pane gather.ProbePane) bool { return pane.PaneID == paneID }), nil
	}
	return false, err
}

// KillServerIfSolePane evaluates every session/window's pane IDs inside tmux's
// synchronous command queue. No client-side snapshot can become stale before
// kill-server. Linked windows may repeat a pane ID; that conservatively refuses.
func (tmux TmuxKiller) KillServerIfSolePane(ctx context.Context, socketPath, paneID string) error {
	id, err := strconv.Atoi(strings.TrimPrefix(paneID, "%"))
	if err != nil || id < 0 || !strings.HasPrefix(paneID, "%") {
		return fmt.Errorf("invalid pane ID for exclusive server kill: %q", paneID)
	}
	condition := "#{==:#{S:#{W:#{P:#{pane_id}}}}," + paneID + "}"
	_, err = tmux.command(ctx, socketPath, "if-shell", "-F", "-t", paneID, condition, "kill-server").Output()
	if err != nil {
		return fmt.Errorf("kill exclusive server for pane %s on %s: %w: %s", paneID, socketPath, err, tmuxReason(err))
	}
	return nil
}

// tmuxReason is what a failed tmux wrote to stderr, trimmed, for the error text.
func tmuxReason(err error) string {
	stderr, _ := pfmtmux.ExitStderr(err)
	return strings.TrimSpace(string(stderr))
}

func (tmux TmuxKiller) KillPane(
	ctx context.Context,
	socketPath, paneID string,
) error {
	_, err := tmux.command(ctx, socketPath, "kill-pane", "-t", paneID).Output()
	if err != nil {
		return fmt.Errorf("kill pane %s on %s: %w: %s", paneID, socketPath, err, tmuxReason(err))
	}
	return nil
}

// ClientTTYs lists the terminals attached to this server. A chat's clients ARE
// its viewports — the panes a person is watching it through.
func (tmux TmuxKiller) ClientTTYs(
	ctx context.Context,
	socketPath string,
) ([]string, error) {
	output, err := tmux.command(
		ctx, socketPath, "list-clients", "-F", "#{client_tty}",
	).Output()
	if err != nil {
		return nil, err
	}
	var ttys []string
	for _, line := range strings.Split(string(output), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			ttys = append(ttys, line)
		}
	}
	return ttys, nil
}

// PanesByTTY maps each pane on this server to the terminal it runs in — the
// join that traces a viewport client back to the pane hosting it — read
// through the one bounded pane read (gather.ProbePanes): a hung bunker is
// ErrServerUnresponsive, never "no viewports".
func (tmux TmuxKiller) PanesByTTY(
	ctx context.Context,
	socketPath string,
) (map[string]string, error) {
	listed, err := gather.ProbePanes(ctx, tmux.Binary, socketPath)
	if err != nil {
		return nil, err
	}
	panes := make(map[string]string)
	for index := range listed {
		pane := &listed[index]
		if pane.TTY != "" && pane.PaneID != "" {
			panes[pane.TTY] = pane.PaneID
		}
	}
	return panes, nil
}

func (tmux TmuxKiller) KillServer(
	ctx context.Context,
	socketPath string,
) error {
	_, err := tmux.command(ctx, socketPath, "kill-server").Output()
	if err != nil {
		return fmt.Errorf("kill server on %s: %w: %s", socketPath, err, tmuxReason(err))
	}
	return nil
}

// socket is the one tmux-addressing wrapper (internal/tmux.Socket): Dir
// stays empty because every TmuxKiller caller already holds a full
// socketPath, not a bare socket name.
func (tmux TmuxKiller) socket() pfmtmux.Socket {
	return pfmtmux.Socket{Binary: tmux.Binary}
}

func (tmux TmuxKiller) command(
	ctx context.Context,
	socketPath string,
	arguments ...string,
) *pfmtmux.Cmd {
	return tmux.socket().Command(ctx, socketPath, arguments...)
}

// Client is one `tmux attach` process on the server: its pid, and the name
// tmux addresses it by (its tty, or client-{pid} for a client with none).
type Client struct {
	PID  int
	Name string
}

// ViewportTmux names the two tmux calls the closer makes: list the attached
// clients, and detach one.
type ViewportTmux interface {
	Clients(ctx context.Context, socketPath string) (clients []Client, skipped []string, err error)
	DetachClient(ctx context.Context, socketPath, name string) error
}

// Clients lists the `tmux attach` processes currently attached to this
// server — one per terminal the chat is being watched through.
func (tmux TmuxKiller) Clients(
	ctx context.Context,
	socketPath string,
) (clients []Client, skipped []string, err error) {
	output, err := tmux.command(
		ctx, socketPath, "list-clients", "-F", "#{client_pid} #{client_name}",
	).Output()
	if err != nil {
		return nil, nil, fmt.Errorf("list clients on %s: %w", socketPath, err)
	}
	clients, skipped = parseClients(string(output))
	return clients, skipped, nil
}

// DetachClient detaches the client tmux names name: the client restores the
// screen it drew on and exits.
func (tmux TmuxKiller) DetachClient(ctx context.Context, socketPath, name string) error {
	if _, err := tmux.command(ctx, socketPath, "detach-client", "-t", name).Output(); err != nil {
		return fmt.Errorf("detach client %s on %s: %w: %s", name, socketPath, err, tmuxReason(err))
	}
	return nil
}

// parseClients reads one `{pid} {name}` client per line; a line that is not
// one is named in skipped and costs only that client, never its siblings.
func parseClients(output string) (clients []Client, skipped []string) {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pidText, name, _ := strings.Cut(line, " ")
		pid, convErr := strconv.Atoi(pidText)
		switch {
		case convErr != nil:
			skipped = append(skipped, fmt.Sprintf("client pid %q unreadable: %v", pidText, convErr))
		case name == "":
			skipped = append(skipped, fmt.Sprintf("client %d: tmux listed it with no name to detach it by", pid))
		default:
			clients = append(clients, Client{PID: pid, Name: name})
		}
	}
	return clients, skipped
}
