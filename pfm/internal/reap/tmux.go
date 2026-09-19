package reap

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"hostops/pfm/internal/gather"
	pfmtmux "hostops/pfm/internal/tmux"
)

// Tmux is the reaper's whole tmux surface.
type Tmux interface {
	ListPanes(ctx context.Context, socket string) ([]gather.ProbePane, error)
	Sessions(ctx context.Context, socket string) ([]VSCTSession, error)
	KillSession(ctx context.Context, socket, session string) error
	// ClientIdle answers exemption 1 — how long ago #{client_activity} last
	// moved for the most recently active client attached to socket. found is
	// false when no client answered at all, which the caller must treat as
	// "cannot confirm" and default toward NOT reaping, never toward
	// "definitely idle": Attached already means a client is supposed to be
	// there, so a probe that comes back empty is an anomaly, not a fact.
	ClientIdle(ctx context.Context, socket string) (idle time.Duration, found bool, err error)
}

// TmuxReaper talks to tmux inside one socket directory and nowhere else, so a
// jail's TMUX_TMPDIR is the whole world a test run can reach.
type TmuxReaper struct {
	Binary  string
	TmuxDir string
	Now     func() time.Time
}

// ListPanes reads one socket's panes through the same probe the picker uses,
// so both halves of the fleet see one shape of a chat (K3).
func (tmux TmuxReaper) ListPanes(
	ctx context.Context,
	socket string,
) ([]gather.ProbePane, error) {
	return gather.TmuxProbe{
		Binary:     tmux.Binary,
		TmuxTmpDir: filepath.Dir(tmux.TmuxDir),
	}.ListPanes(ctx, socket)
}

// Sessions lists the sessions on a SHARED socket — the vsct bunker, where
// plain terminals live many-to-one rather than one server per chat.
func (tmux TmuxReaper) Sessions(
	ctx context.Context,
	socket string,
) ([]VSCTSession, error) {
	format := strings.Join([]string{
		"#{session_name}",
		"#{?session_attached,1,0}",
		"#{session_activity}",
	}, "\x1f")
	output, err := tmux.command(
		ctx,
		socket,
		"list-sessions",
		"-F",
		format,
	).Output()
	if err != nil {
		if pfmtmux.CouldNotRun(err) {
			// tmux itself never started (missing binary, bad configured
			// path): the sweep could not look, so it must not report the
			// bunker as empty.
			return nil, fmt.Errorf("list bunker sessions on %s: %w", socket, err)
		}
		// tmux ran and answered no server on this socket — the ordinary
		// state on a machine that never opened a bunker. Not a sweep
		// failure.
		return nil, nil
	}
	now := tmux.Now
	if now == nil {
		now = time.Now
	}
	sessions := make([]VSCTSession, 0)
	for _, line := range strings.Split(strings.TrimSuffix(string(output), "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := pfmtmux.FormatSplit(line, 3)
		if len(fields) != 3 {
			return nil, fmt.Errorf(
				"tmux %s returned %d session fields in %q",
				socket,
				len(fields),
				line,
			)
		}
		activity, err := strconv.ParseInt(fields[2], 10, 64)
		if err != nil {
			return nil, fmt.Errorf(
				"tmux %s session activity %q: %w",
				socket,
				fields[2],
				err,
			)
		}
		idle := now().Sub(time.Unix(activity, 0))
		if idle < 0 {
			idle = 0
		}
		sessions = append(sessions, VSCTSession{
			Name:     fields[0],
			Attached: fields[1] == "1",
			Idle:     idle,
		})
	}
	return sessions, nil
}

// ClientIdle reads #{client_activity} for every client attached to socket
// and returns how long ago the MOST recently active one moved — the freshest
// client is the one that decides whether a chat is "open for the operator".
func (tmux TmuxReaper) ClientIdle(
	ctx context.Context,
	socket string,
) (time.Duration, bool, error) {
	output, err := tmux.command(ctx, socket, "list-clients", "-F", "#{client_activity}").Output()
	if err != nil {
		// tmux errors "no clients attached" identically to any other reason
		// list-clients found nothing — that is the ordinary shape of a
		// socket nobody currently has open, not a probe failure.
		return 0, false, nil
	}
	now := tmux.Now
	if now == nil {
		now = time.Now
	}
	newest := int64(-1)
	for _, line := range strings.Split(strings.TrimSuffix(string(output), "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		activity, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			return 0, false, fmt.Errorf(
				"tmux %s client activity %q: %w",
				socket,
				line,
				err,
			)
		}
		if activity > newest {
			newest = activity
		}
	}
	if newest < 0 {
		return 0, false, nil
	}
	idle := now().Sub(time.Unix(newest, 0))
	if idle < 0 {
		idle = 0
	}
	return idle, true, nil
}

// KillSession ends one session on a shared socket, leaving its neighbours up.
func (tmux TmuxReaper) KillSession(
	ctx context.Context,
	socket, session string,
) error {
	output, err := tmux.command(
		ctx,
		socket,
		"kill-session",
		"-t",
		"="+session,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"kill tmux session %s on %s: %w: %s",
			session,
			socket,
			err,
			strings.TrimSpace(string(output)),
		)
	}
	return nil
}

// socket is the one tmux-addressing wrapper (internal/tmux.Socket).
func (tmux TmuxReaper) socket() pfmtmux.Socket {
	return pfmtmux.Socket{Binary: tmux.Binary, Dir: tmux.TmuxDir}
}

func (tmux TmuxReaper) command(
	ctx context.Context,
	socket string,
	arguments ...string,
) *pfmtmux.Cmd {
	return tmux.socket().Command(ctx, socket, arguments...)
}
