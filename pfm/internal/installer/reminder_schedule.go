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

// ReminderScheduleDrift compares installed scheduler files with the install assets.
func ReminderScheduleDrift(home string) (path string, drifted bool, err error) {
	if home == "" {
		return "", false, fmt.Errorf("reminder schedule drift: no home directory")
	}
	assets := []string{"systemd/" + reminderTimerUnit, "systemd/" + reminderServiceUnit}
	if schedulerIsLaunchd {
		assets = []string{reminderLaunchdAsset}
	}
	for _, name := range assets {
		path := filepath.Join(home, ".config", "systemd", "user", filepath.Base(name))
		if schedulerIsLaunchd {
			path = reminderLaunchAgentFile(home)
		}
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
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
			wanted, err = renderReminderLaunchAgent(home)
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

// reminderLaunchAgentPath returns where macOS expects the reminder agent.
func (installer *engine) reminderLaunchAgentPath() string {
	return reminderLaunchAgentFile(installer.options.Home)
}

func reminderLaunchAgentFile(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", reminderLaunchdLabel+".plist")
}

func renderReminderLaunchAgent(home string) ([]byte, error) {
	template, err := readAsset(reminderLaunchdAsset)
	if err != nil {
		return nil, fmt.Errorf("read embedded reminder launch agent: %w", err)
	}
	wanted, err := renderServicePath([]byte(strings.ReplaceAll(string(template), "__PFM_HOME__", home)), home)
	if err != nil {
		return nil, fmt.Errorf("render reminder launch agent: %w", err)
	}
	return wanted, nil
}

// wireReminderLaunchAgent installs the macOS half of the reminder scheduler. Like
// wireLaunchAgent it writes a REAL FILE: launchd silently refuses a symlinked
// plist.
func (installer *engine) wireReminderLaunchAgent(ctx context.Context) error {
	path := installer.reminderLaunchAgentPath()
	installer.say("launchd reminder agent -> %s", path)

	wanted, err := renderReminderLaunchAgent(installer.options.Home)
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
