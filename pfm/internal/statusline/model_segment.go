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
// give their model (subagentModel). Missing effort remains explicitly unknown.
func modelSegment(data input, state autoEffortState) string {
	return cModel + modelglyph.Symbol(
		data.Model.DisplayName,
	) + " " + data.Model.DisplayName + reset + effortSuffix(
		data, state,
	)
}

// effortSuffix shows the level the request goes at. auto-effort
// rewrites each request's effort, so while its live keeper holds a level, that
// level shows, marked "requested" whatever the mode. Off, no level, no file or
// no live keeper reads as if the plugin were not installed: the payload's level
// alone. Only a state file that cannot be trusted adds a word, ERR.
func effortSuffix(data input, state autoEffortState) string {
	joint := cMuted + "·" + reset
	plain := modelglyph.EffortLabel(data.Effort.Level)
	if plain == "" {
		plain = "effort unknown"
	}
	switch {
	case state.err != nil:
		return joint + cBad + plain + " · ERR" + reset
	case state.found && state.mode != "off" && state.level != "":
		return joint + paintedEffort(state.level, cEffort) + " · requested" + reset
	}
	label := paintedEffort(data.Effort.Level, cEffort)
	if label == "" {
		label = cEffort + plain
	}
	return joint + label + reset
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

// autoEffortModes lists the state file's modes.
var autoEffortModes = map[string]bool{
	"auto": true, "model": true, "pin": true, "off": true,
}

// autoEffortStateVersion is the state file's `version` this reader knows.
const autoEffortStateVersion = 3

// autoEffortStateMaxBytes caps a read: the file is one short JSON line.
const autoEffortStateMaxBytes = 64 << 10

// autoEffortState is what the session's auto-effort state file says: found
// with a valid mode and level ("" for null), or err for a file that exists or
// could not be looked for and cannot be read or says nothing valid. No file and
// a valid line no live keeper vouches for are both the zero state: no plugin.
type autoEffortState struct {
	found       bool
	mode, level string
	err         error
}

// sessionAutoEffort reads the auto-effort state file of the payload's session.
// No session id names no file: the absent state. Every failure goes to warn,
// one line naming the path and the cause.
func sessionAutoEffort(runtime Runtime, data input) autoEffortState {
	sessionID := sessionIDOf(data.SessionID, data.TranscriptPath)
	if sessionID == "" {
		return autoEffortState{}
	}
	path, err := paths.AutoEffortStateFile(runtime.pathsEnv(), runtime.Home, sessionID)
	state := autoEffortState{err: err}
	if err == nil {
		state = parseAutoEffortState(
			path,
			readCapped,
			keeperProbe(gather.NewProcFS(runtime.ProcRoot), gather.ProcessAlive),
		)
	}
	switch {
	case state.err == nil:
	case path == "":
		// No path was found: the cause names the state file itself.
		fmt.Fprintf(runtime.Warn, "statusline: %v (effort shows ERR)\n", state.err)
	default:
		fmt.Fprintf(runtime.Warn, "statusline: auto-effort state %s (effort shows ERR): %v\n", path, state.err)
	}
	return state
}

// autoEffortRecord is the state file's line (the plugin's stateText); reason
// and updatedAt are not shown.
type autoEffortRecord struct {
	Version int     `json:"version"`
	Mode    string  `json:"mode"`
	Level   *string `json:"level"`
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
	// The keeper vouches before the line is judged: a line left by a gone or
	// retired producer (an older version among them, one that wrote no keeper
	// at all) reads as absent.
	if record.Keeper == nil {
		return autoEffortState{}
	}
	keeper, err := strconv.ParseInt(string(*record.Keeper), 10, 32)
	if err != nil || keeper < 0 {
		return autoEffortState{err: fmt.Errorf("keeper %q is no pid", clipForWarn(string(*record.Keeper)))}
	}
	if keeper == 0 {
		return autoEffortState{}
	}
	live, err := isKeeper(int(keeper))
	if err != nil {
		return autoEffortState{err: err}
	}
	if !live {
		return autoEffortState{}
	}
	if record.Version != autoEffortStateVersion {
		return autoEffortState{err: fmt.Errorf("version %d, want %d", record.Version, autoEffortStateVersion)}
	}
	if !autoEffortModes[record.Mode] {
		return autoEffortState{err: fmt.Errorf("unknown mode %q", record.Mode)}
	}
	// A level word outside the five (a newer plugin's) shows as the raw word,
	// marked requested, never as ERR.
	level := ""
	if record.Level != nil {
		level = *record.Level
	}
	if level == "" && (record.Mode == "model" || record.Mode == "pin") {
		return autoEffortState{err: fmt.Errorf("mode %s without a level", record.Mode)}
	}
	return autoEffortState{found: true, mode: record.Mode, level: level}
}

// keeperMark is the last argv element of auto-effort's keeper process (the
// plugin's src/state.ts KEEPER_MARK).
const keeperMark = "auto-effort-keeper"

// keeperProbe answers whether pid is a live auto-effort keeper: no such
// process, a zombie, another user's process (the keeper runs as the status
// line's own user) or a process without keeperMark in its argv is not one; a
// probe or read that fails any other way is an error, never an answer. alive is
// gather.ProcessAlive outside the tests.
func keeperProbe(proc gather.ProcFS, alive func(pid int) (bool, error)) func(int) (bool, error) {
	return func(pid int) (bool, error) {
		live, err := alive(pid)
		if err != nil {
			return false, fmt.Errorf("probe keeper %d: %w", pid, err)
		}
		if !live {
			return false, nil
		}
		ours, err := processOurs(proc, alive, pid)
		if err != nil {
			return false, fmt.Errorf("inspect keeper %d: %w", pid, err)
		}
		if !ours {
			return false, nil
		}
		argv, err := proc.Cmdline(pid)
		if err == nil {
			return slices.Contains(argv, keeperMark), nil
		}
		if processGone(alive, pid) {
			return false, nil // it ended between the probe and the read
		}
		return false, fmt.Errorf("read keeper %d argv: %w", pid, err)
	}
}

// processOurs answers whether the live pid is a running process of the status
// line's own user, as the keeper and Claude both are: a zombie or another
// user's process is not. A failed identity read is an error unless the process
// ended meanwhile; a ProcFS without identity metadata answers yes.
func processOurs(proc gather.ProcFS, alive func(int) (bool, error), pid int) (bool, error) {
	identities, ok := proc.(gather.ProcIdentity)
	if !ok {
		return true, nil
	}
	who, err := identities.ProcessIdentity(pid)
	if err != nil {
		if processGone(alive, pid) {
			return false, nil
		}
		return false, err
	}
	return !who.Zombie && who.EffectiveUID == uint32(os.Geteuid()), nil
}

// processGone re-probes pid after a failed read: true only when alive proves it
// ended, so a probe that cannot tell keeps the read's error.
func processGone(alive func(int) (bool, error), pid int) bool {
	live, err := alive(pid)
	return err == nil && !live
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

// subagentModel names the family and effort: claude-opus-5-5[1m] + high →
// opus·▰▰▰ high. The level its transcript recorded for its latest request
// (recorded) wins: it is the effort that request went out at, after any
// plugin pinned it, so it renders plainly on any model. Without one, the
// payload's effort shows; one that is not a JSON string is left out. A row
// with neither shows the session's recorded effort (inherited), exactly as
// a payload effort on the model it was recorded for and muted on another,
// where Claude Code may resolve a different level.
func subagentModel(model, recorded string, effort json.RawMessage, inherited sessionEffortRecord) string {
	family := modelFamily(model)
	level := strings.TrimSpace(recorded)
	color, painted := cEffort, true
	if level == "" &&
		(len(effort) == 0 || json.Unmarshal(effort, &level) != nil || strings.TrimSpace(level) == "") {
		level = ""
		if len(effort) == 0 {
			level = inherited.Level
		}
		if modelVersion(model) != modelVersion(inherited.Model) {
			color, painted = cMuted, false
		}
	}
	if level != "" {
		// A muted level may be another than the one shown, so its bar stays muted
		// too; otherwise the bar wears the level's own colour.
		label := color + modelglyph.EffortLabel(level)
		if painted {
			label = paintedEffort(level, color)
		}
		if family == "" {
			return label + reset
		}
		return cModel + family + reset + cMuted + "·" + reset + label + reset
	}
	if family == "" {
		return ""
	}
	return cModel + family + reset
}

// modelVersion strips the vendor prefix and the context-window tag:
// claude-opus-5-5[1m] → opus-5-5.
func modelVersion(model string) string {
	model = strings.TrimPrefix(strings.TrimSpace(model), "claude-")
	if cut := strings.IndexByte(model, '['); cut >= 0 {
		model = model[:cut]
	}
	return model
}

// modelFamily is the model's family alone: claude-opus-5-5[1m] → opus. A gpt-
// id names its tier last and its version first, so it renders tier-version:
// gpt-6-sol → sol-6, gpt-5.6-terra → terra-5.6; one with no tier renders
// whole, gpt-6 → gpt-6. The gateway's priority-tier suffix is no tier and
// drops like the window tag: gpt-6-sol-fast → sol-6, gpt-5.5-fast → gpt-5.5.
func modelFamily(model string) string {
	model = modelVersion(model)
	if strings.HasPrefix(model, "gpt-") {
		model = strings.TrimSuffix(model, "-fast")
		rest := strings.TrimPrefix(model, "gpt-")
		if cut := strings.LastIndexByte(rest, '-'); cut > 0 && cut < len(rest)-1 {
			return rest[cut+1:] + "-" + rest[:cut]
		}
		return model
	}
	if cut := strings.IndexByte(model, '-'); cut > 0 {
		model = model[:cut]
	}
	return model
}
