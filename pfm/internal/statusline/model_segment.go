package statusline

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/modelglyph"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// modelSegment is the main line's model block: the model's symbol and name,
// then the effort joined by a muted "·" — the same shape the sub-agent rows
// give their model (subagentModel). No effort renders the model alone.
func modelSegment(data input, state autoEffortState) string {
	return cModel + modelglyph.Symbol(
		data.Model.DisplayName,
	) + " " + data.Model.DisplayName + reset + effortSuffix(
		data, state,
	)
}

// effortSuffix keeps every source and failure visible beside the shared effort bar.
func effortSuffix(data input, state autoEffortState) string {
	joint := cMuted + "·" + reset
	if !data.Thinking.Enabled {
		if data.Effort.Level == "" {
			return ""
		}
		return joint + cMuted + "effort off" + reset
	}
	switch {
	case state.err != nil:
		return joint + cBad + modeLabel("ERR", modelglyph.EffortLabel(data.Effort.Level)) + reset
	case state.stale:
		return joint + cMuted + modeLabel("STALE", modelglyph.EffortLabel(data.Effort.Level)) + reset
	case !state.found && data.Effort.Level == "":
		return ""
	case !state.found:
		return joint + paintedEffort(data.Effort.Level, cEffort) + reset
	case state.mode == "off":
		return joint + cEffort + "effort off" + reset
	case state.mode == "auto":
		label := paintedEffort(state.level, cEffort)
		if label == "" {
			label = cEffort + "effort"
		}
		label += " AUTO"
		// Only the producer's exact escalation reason names failing tests.
		if count, ok := strings.CutSuffix(state.reason, " failing test runs"); ok {
			if failures, err := strconv.Atoi(count); err == nil && failures > 0 && strconv.Itoa(failures) == count {
				label += " · raised: " + count + " failing tests"
			}
		}
		return joint + label + reset
	default:
		// model and pin always carry a level (parseAutoEffortState refuses them without one).
		return joint + modeLabel(autoEffortModes[state.mode], paintedEffort(state.level, cEffort)) + reset
	}
}

// modeLabel joins a level's label and a source or visible state diagnostic; no
// label reads "effort {mode}". ERR and STALE pass the plain label, so the whole
// segment wears the state's colour; the sources pass paintedEffort's.
func modeLabel(mode, label string) string {
	if label == "" {
		return "effort " + mode
	}
	return label + " · " + mode
}

// paintedEffort is a level's filled-only bar in the level's own colour
// (effortBarColors), then the level word in ambient, the colour the segment
// wears. A level outside the five has no bar and no level colour, and no level
// is "". The caller closes the segment with reset.
func paintedEffort(level, ambient string) string {
	name, bar := modelglyph.EffortBar(level)
	if bar == "" {
		if name == "" {
			return ""
		}
		return ambient + modelglyph.EffortLabel(level)
	}
	return effortBarColors[name] + bar + reset + ambient + " " + name
}

// effortWords lists the producer's five valid levels.
var effortWords = map[string]bool{
	"low": true, "medium": true, "high": true, "xhigh": true, "max": true,
}

// autoEffortModes names the unchanged schema2 modes in source words.
var autoEffortModes = map[string]string{
	"auto":  "AUTO",
	"model": "model's pick",
	"pin":   "your /effort",
	"off":   "effort off",
}

// autoEffortStateVersion is the state file's `version` this reader knows.
const autoEffortStateVersion = 2

// autoEffortStateMaxBytes caps a read: the file is one short JSON line.
const autoEffortStateMaxBytes = 64 << 10

// autoEffortState is what the session's auto-effort-control state file says: found
// with a valid mode and level ("" for null), or err for a file that exists or
// could not be looked for and cannot be read or says nothing valid, or stale:
// a valid line no live keeper vouches for. None of them is no file.
type autoEffortState struct {
	found, stale        bool
	mode, level, reason string
	err                 error
}

// sessionAutoEffort reads the auto-effort-control state file of the payload's session.
// No session id names no file: the absent state. Every failure goes to warn,
// one line naming the path and the cause.
func sessionAutoEffort(runtime Runtime, data input) autoEffortState {
	if !data.Thinking.Enabled {
		return autoEffortState{}
	}
	sessionID := sessionIDOf(data.SessionID, data.TranscriptPath)
	if sessionID == "" {
		return autoEffortState{}
	}
	path, err := paths.AutoEffortStateFile(runtime.pathsEnv(), runtime.Home, sessionID)
	state := autoEffortState{err: err}
	if err == nil {
		state = parseAutoEffortState(path, readCapped, keeperProbe(gather.NewProcFS(runtime.ProcRoot), signalZero))
	}
	switch {
	case state.err == nil:
	case path == "":
		// No path was found: the cause names the state file itself.
		fmt.Fprintf(runtime.Warn, "statusline: %v (effort shows ERR)\n", state.err)
	default:
		fmt.Fprintf(runtime.Warn, "statusline: auto-effort-control state %s (effort shows ERR): %v\n", path, state.err)
	}
	return state
}

// autoEffortRecord is the state file's line (the plugin's stateText); its
// reason supplies the exact automatic-escalation source; updatedAt is not shown.
type autoEffortRecord struct {
	Version int     `json:"version"`
	Mode    string  `json:"mode"`
	Level   *string `json:"level"`
	Reason  string  `json:"reason"`
	// Keeper is the pid of the plugin's keeper process, 0 when none vouches
	// for the line; kept raw so a non-integer is refused, never truncated.
	Keeper *json.RawMessage `json:"keeper"`
}

// parseAutoEffortState reads and validates the state file at path. The plugin
// rewrites it in place, so a read that does not parse is read once more before
// the file counts as broken.
func parseAutoEffortState(
	path string,
	read func(string) ([]byte, error),
	isKeeper func(pid int) (bool, error),
) autoEffortState {
	var record autoEffortRecord
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		record = autoEffortRecord{}
		var raw []byte
		raw, err = read(path)
		if errors.Is(err, os.ErrNotExist) {
			return autoEffortState{}
		}
		if err != nil {
			return autoEffortState{err: fmt.Errorf("read: %w", err)}
		}
		if err = json.Unmarshal(raw, &record); err == nil {
			break
		}
		err = fmt.Errorf("parse %q: %w", clipForWarn(string(bytes.TrimSpace(raw))), err)
	}
	if err != nil {
		return autoEffortState{err: err}
	}
	if record.Version != autoEffortStateVersion {
		return autoEffortState{err: fmt.Errorf("version %d, want %d", record.Version, autoEffortStateVersion)}
	}
	if _, ok := autoEffortModes[record.Mode]; !ok {
		return autoEffortState{err: fmt.Errorf("unknown mode %q", record.Mode)}
	}
	level := ""
	if record.Level != nil {
		level = *record.Level
		if _, ok := effortWords[level]; !ok {
			return autoEffortState{err: fmt.Errorf("unknown level %q", level)}
		}
	}
	if level == "" && (record.Mode == "model" || record.Mode == "pin") {
		return autoEffortState{err: fmt.Errorf("mode %s without a level", record.Mode)}
	}
	if record.Keeper == nil {
		return autoEffortState{err: errors.New("no keeper")}
	}
	keeper, err := strconv.ParseInt(string(*record.Keeper), 10, 32)
	if err != nil || keeper < 0 {
		return autoEffortState{err: fmt.Errorf("keeper %q is no pid", clipForWarn(string(*record.Keeper)))}
	}
	if keeper == 0 {
		return autoEffortState{stale: true}
	}
	live, err := isKeeper(int(keeper))
	if err != nil {
		return autoEffortState{err: err}
	}
	if !live {
		return autoEffortState{stale: true}
	}
	return autoEffortState{found: true, mode: record.Mode, level: level, reason: record.Reason}
}

// keeperMark is the last argv element of auto-effort-control's keeper process (the
// plugin's src/state.ts KEEPER_MARK).
const keeperMark = "auto-effort-control-keeper"

// signalZero sends pid the null signal: kill(2)'s existence and permission
// check, nothing delivered.
func signalZero(pid int) error { return syscall.Kill(pid, 0) }

// keeperProbe answers whether pid is a live auto-effort-control keeper: no such
// process, a zombie, another user's process (the keeper runs as the status
// line's own user, so the null signal reaches it) or a process without
// keeperMark in its argv is not one; a probe or argv read that fails any other
// way is an error, never an answer. signal is signalZero outside the tests.
func keeperProbe(proc gather.ProcFS, signal func(pid int) error) func(int) (bool, error) {
	return func(pid int) (bool, error) {
		err := signal(pid)
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.EPERM) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("probe keeper %d: %w", pid, err)
		}
		argv, err := proc.Cmdline(pid)
		if err == nil {
			return slices.Contains(argv, keeperMark), nil
		}
		if errors.Is(signal(pid), syscall.ESRCH) {
			return false, nil // it ended between the probe and the read
		}
		if identity, ok := proc.(gather.ProcIdentity); ok {
			if who, identityErr := identity.ProcessIdentity(pid); identityErr == nil && who.Zombie {
				return false, nil
			}
		}
		return false, fmt.Errorf("read keeper %d argv: %w", pid, err)
	}
}

// readCapped reads a regular file of at most autoEffortStateMaxBytes. It opens
// without blocking and refuses anything else, so a pipe at the path is an
// error, never a render waiting on a writer.
func readCapped(path string) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("not a regular file (%s)", info.Mode().Type())
	}
	if err != nil {
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(file, autoEffortStateMaxBytes+1))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	if len(raw) > autoEffortStateMaxBytes {
		return nil, fmt.Errorf("larger than %d bytes", autoEffortStateMaxBytes)
	}
	return raw, nil
}

// clipForWarn shortens a torn file's text for a warn line.
func clipForWarn(text string) string {
	if runes := []rune(text); len(runes) > 80 {
		return string(runes[:80]) + "…"
	}
	return text
}
