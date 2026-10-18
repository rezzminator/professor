package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

// PaneCapture reads one live chat's pane. It is the seam InspectSeat crosses to
// answer working-vs-idle for a chat whose transcript cannot answer it, and
// blocked for a silent pending tool call; nil selects the real tmux capture.
type PaneCapture func(ctx context.Context, socketPath, target string) (string, error)

// PaneTarget is the ONE ladder from a resolved chat to the tmux target its
// pane is addressed by: the pane id, else the session name, else the socket
// itself. Shared by `pfm chat capture` and Status's own capture, because two
// copies is exactly how one of them ends up reading somebody else's pane.
func PaneTarget(chat headless.Chat) string {
	if chat.Pane != "" {
		return chat.Pane
	}
	if chat.Session != "" {
		return chat.Session
	}
	return chat.Socket
}

// needsPaneState reports whether Inspect's verdict for this chat rests on no
// transcript evidence at all: a live seat whose engine writes no transcript
// this process can read (OpenCode), or one whose transcript exists but holds
// no turn yet (a chat still sitting at an empty prompt). headless.Inspect
// answers StateWorking for both — the honest answer given only a file, and a
// wrong one about a chat that is plainly waiting for its human. A Codex
// rollout whose turn record leaves its newest turn open is that evidence even
// with no entry for Last (only task_started so far): its screen between two
// tool calls shows no footer, and read as idle it hid a working seat.
func needsPaneState(chat headless.Chat, status headless.Status) (bool, error) {
	if !chat.Live || (chat.Path != "" && status.Last != "") {
		return false, nil
	}
	open, err := codexTurnOpen(chat)
	return err == nil && !open, err
}

// codexTurnOpen says whether a Codex chat's rollout leaves its newest turn
// running (transcript.Meta.CodexTurn) — the end-record rule headless applies
// in Inspect and Await. Any other engine, a chat with no transcript, a rollout
// not created yet and one holding no turn record are not open. A rollout that
// could not be read is an error, never "not open": a failed read must not
// render as idle or as a finished answer.
func codexTurnOpen(chat headless.Chat) (bool, error) {
	if chat.Engine != pfmengine.Codex || chat.Path == "" {
		return false, nil
	}
	meta, err := transcript.ReadMeta(chat.Path, string(pfmengine.Codex))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s Codex turn records %s: %w", chat.Name, chat.Path, err)
	}
	return meta.CodexTurn == transcript.CodexTurnOpen, nil
}

// blockedQuietSeconds is how long a pending tool call must have been silent
// before its pane is read for a dialog. A turn mid-stream writes continuously,
// so only a tool call quiet past this reads the pane — which bounds `pfm chat
// ls` to one capture per silent pending seat.
const blockedQuietSeconds = 5

// awaitsPane reports whether Inspect's working verdict for a live chat is one
// only its screen can confirm: its newest transcript entry is a tool call, and
// the transcript has been quiet long enough that a permission dialog, not the
// tool, may be what it waits on.
func awaitsPane(chat headless.Chat, status headless.Status) bool {
	return chat.Live && status.State == headless.StateWorking &&
		status.PendingTool != "" && status.QuietSeconds >= blockedQuietSeconds
}

// paneState is the pane-evidence verdict: a dialog (heldByDialog) holds the
// seat for its human, otherwise the engine's own running-turn footer, read by
// that engine's own rule.
func paneState(engine pfmengine.ID, capture string) string {
	if heldByDialog(capture) {
		return headless.StateBlocked
	}
	if pfmcall.IsBusyFor(engine, capture) {
		return headless.StateWorking
	}
	return headless.StateIdle
}

// heldByDialog is the positive evidence a screen waits on its human: an open
// numbered selector at the active composer row (pfmcall.SelectorLine — a
// permission dialog, a question, a modal menu) or Claude's folder-trust
// dialog. The absence of a running-turn footer is NOT that evidence: a
// request the model server refused retries with no spinner arm IsBusyFor
// knows, under an empty composer, and read as blocked it sent a seat nobody
// needed to answer to its human.
func heldByDialog(screen string) bool {
	return pfmcall.SelectorLine(screen) != "" || pfmengine.ClaudeTrustDialog(screen)
}

// readPane captures one live chat's screen through the capture seam (nil is
// the real tmux read). A capture that could not RUN comes back as an ERROR and
// never as a state: "we failed to look" rendered as "the chat is idle" is the
// exact confusion headless refuses everywhere else.
func readPane(
	ctx context.Context,
	chat headless.Chat,
	capture PaneCapture,
	runtime *pfmconfig.Runtime,
) (string, error) {
	if capture == nil {
		capture = func(ctx context.Context, socketPath, target string) (string, error) {
			return (pfmcall.TmuxCaller{}).Capture(ctx, socketPath, target, false, pfmcall.FullScrollback)
		}
	}
	values, err := NameResolver{Runtime: runtime}.paths()
	if err != nil {
		return "", fmt.Errorf("resolve %s socket directory for a pane read: %w", chat.Name, err)
	}
	socketPath, err := values.SocketUnder(chat.Socket)
	if err != nil {
		return "", fmt.Errorf("resolve %s socket %q: %w", chat.Name, chat.Socket, err)
	}
	screen, err := capture(ctx, socketPath, PaneTarget(chat))
	if err != nil {
		return "", fmt.Errorf("read %s pane for status: %w", chat.Name, err)
	}
	return screen, nil
}

// statusFromPane replaces a transcript-less live chat's state with what its
// own screen says.
func statusFromPane(
	ctx context.Context,
	chat headless.Chat,
	status headless.Status,
	capture PaneCapture,
	runtime *pfmconfig.Runtime,
) (headless.Status, error) {
	if chat.Backend == spawn.BackendHerdr {
		return statusFromHerdr(ctx, chat, status)
	}
	screen, err := readPane(ctx, chat, capture, runtime)
	if err != nil {
		return status, err
	}
	status.State = paneState(chat.Engine, screen)
	// Inspect derives IdleSeconds from the transcript's mtime, which is the
	// very evidence this path exists because there is none of.
	status.IdleSeconds = 0
	return status, nil
}

// blockedFromPane confirms a silent pending tool call against the screen: a
// dialog on it (heldByDialog) holds the chat for its human, so it is blocked;
// any other screen — a tool still running, a request retrying — stays working.
func blockedFromPane(
	ctx context.Context,
	chat headless.Chat,
	status headless.Status,
	capture PaneCapture,
	runtime *pfmconfig.Runtime,
) (headless.Status, error) {
	if chat.Backend == spawn.BackendHerdr {
		native, err := statusFromHerdr(ctx, chat, status)
		if err != nil || native.State == headless.StateBlocked {
			return native, err
		}
		return status, nil
	}
	screen, err := readPane(ctx, chat, capture, runtime)
	if err != nil {
		return status, err
	}
	if !heldByDialog(screen) {
		return status, nil
	}
	status.State = headless.StateBlocked
	status.IdleSeconds = 0
	return status, nil
}

// nativeStatusObservation is one validated native snapshot, shared with this
// watch's idle clock. AgentSession mirrors the producer's identity fields.
type nativeStatusObservation struct {
	TranscriptRest bool   `json:"-"`
	Pane           string `json:"pane_id"`
	State          string `json:"agent_status"`
	Terminal       string `json:"terminal_id"`
	StateChangeSeq uint64 `json:"state_change_seq"`
	AgentSession   *struct {
		Source string `json:"source"`
		Agent  string `json:"agent"`
		Kind   string `json:"kind"`
		Value  string `json:"value"`
	} `json:"agent_session"`
}

// statusFromHerdr reconciles live native activity with transcript turn evidence.
func statusFromHerdr(
	ctx context.Context,
	chat headless.Chat,
	status headless.Status,
	observations ...*nativeStatusObservation,
) (headless.Status, error) {
	body, err := herdr.SeatRequest(ctx, chat.Socket, "agent.get", map[string]string{"target": chat.Pane})
	if err != nil {
		return status, fmt.Errorf("read %s Herdr state: %w", chat.Name, err)
	}
	var result struct {
		Agent nativeStatusObservation `json:"agent"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return status, fmt.Errorf("decode Herdr state for %s: %w", chat.Pane, err)
	}
	if result.Agent.Pane != chat.Pane {
		return status, fmt.Errorf("herdr state for %s returned pane %q", chat.Pane, result.Agent.Pane)
	}
	result.Agent.TranscriptRest = status.State == headless.StateIdle || status.State == headless.StateError
	switch result.Agent.State {
	case "idle", "done":
		if status.State != headless.StateIdle && status.State != headless.StateError {
			status.State = headless.StateIdle
			status.IdleSeconds = 0
			status.Error = ""
		}
	case "working", "blocked":
		status.State = result.Agent.State
		status.IdleSeconds = 0
		status.Error = ""
	case "unknown":
		status.StateSource = "transcript (Herdr unknown)"
	default:
		return status, fmt.Errorf("herdr pane %s has unknown state %q", chat.Pane, result.Agent.State)
	}
	for _, observation := range observations {
		if observation != nil {
			*observation = result.Agent
		}
	}
	return status, nil
}
