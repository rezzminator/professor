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
// rather than as an empty screen. StateBlocked is likewise never Inspect's
// verdict: it is the pane rule's, decided in internal/chat on top of Inspect.
package headless

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// The states a chat can be in, as reported by status and watch.
const (
	StateWorking = "working"
	StateIdle    = "idle"
	// StateError is a live chat whose turn ENDED on an error — the model
	// server refused it — and that now sits at its prompt waiting for its
	// human. It is neither working (nothing runs) nor idle (it did not
	// answer), and Status.Error names the kind.
	StateError = "error"
	// StateBlocked is a live chat held by a permission dialog, question or
	// modal on its screen, waiting for its human. Set ONLY by internal/chat's
	// pane rule — Inspect itself never reads the pane.
	StateBlocked = "blocked"
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
	// is idle or error, both a turn that has ended. A working chat mid tool
	// run writes nothing for minutes; that silence is not idleness, and
	// reporting it as such made the number contradict the state beside it.
	IdleSeconds int64        `json:"idle_seconds"`
	Engine      pfmengine.ID `json:"engine"`
	Model       string       `json:"model,omitempty"`
	CWD         string       `json:"cwd,omitempty"`
	SessionID   string       `json:"session_id,omitempty"`
	Socket      string       `json:"socket,omitempty"`
	ContextPct  float64      `json:"context_pct,omitempty"`
	Last        string       `json:"last,omitempty"`
	// Error is the kind of the error the turn ended on (server_overloaded,
	// server_error, ...), set only when State is error.
	Error         string `json:"error,omitempty"`
	Summary       string `json:"summary,omitempty"`
	SummaryCached bool   `json:"summary_cached,omitempty"`
	Ask           string `json:"ask,omitempty"`

	// PendingTool and QuietSeconds are evidence for internal/chat's pane rule,
	// outside the JSON contract. PendingTool is the tool name of the newest
	// transcript entry when that entry is a tool call on a live chat.
	// QuietSeconds is how long the transcript has been unchanged, set for every
	// live chat with a transcript (IdleSeconds is zeroed while working).
	PendingTool  string `json:"-"`
	QuietSeconds int64  `json:"-"`
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
	return status.State == StateWorking || status.State == StateIdle ||
		status.State == StateError || status.State == StateBlocked
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
// newest record is the assistant speaking has delivered it — for Codex, whose
// rollout records each turn's start and end, only a task_complete or a
// turn_aborted ends the turn (applyCodexTurnRecord). A long tool run
// therefore reads as working however quiet the file goes, which is the honest
// answer and the one a watcher must not mistake for finished. A turn the
// model server ended on an error leaves that error as the newest record, so
// the chat reads as error, never as the tool call the error cut short.
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
		if chat.Live {
			status.QuietSeconds = status.IdleSeconds
		}
	}

	entries, _, err := transcript.Tail(ctx, chat.Path, string(chat.Engine), 1, transcript.TextCap)
	if err != nil {
		return status, fmt.Errorf("read chat transcript tail %s: %w", chat.Path, err)
	}
	if len(entries) > 0 {
		status.Last = transcript.Condensed(entries[len(entries)-1])
		if chat.Live {
			last := entries[len(entries)-1]
			if last.Role == transcript.RoleTool {
				status.PendingTool = last.Tool
			}
			switch {
			case assistantAnswered(last.Role) && last.Error != "":
				status.State = StateError
				status.Error = last.Error
			case assistantAnswered(last.Role):
				status.State = StateIdle
			default:
				status.State = StateWorking
			}
		}
	} else if chat.Live {
		status.State = StateWorking
	}
	if chat.Live && chat.Engine == pfmengine.Codex {
		applyCodexTurnRecord(&status, meta.CodexTurn)
	}
	if chat.Live && chat.Engine == pfmengine.Claude && chat.ID != "" {
		sidechainWorking, err := newerClaudeSidechain(chat.Path, chat.ID, meta.ModifiedUnixNS)
		if err != nil {
			return status, err
		}
		if sidechainWorking {
			status.State = StateWorking
			status.Error = ""
		}
	}
	if chat.Live && chat.Engine == pfmengine.Codex && chat.ID != "" &&
		(status.State == StateIdle || status.State == StateError) {
		subagentWorking, err := newerCodexSubagent(chat.Path, chat.ID, meta.ModifiedUnixNS)
		if err != nil {
			return status, err
		}
		if subagentWorking {
			status.State = StateWorking
			status.Error = ""
		}
	}
	if status.State != StateIdle && status.State != StateError {
		status.IdleSeconds = 0
	}
	return status, nil
}

// applyCodexTurnRecord lets a Codex rollout's own turn records overrule the
// newest-entry rule. Codex writes assistant commentary between the tool calls
// of one turn, so an assistant entry newest is not a turn that ended — only a
// task_complete or a turn_aborted is, however long the gap before the next
// tool call. A rollout holding no turn record keeps the newest-entry rule.
func applyCodexTurnRecord(status *Status, turn transcript.CodexTurnState) {
	switch turn {
	case transcript.CodexTurnOpen:
		status.State = StateWorking
		status.Error = ""
	case transcript.CodexTurnEnded:
		if status.State == StateWorking {
			// The turn ended after a tool call with no closing message.
			status.State = StateIdle
			status.PendingTool = ""
		}
	case transcript.CodexTurnUnrecorded:
		// No turn record to read: the newest-entry rule stands.
	}
}

func assistantAnswered(role string) bool {
	return role == transcript.RoleAssistant
}

// A sub-agent whose newest entry is an unanswered tool call does NOT count as
// working regardless of its mtime: a killed or crashed sub-agent leaves that
// call unanswered forever and would hold its parent at working forever — the
// crash-read-as-silence a watcher exists to prevent. The mtime rule's failure
// is a premature idle that the sub-agent's next write corrects. Both sidechain
// rules below stay on mtime alone for that reason.
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

// codexMaxLineageDepth bounds the parent walk of a nested Codex sub-agent.
const codexMaxLineageDepth = 8

var codexRolloutName = regexp.MustCompile(
	`^rollout-.*-([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl$`,
)

// newerCodexSubagent reports whether a sub-agent of the Codex thread parentID
// wrote a rollout after parentModifiedUnixNS: the parent then sits at its
// prompt while its sub-agent works, which is working, not idle.
//
// Codex writes sub-agent rollouts only into the sessions tree, under
// sessions/YYYY/MM/DD beside the parent's own — a parent whose rollout is not
// under such a day directory has no sub-agents this can find, and answers
// false. Every day directory from the parent's own day onward is scanned (a
// sub-agent started after midnight lands in a later day); a directory missing
// is skipped, any other read error is returned. A rollout counts when its
// session_meta is a sub-agent's and its parent chain reaches parentID
// (codexmeta.Decode is the one session_meta reader).
func newerCodexSubagent(transcriptPath, parentID string, parentModifiedUnixNS int64) (bool, error) {
	dayDir := filepath.Dir(transcriptPath)
	monthDir := filepath.Dir(dayDir)
	yearDir := filepath.Dir(monthDir)
	root := filepath.Dir(yearDir)
	if !numericName(filepath.Base(yearDir), 4) || !numericName(filepath.Base(monthDir), 2) ||
		!numericName(filepath.Base(dayDir), 2) {
		return false, nil
	}
	parentDay := filepath.Join(filepath.Base(yearDir), filepath.Base(monthDir), filepath.Base(dayDir))

	byID := map[string]string{}
	var newer []string
	years, err := numericSubdirs(root, 4)
	if err != nil {
		return false, err
	}
	for _, year := range years {
		if year < filepath.Base(yearDir) {
			continue
		}
		months, err := numericSubdirs(filepath.Join(root, year), 2)
		if err != nil {
			return false, err
		}
		for _, month := range months {
			if filepath.Join(year, month) < filepath.Dir(parentDay) {
				continue
			}
			days, err := numericSubdirs(filepath.Join(root, year, month), 2)
			if err != nil {
				return false, err
			}
			for _, day := range days {
				if filepath.Join(year, month, day) < parentDay {
					continue
				}
				dir := filepath.Join(root, year, month, day)
				files, err := os.ReadDir(dir)
				if errors.Is(err, fs.ErrNotExist) {
					continue
				}
				if err != nil {
					return false, fmt.Errorf("read Codex sessions directory %s: %w", dir, err)
				}
				for _, file := range files {
					match := codexRolloutName.FindStringSubmatch(file.Name())
					if file.IsDir() || match == nil {
						continue
					}
					path := filepath.Join(dir, file.Name())
					info, err := file.Info()
					if err != nil {
						return false, fmt.Errorf("inspect Codex rollout %s: %w", path, err)
					}
					byID[match[1]] = path
					if info.ModTime().UnixNano() > parentModifiedUnixNS && match[1] != parentID {
						newer = append(newer, path)
					}
				}
			}
		}
	}
	for _, path := range newer {
		reaches, err := codexSubagentReaches(path, parentID, byID)
		if err != nil {
			return false, err
		}
		if reaches {
			return true, nil
		}
	}
	return false, nil
}

// codexSubagentReaches walks one rollout's parent chain, at most
// codexMaxLineageDepth levels, and reports whether it ends at parentID.
func codexSubagentReaches(path, parentID string, byID map[string]string) (bool, error) {
	for depth := 0; depth < codexMaxLineageDepth; depth++ {
		header, ok, err := readCodexHeader(path)
		if err != nil || !ok {
			return false, err
		}
		if header.Kind != codexmeta.Subagent {
			return false, nil
		}
		if header.ParentThreadID == parentID {
			return true, nil
		}
		next, known := byID[header.ParentThreadID]
		if header.ParentThreadID == "" || !known {
			return false, nil
		}
		path = next
	}
	return false, nil
}

// readCodexHeader decodes a rollout's first line. ok is false for a file whose
// first line is not written yet (empty, or no newline): a rollout still being
// created says nothing about lineage and is not an error.
func readCodexHeader(path string) (header codexmeta.Header, ok bool, returnErr error) {
	file, err := os.Open(path)
	if err != nil {
		return codexmeta.Header{}, false, fmt.Errorf("open Codex rollout %s: %w", path, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close Codex rollout %s: %w", path, err))
		}
	}()
	line, err := bufio.NewReaderSize(file, 64<<10).ReadBytes('\n')
	if errors.Is(err, io.EOF) {
		return codexmeta.Header{}, false, nil
	}
	if err != nil {
		return codexmeta.Header{}, false, fmt.Errorf("read Codex rollout header %s: %w", path, err)
	}
	header, err = codexmeta.Decode(line)
	if err != nil {
		return codexmeta.Header{}, false, fmt.Errorf("decode Codex rollout header %s: %w", path, err)
	}
	return header, true, nil
}

func numericName(name string, width int) bool {
	if len(name) != width {
		return false
	}
	for _, character := range name {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

// numericSubdirs lists dir's subdirectories named by width digits, sorted. A
// directory that does not exist has none; any other read error is returned.
func numericSubdirs(dir string, width int) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Codex sessions directory %s: %w", dir, err)
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() && numericName(entry.Name(), width) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// Missing is the status of a name nothing answers to. It is a value, not an
// error, so every caller renders the same shape — and it is never silent.
func Missing(name string) Status {
	return Status{Name: name, State: StateMissing}
}
