package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

// The schedule scheduler: `pfm internal schedule-fire` every 5 minutes, a
// systemd timer + oneshot service on Linux and one launchd agent on macOS. The
// tick is fixed, so unlike name-sync's no asset carries an install-time marker.
const (
	scheduleServiceUnit  = "pfm-schedule.service"
	scheduleTimerUnit    = "pfm-schedule.timer"
	scheduleLaunchdLabel = "com.professor.pfm.schedule"
	scheduleLaunchdAsset = "launchd/" + scheduleLaunchdLabel + ".plist"
	// retiredScheduleLaunchdLabel is the agent the schedule agent was renamed
	// from; install and uninstall unload and remove it.
	retiredScheduleLaunchdLabel = "com.professor.pfm.reminder"
)

// ScheduleTimerDrift compares installed scheduler files with the install assets.
func ScheduleTimerDrift(home string, required ...bool) (path string, drifted bool, err error) {
	if home == "" {
		return "", false, fmt.Errorf("schedule timer drift: no home directory")
	}
	assets := []string{"systemd/" + scheduleTimerUnit, "systemd/" + scheduleServiceUnit}
	if schedulerIsLaunchd {
		assets = []string{scheduleLaunchdAsset}
	}
	installed := len(required) > 0 && required[0]
	for _, name := range assets {
		path := filepath.Join(home, ".config", "systemd", "user", filepath.Base(name))
		if schedulerIsLaunchd {
			path = scheduleLaunchAgentFile(home)
		}
		if _, statErr := os.Lstat(path); !errors.Is(statErr, fs.ErrNotExist) {
			installed = true
		}
	}
	for _, name := range assets {
		path := filepath.Join(home, ".config", "systemd", "user", filepath.Base(name))
		if schedulerIsLaunchd {
			path = scheduleLaunchAgentFile(home)
		}
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			if installed {
				return path, true, nil
			}
			continue
		} else if err != nil {
			return "", false, fmt.Errorf("read %s: %w", path, err)
		}
		actual, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			// An installed unit link whose staged file is gone is drift, not absence.
			return path, true, nil
		}
		if err != nil {
			return "", false, fmt.Errorf("read %s: %w", path, err)
		}
		var wanted []byte
		if schedulerIsLaunchd {
			wanted, err = renderScheduleLaunchAgent(home)
		} else {
			wanted, err = readAsset(name)
			if err != nil {
				return "", false, fmt.Errorf("read embedded asset %s: %w", name, err)
			}
			wanted, err = renderServicePath(wanted, home)
		}
		if err != nil {
			return "", false, fmt.Errorf("render embedded asset %s: %w", name, err)
		}
		if !bytes.Equal(actual, wanted) {
			return path, true, nil
		}
	}
	return "", false, nil
}

// scheduleLaunchAgentPath returns where macOS expects the schedule agent.
func (installer *engine) scheduleLaunchAgentPath() string {
	return scheduleLaunchAgentFile(installer.options.Home)
}

func scheduleLaunchAgentFile(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", scheduleLaunchdLabel+".plist")
}

func renderScheduleLaunchAgent(home string) ([]byte, error) {
	template, err := readAsset(scheduleLaunchdAsset)
	if err != nil {
		return nil, fmt.Errorf("read embedded schedule launch agent: %w", err)
	}
	wanted, err := renderLaunchAgentTemplate(template, home)
	if err == nil {
		wanted, err = renderServicePath(wanted, home)
	}
	if err != nil {
		return nil, fmt.Errorf("render schedule launch agent: %w", err)
	}
	return wanted, nil
}

// wireScheduleLaunchAgent installs the macOS half of the schedule scheduler. Like
// wireLaunchAgent it writes a REAL FILE: launchd silently refuses a symlinked
// plist.
func (installer *engine) wireScheduleLaunchAgent(ctx context.Context) error {
	if err := installer.unwireScheduleLaunchAgentLabel(ctx, retiredScheduleLaunchdLabel); err != nil {
		return err
	}
	path := installer.scheduleLaunchAgentPath()
	installer.say("launchd schedule agent -> %s", path)
	if err := installer.ensureAgentLauncher(); err != nil {
		return err
	}

	wanted, err := renderScheduleLaunchAgent(installer.options.Home)
	if err != nil {
		return err
	}
	plistChanged := false
	if sameFile(path, wanted, 0o644) {
		installer.ok(path)
	} else {
		if err := installer.change("write "+path, func() error {
			if _, statErr := os.Lstat(path); statErr == nil {
				if err := copyBackup(path, availableBackup(path, installer.stamp)); err != nil {
					return fmt.Errorf("back up schedule launch agent %s: %w", path, err)
				}
			}
			return atomicfile.Write(path, wanted, 0o644)
		}); err != nil {
			return fmt.Errorf("write schedule launch agent %s: %w", path, err)
		}
		plistChanged = true
	}
	if !installer.apply {
		return nil
	}
	return installer.reloadLaunchAgentWithLabel(ctx, path, scheduleLaunchdLabel, plistChanged)
}

// unwireScheduleLaunchAgent unloads and removes the schedule agent and the
// agent it was renamed from.
func (installer *engine) unwireScheduleLaunchAgent(ctx context.Context) error {
	for _, label := range []string{scheduleLaunchdLabel, retiredScheduleLaunchdLabel} {
		if err := installer.unwireScheduleLaunchAgentLabel(ctx, label); err != nil {
			return err
		}
	}
	return nil
}

// unwireScheduleLaunchAgentLabel unloads and removes one schedule agent plist.
func (installer *engine) unwireScheduleLaunchAgentLabel(ctx context.Context, label string) error {
	path := filepath.Join(installer.options.Home, "Library", "LaunchAgents", label+".plist")
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat schedule launch agent %s: %w", path, err)
	}
	if installer.apply {
		domain := "gui/" + strconv.Itoa(os.Getuid())
		_ = installer.options.Runner.Run(ctx, "launchctl", "bootout", domain+"/"+label)
	}
	if err := installer.change("remove "+path, func() error { return os.Remove(path) }); err != nil {
		return fmt.Errorf("remove schedule launch agent %s: %w", path, err)
	}
	return nil
}
