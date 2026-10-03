package installer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

// The reminder scheduler: `pfm internal reminder-fire` every 5 minutes, a
// systemd timer + oneshot service on Linux and one launchd agent on macOS. The
// tick is fixed, so unlike name-sync's no asset carries an install-time marker.
const (
	reminderServiceUnit  = "pfm-reminder.service"
	reminderTimerUnit    = "pfm-reminder.timer"
	reminderLaunchdLabel = "com.professor.pfm.reminder"
	reminderLaunchdAsset = "launchd/" + reminderLaunchdLabel + ".plist"
)

// reminderLaunchAgentPath returns where macOS expects the reminder agent.
func (installer *engine) reminderLaunchAgentPath() string {
	return filepath.Join(
		installer.options.Home, "Library", "LaunchAgents", reminderLaunchdLabel+".plist",
	)
}

// wireReminderLaunchAgent installs the macOS half of the reminder scheduler. Like
// wireLaunchAgent it writes a REAL FILE: launchd silently refuses a symlinked
// plist.
func (installer *engine) wireReminderLaunchAgent(ctx context.Context) error {
	path := installer.reminderLaunchAgentPath()
	installer.say("launchd reminder agent -> %s", path)

	template, err := readAsset(reminderLaunchdAsset)
	if err != nil {
		return fmt.Errorf("read embedded reminder launch agent: %w", err)
	}
	wanted, err := renderServicePath(
		[]byte(strings.ReplaceAll(string(template), "__PFM_HOME__", installer.options.Home)),
		installer.options.Home,
	)
	if err != nil {
		return fmt.Errorf("render reminder launch agent: %w", err)
	}
	plistChanged := false
	if sameFile(path, wanted, 0o644) {
		installer.ok(path)
	} else {
		if err := installer.change("write "+path, func() error {
			if _, statErr := os.Lstat(path); statErr == nil {
				if err := copyBackup(path, availableBackup(path, installer.stamp)); err != nil {
					return fmt.Errorf("back up reminder launch agent %s: %w", path, err)
				}
			}
			return atomicfile.Write(path, wanted, 0o644)
		}); err != nil {
			return fmt.Errorf("write reminder launch agent %s: %w", path, err)
		}
		plistChanged = true
	}
	if !installer.apply {
		return nil
	}
	return installer.reloadLaunchAgentWithLabel(ctx, path, reminderLaunchdLabel, plistChanged)
}

// unwireReminderLaunchAgent unloads and removes the reminder agent.
func (installer *engine) unwireReminderLaunchAgent(ctx context.Context) error {
	path := installer.reminderLaunchAgentPath()
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat reminder launch agent %s: %w", path, err)
	}
	if installer.apply {
		domain := "gui/" + strconv.Itoa(os.Getuid())
		_ = installer.options.Runner.Run(ctx, "launchctl", "bootout", domain+"/"+reminderLaunchdLabel)
	}
	if err := installer.change("remove "+path, func() error { return os.Remove(path) }); err != nil {
		return fmt.Errorf("remove reminder launch agent %s: %w", path, err)
	}
	return nil
}
