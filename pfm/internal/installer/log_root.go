package installer

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// generationName is a log file of the rotation pfm used before segments:
// {name}.jsonl or one of its numbered generations {name}.jsonl.{n}.
var generationName = regexp.MustCompile(`^(.+\.jsonl)(\.\d+)?$`)

// moveLogsToRoot moves every log file the home's state folder still holds into
// the log root (paths.LogRoot) once, the run the root first differs from it:
// the activity log and its generations, and the sub-agent trace's, become
// closed segments named by their last write; a .lock sidecar is dropped; any
// other log is appended to its namesake in the root (renamed when there is
// none); the restart folder's logs go to the root's restart folder. The
// emptied folders go too, and this process's own log follows to the root.
// A home logging in its state folder has nothing to move.
func (installer *engine) moveLogsToRoot() error {
	env := installer.options.Env
	if env == nil {
		env = paths.OSEnv{}
	}
	home := installer.options.Home
	place := paths.LogRoot(env, home)
	moves := []struct{ from, to string }{
		{paths.StateLogDir(home), place.Dir},
		{filepath.Join(filepath.Dir(paths.DefaultStateDB(home)), paths.RestartLogDirName), place.Restart()},
	}
	if place.Dir == moves[0].from {
		return nil
	}
	var failures []error
	for _, move := range moves {
		entries, err := os.ReadDir(move.from)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("read log folder %s: %w", move.from, err))
			continue
		}
		message := fmt.Sprintf("move %d log files from %s to %s", len(entries), move.from, move.to)
		failures = append(failures, installer.change(message, func() error {
			return moveLogFolder(move.from, move.to, entries)
		}))
	}
	if installer.apply {
		failures = append(failures, obs.RelocateLog(place.Activity()))
	}
	return errors.Join(failures...)
}

// moveLogFolder moves each of entries out of from into to, then removes from.
func moveLogFolder(from, to string, entries []fs.DirEntry) error {
	if err := os.MkdirAll(to, 0o700); err != nil {
		return fmt.Errorf("create log root %s: %w", to, err)
	}
	for _, entry := range entries {
		source := filepath.Join(from, entry.Name())
		if strings.HasSuffix(entry.Name(), ".lock") {
			if err := os.Remove(source); err != nil {
				return fmt.Errorf("drop log lock %s: %w", source, err)
			}
			continue
		}
		target := filepath.Join(to, entry.Name())
		if match := generationName.FindStringSubmatch(entry.Name()); match != nil {
			info, err := entry.Info()
			if err != nil {
				return fmt.Errorf("inspect log %s: %w", source, err)
			}
			if target, err = obs.SegmentPath(filepath.Join(to, match[1]), info.ModTime()); err != nil {
				return err
			}
		}
		if err := moveLogFile(source, target); err != nil {
			return err
		}
	}
	if err := os.Remove(from); err != nil {
		return fmt.Errorf("remove emptied log folder %s: %w", from, err)
	}
	return nil
}

// moveLogFile renames source to target when target is free and on the same
// device; otherwise it appends source's bytes to target and removes source,
// so a log that both places hold keeps every line.
func moveLogFile(source, target string) error {
	if _, err := os.Lstat(target); errors.Is(err, fs.ErrNotExist) {
		err := os.Rename(source, target)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EXDEV) {
			return fmt.Errorf("move log %s to %s: %w", source, target, err)
		}
	} else if err != nil {
		return fmt.Errorf("inspect log %s: %w", target, err)
	}
	from, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("read log %s: %w", source, err)
	}
	defer from.Close() //nolint:errcheck // read-only: nothing is lost by a failed close
	to, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open log %s: %w", target, err)
	}
	_, copyErr := io.Copy(to, from)
	if err := errors.Join(copyErr, to.Close()); err != nil {
		return fmt.Errorf("append log %s to %s: %w", source, target, err)
	}
	if err := os.Remove(source); err != nil {
		return fmt.Errorf("remove moved log %s: %w", source, err)
	}
	return nil
}
