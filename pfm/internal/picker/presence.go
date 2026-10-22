package picker

import (
	"reflect"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/ui"
)

// presenceBaseline is the fork-free fingerprint of the fleet as the last full
// pass saw it. The directory half is taken BEFORE the pass gathers (a socket
// that appears while the multi-second pass runs is then a change, not part of
// the baseline); the process half is taken from the pass's own snapshot once it
// has published.
type presenceBaseline struct {
	pendingFiles string
	filesErr     error
	files        string
	procs        string
	have         bool
	// passAt is when the last full pass began: a baseline that could not be
	// taken buys its next full pass only fleetRefreshParkThreshold after it.
	passAt time.Time
	// forcedGone holds each "pid gone" line that already bought its one fresh
	// pass. A pid its own snapshot lists already gone means the snapshot is
	// older than the probe, and it owes one pass; a pid that stays gone after
	// that pass is a fact about the host (a pane pid the proc root cannot see)
	// and must not spend a full pass every poll — but a NEW gone pid, a chat
	// that died while the pass ran, still owes its own.
	forcedGone map[string]bool
	// held is set while a change waits one poll for its pass (see moved).
	held bool
}

func presenceProbeFor(environment fleet.Env) gather.PresenceProbe {
	return gather.PresenceProbe{
		TmuxDir: environment.Paths.TmuxDir,
		SIDDir:  environment.Paths.SIDDir,
		Proc:    gather.NewProcFS(environment.Paths.ProcRoot),
	}
}

// begin fingerprints the directories at the start of a pass.
func (baseline *presenceBaseline) begin(environment fleet.Env, now time.Time) {
	baseline.passAt = now
	baseline.held = false
	files, err := presenceProbeFor(environment).Files()
	if err != nil {
		baseline.filesErr = err
		return
	}
	baseline.pendingFiles, baseline.filesErr = files, nil
}

// record commits the baseline once the pass has published its snapshot. An
// error is a baseline that could not be taken: the parked poll is then blind,
// and the caller must say so rather than let the fleet read as watched.
func (baseline *presenceBaseline) record(environment fleet.Env, live gather.Snapshot) error {
	if baseline.filesErr != nil {
		baseline.have = false
		return baseline.filesErr
	}
	procs, settled, err := presenceProbeFor(environment).Procs(live)
	if err != nil {
		baseline.have = false
		return err
	}
	baseline.files, baseline.procs, baseline.have = baseline.pendingFiles, procs, true
	if settled {
		baseline.forcedGone = nil
	}
	return nil
}

// moved reports whether the fleet differs from the baseline, and a full pass is
// due. A probe that cannot run is a change plus the error: an unreadable
// directory never reads as an unchanged fleet. With no baseline at all (the
// last pass could not take one) the poll cannot answer, and a full pass is due
// once per fleetRefreshParkThreshold — never on every poll, or a persistent
// error would cost a multi-second pass every two seconds.
//
// A change is held for one poll before it buys its pass. A chat's birth and
// death each land in two steps a poll apart (its socket, then its pane crumb;
// its server going down, then its crumb swept), and a pass per step doubles
// the seconds of CPU the change costs; the held poll sees both. The pass is
// still due within two polls of the change.
func (baseline *presenceBaseline) moved(environment fleet.Env, live gather.Snapshot, now time.Time) (bool, error) {
	if !baseline.have {
		return now.Sub(baseline.passAt) >= fleetRefreshParkThreshold, nil
	}
	probe := presenceProbeFor(environment)
	files, err := probe.Files()
	if err != nil {
		baseline.held = false
		return true, err
	}
	procs, _, err := probe.Procs(live)
	if err != nil {
		baseline.held = false
		return true, err
	}
	changed := files != baseline.files || procs != baseline.procs
	var owed []string
	if !changed {
		for _, line := range strings.Split(procs, "\n") {
			if strings.HasSuffix(line, ":gone") && !baseline.forcedGone[line] {
				owed = append(owed, line)
			}
		}
		changed = len(owed) > 0
	}
	if !changed {
		baseline.held = false
		return false, nil
	}
	if !baseline.held {
		baseline.held = true
		return false, nil
	}
	baseline.held = false
	for _, line := range owed {
		if baseline.forcedGone == nil {
			baseline.forcedGone = make(map[string]bool)
		}
		baseline.forcedGone[line] = true
	}
	return true, nil
}

// hasLiveRow reports whether any composed row is a live chat: the only rows
// whose working state can change under a parked picker.
func hasLiveRow(rows []compose.Row) bool {
	for index := range rows {
		if rows[index].Kind.IsLiveSeat() {
			return true
		}
	}
	return false
}

// rowFactsMoved reports whether a re-read of the rows' facts changed anything
// the picker shows: the rows themselves (model, effort, working state) or why
// facts or schedules could not be read, and the cached picker theme.
func rowFactsMoved(previous, next ui.Snapshot) bool {
	return previous.FactsError != next.FactsError ||
		previous.ScheduleError != next.ScheduleError ||
		previous.ThemeError != next.ThemeError ||
		!reflect.DeepEqual(previous.ThemePalette, next.ThemePalette) ||
		!reflect.DeepEqual(previous.Rows, next.Rows)
}
