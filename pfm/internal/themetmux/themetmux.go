// Package themetmux adapts the tmux probe to themesync.TmuxServers: the live
// pfm chat servers in one tmux socket directory, one global option read, and
// one option-vector apply.
package themetmux

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/themesync"
)

// Probe is the slice of gather.TmuxProbe the adapter uses; tests inject a fake.
type Probe interface {
	ServerPID(ctx context.Context, socket string) (int, error)
	ShowGlobalOption(ctx context.Context, socket, name string) (string, error)
	ApplyGlobalOptions(ctx context.Context, socket string, options [][]string) error
}

// Servers is the themesync.TmuxServers over one tmux socket directory.
type Servers struct {
	Probe Probe
	// TmuxDir is the directory holding the chat sockets (paths.Env.TmuxDir).
	TmuxDir string
	// entryInfo stats one listed entry; nil is the entry's own Info. Tests set
	// it to produce a stat fault a privileged user's filesystem never raises.
	entryInfo func(fs.DirEntry) (fs.FileInfo, error)
}

var _ themesync.TmuxServers = Servers{}

// AtDir reaches the chat servers in tmuxDir through a real tmux: the probe's
// TMUX_TMPDIR is the directory tmuxDir sits in, so `-L {socket}` resolves to
// tmuxDir/{socket}.
func AtDir(tmuxDir string) Servers {
	return Servers{
		Probe:   gather.TmuxProbe{TmuxTmpDir: filepath.Dir(tmuxDir)},
		TmuxDir: tmuxDir,
	}
}

// Sockets lists the live chat servers, sorted. A socket file whose server is
// gone (a chat that ended) is skipped; a socket whose liveness could not be
// read stays in the list and its error is returned beside it; a socket that
// could not be stat-ed (other than vanishing mid-scan) is left out and its error
// returned the same way, so the caller reports the fault instead of reading "no
// server" from a failed look. A
// missing socket directory is no servers and no error.
func (servers Servers) Sockets(ctx context.Context) ([]string, error) {
	entries, err := os.ReadDir(servers.TmuxDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read tmux socket directory %s: %w", servers.TmuxDir, err)
	}
	names := make([]string, 0, len(entries))
	var faults []error
	for _, entry := range entries {
		name := entry.Name()
		if !gather.IsChatSocketName(name) {
			continue
		}
		stat := fs.DirEntry.Info
		if servers.entryInfo != nil {
			stat = servers.entryInfo
		}
		info, err := stat(entry)
		if errors.Is(err, fs.ErrNotExist) {
			continue // the socket vanished between the listing and the stat
		}
		if err != nil {
			faults = append(faults, fmt.Errorf("stat tmux socket %s: %w", filepath.Join(servers.TmuxDir, name), err))
			continue
		}
		if info.Mode()&os.ModeSocket == 0 {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	live := make([]string, 0, len(names))
	for _, name := range names {
		if _, err := servers.Probe.ServerPID(ctx, name); err != nil {
			if errors.Is(err, gather.ErrServerGone) {
				continue
			}
			faults = append(faults, err)
		}
		live = append(live, name)
	}
	return live, errors.Join(faults...)
}

// Show reads one global option of socket's server; tmux's answer that the
// option is unset wraps themesync.ErrTmuxOptionUnset.
func (servers Servers) Show(ctx context.Context, socket, name string) (string, error) {
	value, err := servers.Probe.ShowGlobalOption(ctx, socket, name)
	if errors.Is(err, gather.ErrOptionUnset) {
		return "", fmt.Errorf("%w: %w", themesync.ErrTmuxOptionUnset, err)
	}
	return value, err
}

// Apply runs each set-option vector against socket's server.
func (servers Servers) Apply(ctx context.Context, socket string, options [][]string) error {
	return servers.Probe.ApplyGlobalOptions(ctx, socket, options)
}
