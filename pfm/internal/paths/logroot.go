package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// The names every pfm log file takes inside the log root (LogRoot). Each
// file keeps its own format; only its folder is shared.
const (
	// ActivityLogName is the activity log's current segment (internal/obs).
	ActivityLogName = "pfm.jsonl"
	// CostSinkLogName is the detached cost sink's failure log.
	CostSinkLogName = "cost-sink.log"
	// RestartLogDirName holds `pfm restart`'s worker logs and the logs of
	// the servers it starts.
	RestartLogDirName = "restart"
)

// LogPlace is the folder every pfm log of one home lives in. Fallback says
// why it is the home's state folder although a clone is on record (the
// clone is gone, or its tmp folder cannot be created or written); it is nil
// when Dir is the clone's, when no clone is recorded, and in a jailed home.
type LogPlace struct {
	Dir      string
	Fallback error
}

// Activity is the activity log's current segment.
func (place LogPlace) Activity() string { return filepath.Join(place.Dir, ActivityLogName) }

// CostSink is the cost sink's log.
func (place LogPlace) CostSink() string { return filepath.Join(place.Dir, CostSinkLogName) }

// Restart is the folder of `pfm restart`'s logs.
func (place LogPlace) Restart() string { return filepath.Join(place.Dir, RestartLogDirName) }

// StateLogDir is the home's own log folder: LogRoot's fallback, and where
// every pfm log lived before the log root moved under the clone (pfm install
// moves what it still finds there).
func StateLogDir(home string) string { return filepath.Join(home, ".local", "state", "pfm", "log") }

// CloneLogDir is the log root inside a Professor clone: its ignored tmp folder.
func CloneLogDir(repo string) string { return filepath.Join(repo, "tmp", "logs", "pfm") }

// LogRoot is the one door to where pfm's logs live, called by every writer
// and reader of one: the Professor clone this pfm was installed from (the
// install's source-repo marker, ReadSourceRepoMarker), under its tmp folder,
// created here when absent. A jailed home (EnvHome set: a test jail, the
// fence's runs) never reaches a clone, so it can never write into a real
// checkout; it and a home with no clone on record log under StateLogDir, and
// so does a home whose recorded clone is gone or whose tmp folder cannot be
// created or written (a read-only mount), with the reason in Fallback.
func LogRoot(env Env, home string) LogPlace {
	state := LogPlace{Dir: StateLogDir(home)}
	if env.Get(EnvHome) != "" {
		return state
	}
	repo, err := ReadSourceRepoMarker(home)
	if errors.Is(err, ErrNoSourceRepoMarker) {
		return state
	}
	if err != nil {
		state.Fallback = fmt.Errorf("log root: %w", err)
		return state
	}
	dir := CloneLogDir(repo)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		state.Fallback = fmt.Errorf("log root: create %s: %w", dir, err)
		return state
	}
	// access(2) answers for this process's own identity, root and a
	// read-only mount included, without leaving a probe file behind.
	if err := unix.Access(dir, unix.W_OK); err != nil {
		state.Fallback = fmt.Errorf("log root: %s is not writable: %w", dir, err)
		return state
	}
	return LogPlace{Dir: dir}
}
