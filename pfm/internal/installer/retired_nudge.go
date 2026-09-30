package installer

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// removeRetiredNudgeState removes only regular files the retired hook wrote
// directly in the SID directory. A failed file removal is visible and retryable.
func (installer *engine) removeRetiredNudgeState() error {
	return installer.removeRetiredNudgeStateWith(os.ReadDir, os.Remove)
}

func (installer *engine) removeRetiredNudgeStateWith(
	readDir func(string) ([]fs.DirEntry, error), remove func(string) error,
) error {
	env := installer.options.Env
	if env == nil {
		env = paths.OSEnv{}
	}
	sidDir := paths.EnvOrFrom(env, paths.EnvSIDDir, "/tmp/cc-sid")
	entries, err := readDir(sidDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		installer.warnRetiredNudge(sidDir, err)
		return nil
	}
	var targets []string
	for _, entry := range entries {
		name := entry.Name()
		if (strings.HasPrefix(name, "nudge-ctx-") || strings.HasPrefix(name, "nudge-band-")) &&
			entry.Type().IsRegular() {
			targets = append(targets, filepath.Join(sidDir, name))
		}
	}
	if len(targets) == 0 {
		return nil
	}
	message := "remove retired compact-nudge state from " + sidDir
	return installer.changePaths(message, targets, func() error {
		for _, path := range targets {
			if err := remove(path); err != nil {
				installer.warnRetiredNudge(path, err)
			}
		}
		return nil
	})
}

func (installer *engine) warnRetiredNudge(path string, err error) {
	installer.say("  warn    %s: %v", path, err)
	ctx := obs.Component(context.Background(), comp)
	obs.Logger(ctx).LogAttrs(ctx, slog.LevelWarn, "installer.retired_nudge",
		slog.String("path", path), slog.String(obs.FieldErr, err.Error()))
}
