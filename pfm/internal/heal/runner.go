package heal

import (
	"context"
	"fmt"
	"strings"
	"time"

	"hostops/pfm/internal/clock"
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

// Stores reports the files this runner reads and writes.
func (runner *Runner) Stores() Stores {
	return runner.stores
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
) (Report, error) {
	report, err := Sweep(ctx, runner.stores, options.Thread)
	if err != nil {
		return Report{}, err
	}
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
	for _, thread := range broken {
		if Live(runner.stores.Root, thread.ID) {
			report.SkippedLive = append(report.SkippedLive, thread.ID)
		}
	}
	if len(report.SkippedLive) == len(broken) {
		// Nothing to write, so nothing to back up.
		return report, nil
	}
	backup, err := Backup(runner.stores, runner.now())
	if err != nil {
		return Report{}, err
	}
	report.BackupDir = backup

	skipped := make(map[string]struct{}, len(report.SkippedLive))
	for _, id := range report.SkippedLive {
		skipped[id] = struct{}{}
	}
	for _, thread := range broken {
		if _, live := skipped[thread.ID]; live {
			continue
		}
		if err := Delete(ctx, runner.stores, thread.ID); err != nil {
			return Report{}, fmt.Errorf("heal %s: %w", thread.ID, err)
		}
		report.Healed = append(report.Healed, thread.ID)
	}
	return report, nil
}

// Thread heals one conversation ahead of a resume and reports what it did in
// one line, or nothing at all when there was nothing to do.
//
// It never fails a resume: a Codex home with no stores, an unreadable
// projection, a thread somebody else is holding — each returns an empty
// message and a nil error, because opening the chat matters more than
// repairing it, and the resume's own continuity banner already warns when a
// thread looks short.
func Thread(ctx context.Context, codexHome, threadID string) string {
	if threadID == "" {
		return ""
	}
	runner, err := New(codexHome, nil)
	if err != nil {
		return ""
	}
	report, err := runner.Run(ctx, Options{Thread: threadID})
	if err != nil {
		return ""
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
