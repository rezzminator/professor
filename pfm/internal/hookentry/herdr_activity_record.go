package hookentry

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// A session's activity record is activityRecordPrefix, its key's digest and
// activityRecordSuffix; herdrWatchRecord ends its transcript watch record beside it.
const (
	activityRecordPrefix = "activity-"
	activityRecordSuffix = ".json"
)

func (reporter *herdrReporter) activityKey(session string) string {
	return fmt.Sprintf("%s%x", activityRecordPrefix, sha256.Sum256([]byte(session)))
}

// activityFile is the file of the activity key's record named by suffix.
func (reporter *herdrReporter) activityFile(key, suffix string) string {
	return filepath.Join(reporter.deps.lockDir, key+suffix)
}

func (reporter *herdrReporter) activityPath(session string) string {
	return reporter.activityFile(reporter.activityKey(session), activityRecordSuffix)
}

func (reporter *herdrReporter) readActivity(session string) (herdrActivity, error) {
	return reporter.readActivityFile(reporter.activityPath(session))
}

func (reporter *herdrReporter) readActivityFile(path string) (herdrActivity, error) {
	var state herdrActivity
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("open Claude activity: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return state, fmt.Errorf("inspect Claude activity: %w", err)
	}
	const maxActivityBytes = 1 << 20
	if !info.Mode().IsRegular() || info.Size() > maxActivityBytes {
		return state, fmt.Errorf("claude activity is not a regular file of at most %d bytes", maxActivityBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxActivityBytes+1))
	if err != nil {
		return state, fmt.Errorf("read Claude activity: %w", err)
	}
	if len(data) > maxActivityBytes {
		return state, fmt.Errorf("claude activity exceeds %d bytes", maxActivityBytes)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		// A corrupt file must not freeze the session: this event rewrites it.
		reporter.warn("pfm internal herdr-state %s: decode Claude activity %s (starting empty): %v",
			reporter.options.event, path, err)
		return herdrActivity{}, nil
	}
	return state, nil
}

func (reporter *herdrReporter) writeActivity(session string, state herdrActivity) error {
	return reporter.writeActivityFile(reporter.activityPath(session), state)
}

func (reporter *herdrReporter) writeActivityFile(path string, state herdrActivity) error {
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("encode Claude activity: %w", err)
	}
	if err := atomicfile.Write(path, data, 0o600); err != nil {
		return fmt.Errorf("store Claude activity: %w", err)
	}
	return nil
}

// removeActivity removes an ended session's record and its transcript watch's.
func (reporter *herdrReporter) removeActivity(session string) error {
	for _, path := range []string{reporter.activityPath(session), reporter.watchRecord(session)} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove ended Claude activity: %w", err)
		}
	}
	return nil
}

// ReleaseNativeClaudeReports hands every pane an older pfm build reported a
// native Claude's state to under herdr:claude back to Herdr, once: a reported
// state outranks Herdr's screen detection until it is released. pfm install
// runs it on a Herdr host, so a chat that fires no further hook is released too.
func ReleaseNativeClaudeReports(ctx context.Context, home string) error {
	deps := realHerdrDeps()
	deps.lockDir = paths.HerdrLockDir(home)
	reporter := &herdrReporter{
		deps: deps, stderr: io.Discard, options: herdrOptions{engine: pfmengine.Claude, event: herdr.EventIdle},
	}
	return reporter.releaseNativeClaudeReports(ctx)
}

// releaseNativeClaudeReports retires every activity record's native targets as
// a hook does. A failed release stays retired, so the next install or hook
// retries it.
func (reporter *herdrReporter) releaseNativeClaudeReports(ctx context.Context) error {
	records, err := filepath.Glob(reporter.activityFile(activityRecordPrefix+"*", activityRecordSuffix))
	if err != nil {
		return fmt.Errorf("list Claude activity records in %s: %w", reporter.deps.lockDir, err)
	}
	var errs []error
	for _, record := range records {
		key := strings.TrimSuffix(filepath.Base(record), activityRecordSuffix)
		if err := reporter.releaseNativeRecord(ctx, key); err != nil {
			errs = append(errs, fmt.Errorf("hand native Claude panes of %s back to Herdr: %w", record, err))
		}
	}
	return errors.Join(errs...)
}

// releaseNativeRecord runs under the record's activity lock, so an older
// build's watchdog or settle process rereads the record after it, never before.
// A record the retirement leaves reaching no pane goes with its watch record.
func (reporter *herdrReporter) releaseNativeRecord(ctx context.Context, key string) error {
	unlock, err := reporter.lock(ctx, key)
	if err != nil {
		return err
	}
	defer unlock()
	path := reporter.activityFile(key, activityRecordSuffix)
	state, err := reporter.readActivityFile(path)
	if err != nil {
		return err
	}
	reached := len(state.Targets) + len(state.RetiredTargets)
	if err := reporter.retireNativeTargets(ctx, path, &state); err != nil {
		return err
	}
	if reached == 0 || len(state.Targets) > 0 || len(state.RetiredTargets) > 0 || state.TmuxSocket != "" {
		return nil
	}
	var errs []error
	for _, file := range []string{path, reporter.activityFile(key, herdrWatchRecord)} {
		if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove native Claude activity %s: %w", file, err))
		}
	}
	return errors.Join(errs...)
}
