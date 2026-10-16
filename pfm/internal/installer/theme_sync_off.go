package installer

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/themesync"
	"github.com/rezzminator/professor/pfm/internal/themetmux"
)

// themeSyncOff runs `pfm theme off` before an uninstall while the theme sync
// is on or an earlier off left a surface snapshot unrestored: every surface
// goes back to its snapshot, the appearance follower is booted out and the
// state disabled, so no surface stays themed by a pfm that is gone. A failed
// off stops the uninstall, and the rerun retries it.
func (installer *engine) themeSyncOff(ctx context.Context) error {
	home := installer.options.Home
	if !themesync.RestorePending(themesync.DefaultStateDir(home)) {
		return nil
	}
	if !installer.apply {
		installer.say("  plan    would run pfm theme off: the theme sync is on")
		return nil
	}
	off := installer.options.themeOff
	if off == nil {
		off = themeSyncOffReal
	}
	if err := off(ctx, home); err != nil {
		return fmt.Errorf("uninstall: pfm theme off: %w", err)
	}
	installer.ok("pfm theme off: every themed surface restored and the follower booted out")
	return nil
}

// themeSyncOffReal is themesync.Off over the real runner and pfm's tmux
// servers; a surface it could not restore is its error.
func themeSyncOffReal(ctx context.Context, home string) error {
	values, err := paths.Resolve()
	if err != nil {
		return fmt.Errorf("resolve the tmux directory: %w", err)
	}
	report, err := themesync.Off(ctx, themesync.Env{
		Home:      home,
		StateDir:  themesync.DefaultStateDir(home),
		PfmBinary: filepath.Join(home, ".local", "bin", "pfm"),
		Runner:    obs.Runner(deps.RealRunner{}),
		Tmux:      themetmux.AtDir(values.TmuxDir),
	})
	if err != nil {
		return err
	}
	var failed []string
	for _, surface := range report.Surfaces {
		if surface.Verdict == themesync.VerdictError {
			failed = append(failed, surface.Surface+": "+surface.Detail)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("surfaces not restored: %s", strings.Join(failed, "; "))
	}
	return nil
}
