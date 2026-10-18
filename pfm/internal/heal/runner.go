package heal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// Options are one heal run's knobs.
type Options struct {
	// Apply heals every broken thread that is not live. Without it the run
	// only reports.
	Apply bool
	// Thread limits the run to one thread id: heal it IF it is broken, and
	// say nothing at all otherwise. That silence is what lets a resume path
	// call this before every `codex resume` for free.
	Thread string
}

// Runner performs sweeps and heals against one Codex home.
type Runner struct {
	stores Stores
	now    func() time.Time
}

// New locates the stores under codexHome.
func New(codexHome string, now func() time.Time) (*Runner, error) {
	stores, err := FindStores(codexHome)
	if err != nil {
		return nil, err
	}
	if now == nil {
		now = clock.Real.Now
	}
	return &Runner{stores: stores, now: now}, nil
}

// Run sweeps, and with Apply (or Thread) heals what the sweep found broken.
//
// A LIVE thread is never healed: Codex holds its writer lock and carries the
// cursor in memory, so a heal underneath it would race its own next write.
// The thread is reported skipped instead — an honest refusal, not a silent
// pass.
func (runner *Runner) Run(
	ctx context.Context,
	options Options,
) (report Report, err error) {
	// The state door: one transition per run phase; a failure is attributed
	// to the phase the run was in.
	trail := obs.NewTrail(ctx, "heal", "requested")
	defer func() { trail.End(err) }()

	report, err = Sweep(ctx, runner.stores, options.Thread)
	if err != nil {
		return Report{}, err
	}
	trail.Reach("scanned", "projection cursors judged")
	if !options.Apply && options.Thread == "" {
		return report, nil
	}

	broken := make([]ThreadState, 0, len(report.Threads))
	for _, thread := range report.Threads {
		if thread.Verdict.Broken() {
			broken = append(broken, thread)
		}
	}
	if len(broken) == 0 {
		return report, nil
	}
	locks := filepath.Join(runner.stores.Root, "thread-writer-locks")
	if err := os.MkdirAll(locks, 0o700); err != nil {
		return report, fmt.Errorf("create writer lock directory %s: %w", locks, err)
	}
	coordination, _, lockErr := writerLock(filepath.Join(locks, ".coordination.lock"), true)
	if lockErr != nil {
		return report, lockErr
	}
	if coordination == nil {
		return report, fmt.Errorf("writer coordination lock is busy; projections left untouched")
	}
	// Keep coordination as well as the per-thread locks through the commit:
	// Codex removes writer files under coordination on guard drop and startup.
	// Without it a new inode could evade a held thread lock.
	defer func() {
		if closeErr := coordination.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close writer coordination lock: %w", closeErr))
		}
	}()
	writerLocks := make(map[string]*os.File, len(broken))
	defer func() {
		for threadID, file := range writerLocks {
			if closeErr := file.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close writer lock for %s: %w", threadID, closeErr))
			}
		}
	}()
	for _, thread := range broken {
		file, live, lockErr := writerLock(filepath.Join(locks, thread.ID+".lock"), true)
		if lockErr != nil {
			return report, fmt.Errorf("validate writer for %s: %w", thread.ID, lockErr)
		}
		if live {
			report.SkippedLive = append(report.SkippedLive, thread.ID)
			continue
		}
		writerLocks[thread.ID] = file
	}
	// Capture the backup time only after every candidate is protected, then
	// re-read projections before deciding whether that backup is needed.
	stamp := runner.now()
	// Merge protected readings once, including the completed prefix on an error.
	revalidated := make(map[string][]ThreadState, len(broken))
	defer func() {
		if len(revalidated) == 0 {
			return
		}
		retained := report.Threads[:0]
		for _, prior := range report.Threads {
			if _, replaced := revalidated[prior.ID]; !replaced {
				retained = append(retained, prior)
			}
		}
		for _, current := range revalidated {
			retained = append(retained, current...)
		}
		report.Threads = retained
		report.recount()
	}()
	ready := make([]ThreadState, 0, len(broken))
	for _, thread := range broken {
		if _, protected := writerLocks[thread.ID]; !protected {
			continue
		}
		// Revalidate after obtaining protection, not from the pre-lock snapshot.
		validated, sweepErr := Sweep(ctx, runner.stores, thread.ID)
		if sweepErr != nil {
			return report, fmt.Errorf("validate projection %s under writer lock: %w", thread.ID, sweepErr)
		}
		revalidated[thread.ID] = validated.Threads
		for _, current := range validated.Threads {
			if current.Verdict.Broken() {
				ready = append(ready, current)
			}
		}
	}
	trail.Reach("planned", "broken threads classified under writer locks")
	if len(ready) == 0 {
		return report, nil
	}
	backup, backupErr := Backup(runner.stores, stamp)
	if backupErr != nil {
		return report, backupErr
	}
	report.BackupDir = backup
	for _, thread := range ready {
		if deleteErr := Delete(ctx, runner.stores, thread.ID); deleteErr != nil {
			return report, fmt.Errorf("heal %s: %w", thread.ID, deleteErr)
		}
		report.Healed = append(report.Healed, thread.ID)
	}
	trail.Reach("repaired", "broken threads healed")
	return report, nil
}

// Thread heals one conversation ahead of a resume and reports what it did in
// one line, or nothing at all when there was nothing to do.
//
// It never blocks a resume. A failed lookup or repair returns a contextual
// warning; a successful check with nothing to repair, or a Codex home that
// keeps no thread store, is silent.
func Thread(ctx context.Context, codexHome, threadID string) string {
	if threadID == "" {
		return ""
	}
	runner, err := New(codexHome, nil)
	if errors.Is(err, ErrNoStores) {
		return ""
	}
	if err != nil {
		return fmt.Sprintf("pfm: warning: cannot check %s before resume: %v — continuing resume", threadID, err)
	}
	report, err := runner.Run(ctx, Options{Thread: threadID})
	if err != nil {
		return fmt.Sprintf("pfm: warning: cannot heal %s before resume: %v — continuing resume", threadID, err)
	}
	for _, healed := range report.Healed {
		if healed != threadID {
			continue
		}
		verdict := VerdictWedged
		for _, thread := range report.Threads {
			if thread.ID == threadID {
				verdict = thread.Verdict
			}
		}
		return fmt.Sprintf(
			"pfm: %s projection was %s — rebuilding its full history from the rollout",
			threadID,
			verdict,
		)
	}
	for _, live := range report.SkippedLive {
		if live == threadID {
			return fmt.Sprintf(
				"pfm: %s has a broken projection but another seat holds it — close that seat and resume again",
				threadID,
			)
		}
	}
	for _, thread := range report.Threads {
		if thread.ID != threadID || !thread.Verdict.LeftAlone() {
			continue
		}
		clause := anomalyClause(thread.Detail)
		if thread.Verdict == VerdictUnscanned {
			return fmt.Sprintf(
				"pfm: %s projection is wedged but its rollout could not be scanned (%s) — left alone",
				threadID,
				strings.TrimPrefix(clause, unscannedSuffix),
			)
		}
		return fmt.Sprintf(
			"pfm: %s has a noncanonical rollout (%s) — projection left alone; "+
				"Codex >= %s projects past it at this resume, older Codex shows the chat short",
			threadID,
			clause,
			CodexProjectsPastAnomalies,
		)
	}
	return ""
}

// anomalyClause returns the part of a Detail message appended by the ordinal
// scan — the segment after the LAST "; ", which classify always appends as
// the final piece when it downgrades a WEDGED/MIDLINE verdict to
// NONCANONICAL or UNSCANNED.
func anomalyClause(detail string) string {
	if index := strings.LastIndex(detail, "; "); index >= 0 {
		return detail[index+2:]
	}
	return detail
}
