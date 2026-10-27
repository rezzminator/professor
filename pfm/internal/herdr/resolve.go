package herdr

import (
	"context"
	"encoding/json"
	"fmt"
)

// Location is the Herdr pane a process runs in: the Herdr socket, the pane id
// (a move assigns a new one) and the terminal id (a move keeps it).
type Location struct {
	Socket   string
	Pane     string
	Terminal string
}

// Resolver locates the Herdr pane whose foreground holds a process — the one
// rule the reporter and presence share for "which pane is this tmux client
// in". The client's own environment says whether it runs inside Herdr and on
// which socket; its HERDR_PANE_ID is only the pane asked first, as it is the
// pane the client's shell started in and goes stale after a Herdr restart or
// a pane move. The truth is the pane whose pane.process_info foreground holds
// the client pid.
type Resolver struct {
	// Environ reads another process's environment (gather's ProcFS.Environ).
	Environ func(pid int) (map[string]string, error)
	// Panes lists every pane of a Herdr socket (pane.list); a caller passes
	// its own cached listing.
	Panes func(ctx context.Context, socket string) ([]Pane, error)
	// Foreground returns the foreground pids of one pane (pane.process_info).
	Foreground func(ctx context.Context, socket, pane string) ([]int, error)
}

// WireResolver is a Resolver over a live Herdr socket: call is Call's seam and
// seq numbers each request.
func WireResolver(
	environ func(pid int) (map[string]string, error),
	call func(ctx context.Context, socket string, lines ...[]byte) ([]json.RawMessage, error),
	seq func() uint64,
) Resolver {
	return Resolver{
		Environ: environ,
		Panes: func(ctx context.Context, socket string) ([]Pane, error) {
			line, err := PaneList{Seq: seq()}.Line()
			if err != nil {
				return nil, err
			}
			results, err := call(ctx, socket, line)
			if err != nil {
				return nil, err
			}
			return ParsePanes(results[0])
		},
		Foreground: func(ctx context.Context, socket, pane string) ([]int, error) {
			line, err := ProcessInfo{Pane: pane, Seq: seq()}.Line()
			if err != nil {
				return nil, err
			}
			results, err := call(ctx, socket, line)
			if err != nil {
				return nil, err
			}
			return ParseProcessInfo(results[0])
		},
	}
}

// Resolve returns the pane process pid runs in. A process whose environment
// holds no HERDR_ENV=1 runs outside Herdr: not found, no error, and no Herdr
// call is made. Inside Herdr, the pane its HERDR_PANE_ID names is asked first,
// then every listed pane; a process no pane holds is an error naming the pid
// and the socket — never "outside Herdr".
func (resolver Resolver) Resolve(ctx context.Context, pid int) (Location, bool, error) {
	if resolver.Environ == nil || resolver.Panes == nil || resolver.Foreground == nil {
		return Location{}, false, fmt.Errorf("resolve the Herdr pane of client %d: the resolver has a nil seam", pid)
	}
	environment, err := resolver.Environ(pid)
	if err != nil {
		return Location{}, false, fmt.Errorf("read the environment of client %d: %w", pid, err)
	}
	if environment[EnvMarker] != "1" {
		return Location{}, false, nil
	}
	socket := environment[EnvSocket]
	if socket == "" {
		return Location{}, false, fmt.Errorf("client %d: %s=1 without %s", pid, EnvMarker, EnvSocket)
	}
	panes, err := resolver.Panes(ctx, socket)
	if err != nil {
		return Location{}, false, fmt.Errorf("list the panes of %s: %w", socket, err)
	}
	pane, found, err := resolver.PaneRunning(ctx, socket, pid, panes, environment[EnvPane])
	if err != nil {
		return Location{}, false, err
	}
	if !found {
		return Location{}, false, fmt.Errorf(
			"client %d runs in no pane of %s; nested tmux is unsupported: "+
				"Herdr exposes only the outer foreground process group; attach the pfm client directly in Herdr",
			pid, socket,
		)
	}
	return Location{Socket: socket, Pane: pane.PaneID, Terminal: pane.TerminalID}, true, nil
}

// PaneRunning returns the listed pane whose foreground holds pid, asking about
// the pane first names before the others. A pane whose process info cannot be
// read is skipped (it may have closed since the listing); when no pane holds
// the pid and any read failed, the result is an error naming the failures, as
// a Herdr that answered nothing is not "no pane runs it".
func (resolver Resolver) PaneRunning(
	ctx context.Context,
	socket string,
	pid int,
	panes []Pane,
	first string,
) (Pane, bool, error) {
	if resolver.Foreground == nil {
		return Pane{}, false, fmt.Errorf("find the pane of client %d: the resolver has no process-info seam", pid)
	}
	ordered := make([]Pane, 0, len(panes))
	for _, pane := range panes {
		if pane.PaneID == first {
			ordered = append([]Pane{pane}, ordered...)
		} else {
			ordered = append(ordered, pane)
		}
	}
	failed := 0
	var firstFailure error
	for _, pane := range ordered {
		pids, err := resolver.Foreground(ctx, socket, pane.PaneID)
		if err != nil {
			failed++
			if firstFailure == nil {
				firstFailure = fmt.Errorf("%s: %w", pane.PaneID, err)
			}
			continue
		}
		for _, foreground := range pids {
			if foreground == pid {
				return pane, true, nil
			}
		}
	}
	if failed > 0 {
		return Pane{}, false, fmt.Errorf(
			"client %d runs in no pane of %s (process info failed for %d of %d panes: %w)",
			pid, socket, failed, len(ordered), firstFailure,
		)
	}
	return Pane{}, false, nil
}
