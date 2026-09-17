// Package headless answers questions about a spawned chat. Its STATE always
// comes from the transcript and the socket, never from the tmux pane.
//
// The rule behind that: a chat that is gone must never look like a chat that
// is quiet. Pane-scraping loses whatever scrolled, cannot tell a crashed
// engine from a thinking one, and breaks the day either engine repaints — so
// liveness comes from the socket and content comes from the file, and both
// are reported explicitly.
//
// Ask is the single deliberate exception and it does not weaken the rule. It
// reads the pane because a human asking "what is this chat doing right now"
// wants the live screen, and it labels that capture as a distinct source so a
// reader can tell it from the transcript. It feeds no state: State, Alive, and
// everything Inspect decides are still derived from transcript and socket
// alone, and Ask reports a capture it could not take as an explicit failure
// rather than as an empty screen.
package headless

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/transcript"
)

// The states a chat can be in, as reported by status and watch.
const (
	StateWorking = "working"
	StateIdle    = "idle"
	StateDead    = "dead"
	StateMissing = "not-found"
)

// Chat is one resolved seat: who it is, where its record lives, and whether a
// server is still holding it up.
type Chat struct {
	Name    string
	ID      string
	Engine  pfmengine.ID
	Path    string
	CWD     string
	Socket  string
	Session string
	Pane    string
	Live    bool
}

// Status is the machine-readable verdict. Field names are a contract — a
// consumer scripts against them.
type Status struct {
	Name  string `json:"name"`
	State string `json:"state"`
	// IdleSeconds is how long the chat has been idle — nonzero ONLY when State
	// is idle. A working chat mid tool run writes nothing for minutes; that
	// silence is not idleness, and reporting it as such made the number
	// contradict the state beside it.
	IdleSeconds   int64        `json:"idle_seconds"`
	Engine        pfmengine.ID `json:"engine"`
	Model         string       `json:"model,omitempty"`
	CWD           string       `json:"cwd,omitempty"`
	SessionID     string       `json:"session_id,omitempty"`
	Socket        string       `json:"socket,omitempty"`
	ContextPct    float64      `json:"context_pct,omitempty"`
	Last          string       `json:"last,omitempty"`
	Summary       string       `json:"summary,omitempty"`
	SummaryCached bool         `json:"summary_cached,omitempty"`
	Ask           string       `json:"ask,omitempty"`
}

// SummaryLine is the human status suffix. Cached summaries say so at the
// label, while structured output carries SummaryCached separately.
func (status Status) SummaryLine() string {
	label := "summary"
	if status.SummaryCached {
		label = "summary(cached)"
	}
	return label + ": " + status.Summary
}

// AskLine is the human status suffix for --ask. Ask never caches, so unlike
// SummaryLine there is no cached/live label to carry.
func (status Status) AskLine() string {
	return "ask: " + status.Ask
}

// Alive reports whether the chat is a running seat, which is the only
// distinction a caller may act on without reading the state string.
func (status Status) Alive() bool {
	return status.State == StateWorking || status.State == StateIdle
}

// Line is the one-line human rendering.
func (status Status) Line() string {
	line := fmt.Sprintf(
		"%s\t%s\tidle=%ds\t%s",
		status.Name,
		status.State,
		status.IdleSeconds,
		status.Engine,
	)
	if status.Model != "" {
		line += "\t" + status.Model
	}
	if status.ContextPct > 0 {
		line += fmt.Sprintf("\tctx=%.0f%%", status.ContextPct)
	}
	if status.Last != "" {
		line += "\t" + status.Last
	}
	return line
}

// Inspect derives the status of a resolved chat at the given instant.
//
// working vs idle is decided by the TRANSCRIPT, not by a timer: a chat whose
// newest record is a tool call or a human turn owes an answer, and one whose
// newest record is the assistant speaking has delivered it. A long tool run
// therefore reads as working however quiet the file goes, which is the honest
// answer and the one a watcher must not mistake for finished.
func Inspect(
	ctx context.Context,
	chat Chat,
	now time.Time,
) (Status, error) {
	status := Status{
		Name:      chat.Name,
		Engine:    chat.Engine,
		CWD:       chat.CWD,
		SessionID: chat.ID,
		Socket:    chat.Socket,
		State:     StateDead,
	}
	if chat.Path == "" {
		// No transcript at all: a seat that never wrote a word. It is alive
		// only if its server is, and it has nothing to report either way.
		if chat.Live {
			status.State = StateWorking
		}
		return status, nil
	}
	meta, err := transcript.ReadMeta(chat.Path, string(chat.Engine))
	if err != nil {
		// A live engine can publish its fleet seat before it creates the first
		// rollout record. That is the same honest state as an empty Path: the
		// chat is working and has no transcript facts yet. A dead seat with the
		// same missing path remains an error because its evidence was lost.
		if chat.Live && errors.Is(err, fs.ErrNotExist) {
			status.State = StateWorking
			return status, nil
		}
		return status, fmt.Errorf("read chat transcript metadata %s: %w", chat.Path, err)
	}
	status.Model = meta.Model
	status.ContextPct = meta.ContextPercent()
	if meta.ModifiedUnixNS > 0 {
		idle := now.UnixNano() - meta.ModifiedUnixNS
		if idle < 0 {
			idle = 0
		}
		status.IdleSeconds = idle / int64(time.Second)
	}

	entries, _, err := transcript.Tail(ctx, chat.Path, string(chat.Engine), 1, transcript.TextCap)
	if err != nil {
		return status, fmt.Errorf("read chat transcript tail %s: %w", chat.Path, err)
	}
	if len(entries) > 0 {
		status.Last = transcript.Condensed(entries[len(entries)-1])
		if chat.Live {
			if assistantAnswered(entries[len(entries)-1].Role) {
				status.State = StateIdle
			} else {
				status.State = StateWorking
			}
		}
	} else if chat.Live {
		status.State = StateWorking
	}
	if chat.Live && chat.Engine == pfmengine.Claude && chat.ID != "" {
		sidechainWorking, err := newerClaudeSidechain(chat.Path, chat.ID, meta.ModifiedUnixNS)
		if err != nil {
			return status, err
		}
		if sidechainWorking {
			status.State = StateWorking
		}
	}
	if status.State != StateIdle {
		status.IdleSeconds = 0
	}
	return status, nil
}

func assistantAnswered(role string) bool {
	return role == transcript.RoleAssistant
}

func newerClaudeSidechain(transcriptPath, sessionID string, parentModifiedUnixNS int64) (bool, error) {
	directory := filepath.Join(filepath.Dir(transcriptPath), sessionID, "subagents")
	entries, err := os.ReadDir(directory)
	if errors.Is(err, fs.ErrNotExist) {
		if _, lstatErr := os.Lstat(directory); errors.Is(lstatErr, fs.ErrNotExist) {
			return false, nil
		} else if lstatErr != nil {
			return false, fmt.Errorf("inspect Claude sidechain directory %s: %w", directory, lstatErr)
		}
	}
	if err != nil {
		return false, fmt.Errorf("read Claude sidechain directory %s: %w", directory, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return false, fmt.Errorf("inspect Claude sidechain %s: %w", filepath.Join(directory, entry.Name()), err)
		}
		if info.ModTime().UnixNano() > parentModifiedUnixNS {
			return true, nil
		}
	}
	return false, nil
}

// Missing is the status of a name nothing answers to. It is a value, not an
// error, so every caller renders the same shape — and it is never silent.
func Missing(name string) Status {
	return Status{Name: name, State: StateMissing}
}
