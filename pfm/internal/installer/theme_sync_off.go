package installer

import (
	"context"
	"errors"
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

// refreshThemeFollower brings the theme follower's launch agent onto the
// stable launcher while the theme sync is on: the follower's plist is written
// by `pfm theme`, not by install, so without this a plist naming pfm itself
// stays loaded until someone runs `pfm theme sync` in a shell. Install runs
// outside the follower's job, so the reload the follower refuses inside it
// goes through here. A sync that is off has no agent to move.
func (installer *engine) refreshThemeFollower(ctx context.Context) error {
	home := installer.options.Home
	if !themesync.SyncOn(themesync.DefaultStateDir(home)) {
		return nil
	}
	if !installer.apply {
		installer.say("  plan    would refresh the theme follower's launch agent: the theme sync is on")
		return nil
	}
	refresh := installer.options.themeFollowerRefresh
	if refresh == nil {
		refresh = themeFollowerRefreshReal
	}
	err := refresh(ctx, home)
	if errors.Is(err, errThemeSyncInert) {
		installer.skip("theme follower launch agent: the theme sync turned off before install reached it")
		return nil
	}
	if err != nil {
		return fmt.Errorf("refresh the theme follower launch agent: %w", err)
	}
	installer.ok("theme follower launch agent runs the stable launcher")
	return nil
}

// errThemeSyncInert is a refresh whose sync touched nothing: the state turned
// off between install's check and the run, so no follower agent exists.
var errThemeSyncInert = errors.New("the theme sync ran inert: no surface was touched")

// themeFollowerOutcome reads a sync report for the follower's row: applied or
// in sync is the refresh done; any other verdict, or no row at all because the
// theme source failed to resolve before any surface ran, is its error.
func themeFollowerOutcome(report themesync.Report) error {
	if len(report.Surfaces) == 0 {
		return errThemeSyncInert
	}
	var failed []string
	for _, surface := range report.Surfaces {
		if surface.Surface == themesync.FollowerSurface {
			if surface.Verdict == themesync.VerdictApplied || surface.Verdict == themesync.VerdictInSync {
				return nil
			}
			return fmt.Errorf("%s %s: %s", surface.Surface, surface.Verdict, surface.Detail)
		}
		if surface.Verdict == themesync.VerdictError {
			failed = append(failed, surface.Surface+": "+surface.Detail)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("the sync never reached the follower: %s", strings.Join(failed, "; "))
	}
	return errors.New("the sync report carries no follower row")
}

// themeFollowerRefreshReal is themesync.Sync over the real runner and pfm's
// tmux servers, judged by themeFollowerOutcome.
func themeFollowerRefreshReal(ctx context.Context, home string) error {
	values, err := paths.Resolve()
	if err != nil {
		return fmt.Errorf("resolve the tmux directory: %w", err)
	}
	report, err := themesync.Sync(ctx, themesync.Env{
		Home:      home,
		StateDir:  themesync.DefaultStateDir(home),
		PfmBinary: filepath.Join(home, ".local", "bin", "pfm"),
		Runner:    obs.Runner(deps.RealRunner{}),
		Tmux:      themetmux.AtDir(values.TmuxDir),
	}, false)
	if err != nil {
		return err
	}
	return themeFollowerOutcome(report)
}
