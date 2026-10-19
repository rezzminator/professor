package statusline

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const keepwarmOff = "off"

// keepwarmBirthSlackMS is how much later than the record's updatedAt the
// writer's start time may read before the pid counts as reused.
const keepwarmBirthSlackMS = 2000

type keepwarmPokes struct {
	Count int `json:"count"`
	Max   int `json:"max"`
}

// keepwarmRecord is the public cache-keepwarm v1 protocol, not the plugin's
// internal persistence. A quiet live session's record does not expire by age.
type keepwarmRecord struct {
	Version    int            `json:"v"`
	SessionID  string         `json:"sessionId"`
	PID        int            `json:"pid"`
	UpdatedAt  int64          `json:"updatedAt"`
	Enabled    *bool          `json:"enabled"`
	Source     string         `json:"source"`
	State      string         `json:"state"`
	Reason     string         `json:"reason"`
	Pokes      *keepwarmPokes `json:"pokes"`
	NextAt     *int64         `json:"nextAt"`
	Protecting *float64       `json:"protectingTokensEq"`
}

type keepwarmState struct {
	found, stale bool
	record       keepwarmRecord
	err          error
}

func cacheKeepwarmShield(runtime Runtime, now time.Time, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	path, err := paths.CacheKeepwarmStateFile(runtime.pathsEnv(), runtime.Home, sessionID)
	state := keepwarmState{err: err}
	if err == nil {
		state = readKeepwarmState(
			path,
			sessionID,
			readCapped,
			keepwarmProbe(gather.NewProcFS(runtime.ProcRoot), signalZero),
		)
	}
	if state.err != nil {
		warn := runtime.Warn
		if warn == nil {
			warn = os.Stderr
		}
		if path == "" {
			// No path was resolved: the cause names the state file itself.
			fmt.Fprintf(warn, "statusline: %v (shield shows ERR)\n", state.err)
		} else {
			fmt.Fprintf(warn, "statusline: cache-keepwarm state %s (shield shows ERR): %v\n", path, state.err)
		}
		return " " + cBad + "🛡 ERR" + reset
	}
	if state.stale {
		return " " + cMuted + "🛡 ?" + reset
	}
	if !state.found {
		return ""
	}
	record := state.record
	color, text := cMuted, "🛡 "+record.State
	switch record.State {
	case "warming":
		color, text = cWarn, "🛡"
		if record.Pokes != nil {
			text += fmt.Sprintf(" %d/%d", record.Pokes.Count, record.Pokes.Max)
		}
		if record.NextAt != nil {
			remaining := time.UnixMilli(*record.NextAt).Sub(now)
			next := "<1m"
			if remaining >= time.Minute {
				next = fmt.Sprintf("%dm", int64(remaining/time.Minute))
			}
			text += " ⟳" + next
		}
		if record.Protecting != nil {
			thousands := math.Round(*record.Protecting / 1000)
			savings := fmt.Sprintf("%.0fk", thousands)
			if thousands >= 1000 {
				savings = fmt.Sprintf("%.1fM", math.Round(*record.Protecting/100000)/10)
			}
			text += " guards ≈" + savings
		}
	case "stopped":
		color, text = cBad, "🛡"
		if record.Pokes != nil {
			text += fmt.Sprintf(" %d/%d", record.Pokes.Count, record.Pokes.Max)
		}
		text += " ✕"
	}
	return " " + color + text + reset
}

func readKeepwarmState(
	path, sessionID string,
	read func(string) ([]byte, error),
	live func(pid int, updatedAt int64) (bool, error),
) keepwarmState {
	body, err := read(path)
	if errors.Is(err, os.ErrNotExist) {
		return keepwarmState{}
	}
	if err != nil {
		return keepwarmState{err: fmt.Errorf("read: %w", err)}
	}
	var record keepwarmRecord
	if err = json.Unmarshal(body, &record); err != nil {
		return keepwarmState{err: fmt.Errorf("parse: %w", err)}
	}
	if err = record.validate(sessionID); err != nil {
		return keepwarmState{err: err}
	}
	alive, err := live(record.PID, record.UpdatedAt)
	if err != nil {
		return keepwarmState{err: err}
	}
	if !alive {
		return keepwarmState{stale: true}
	}
	return keepwarmState{found: true, record: record}
}

func (record keepwarmRecord) validate(sessionID string) error {
	if record.Version != 1 {
		return fmt.Errorf("version %d, want 1", record.Version)
	}
	if record.SessionID != sessionID {
		return fmt.Errorf("sessionId %q, want %q", record.SessionID, sessionID)
	}
	if record.PID <= 0 || record.UpdatedAt <= 0 || record.Enabled == nil {
		return errors.New("invalid or missing pid, updatedAt or enabled")
	}
	if record.Source != "launch" && record.Source != "session" {
		return fmt.Errorf("unknown source %q", record.Source)
	}
	switch record.State {
	case "warming", "on", "stopped", keepwarmOff:
	default:
		return fmt.Errorf("unknown state %q", record.State)
	}
	if strings.TrimSpace(record.Reason) == "" {
		return errors.New("missing reason")
	}
	if *record.Enabled == (record.State == keepwarmOff) {
		return errors.New("enabled disagrees with state")
	}
	if record.Pokes != nil &&
		(record.Pokes.Count < 0 || record.Pokes.Max < 1 || record.Pokes.Count > record.Pokes.Max) {
		return errors.New("invalid poke counters")
	}
	if record.NextAt != nil && *record.NextAt < 0 {
		return errors.New("negative nextAt")
	}
	if record.Protecting != nil &&
		(*record.Protecting < 0 || math.IsNaN(*record.Protecting) || math.IsInf(*record.Protecting, 0)) {
		return errors.New("invalid protectingTokensEq")
	}
	return nil
}

// keepwarmProbe uses the shared process classifier. A reused PID is not a live
// Claude: a process that is not Claude, a zombie, or one born after the record's
// last write (so it cannot be the writer, even when it is another Claude) is
// stale. A failed inspection is an error, not an absent plugin. signalZero
// checks ownership/existence without sending a signal to the chat.
func keepwarmProbe(proc gather.ProcFS, signal func(int) error) func(int, int64) (bool, error) {
	return func(pid int, updatedAt int64) (bool, error) {
		err := signal(pid)
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, syscall.EPERM) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("probe Claude pid %d: %w", pid, err)
		}
		if identity, ok := proc.(gather.ProcIdentity); ok {
			who, identityErr := identity.ProcessIdentity(pid)
			if identityErr != nil {
				if errors.Is(signal(pid), syscall.ESRCH) {
					return false, nil
				}
				return false, fmt.Errorf("inspect Claude pid %d: %w", pid, identityErr)
			}
			if who.Zombie {
				return false, nil
			}
		}
		if births, ok := proc.(gather.ProcBirth); ok {
			born, birthErr := births.Birth(pid)
			if birthErr != nil {
				if errors.Is(signal(pid), syscall.ESRCH) {
					return false, nil
				}
				return false, fmt.Errorf("read Claude pid %d start time: %w", pid, birthErr)
			}
			// Birth is whole seconds; the slack absorbs its rounding.
			if born*1000 > updatedAt+keepwarmBirthSlackMS {
				return false, nil
			}
		}
		argv, err := proc.Cmdline(pid)
		if err != nil {
			if errors.Is(signal(pid), syscall.ESRCH) {
				return false, nil
			}
			return false, fmt.Errorf("read Claude pid %d argv: %w", pid, err)
		}
		return gather.IsClaudeCommand(argv), nil
	}
}
