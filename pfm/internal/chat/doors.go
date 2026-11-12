package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// A chat function has two doors, `pfm chat {verb}` and the chat MCP tool of the same name. Every outcome
// either door answers is decided here, once; a door only wraps it in its envelope (the CLI's stderr line and
// exit code, the MCP's receipt). A refusal is a pfmcall.Refusal, whose wording lives in package call.

// The status words of a refusal only these verbs answer.
const (
	statusRefused     = "refused"
	statusInProgress  = "in_progress"
	statusUnsupported = "unsupported"
)

// LiveSeatTarget is a live chat's seat address on either host.
func LiveSeatTarget(chat headless.Chat) (CallTarget, error) {
	socketPath := chat.Socket
	if chat.Backend != spawn.BackendHerdr {
		var err error
		if socketPath, err = paths.SocketPath(chat.Socket); err != nil {
			return CallTarget{}, err
		}
	}
	return CallTarget{Backend: chat.Backend, SocketPath: socketPath, Pane: PaneTarget(chat)}, nil
}

// liveSeat resolves name to a live chat and the call engine's door on its seat — the one address chat keys and
// chat capture reach. A name nothing answers to is pfmcall.UnknownChat, a chat with no running seat
// pfmcall.NotRunning; any other error means a step could not run.
func liveSeat(
	ctx context.Context, name string, runtime *pfmconfig.Runtime,
) (headless.Chat, *CallEngine, CallTarget, error) {
	chat, err := Target(ctx, name, runtime)
	if err != nil {
		return headless.Chat{}, nil, CallTarget{}, TargetRefusal(err, name)
	}
	if !chat.Live {
		return chat, nil, CallTarget{}, pfmcall.NotRunning(chat.Name)
	}
	door, err := NewCallEngineWith(CallDependencies{})
	if err != nil {
		return chat, nil, CallTarget{}, err
	}
	seat, err := LiveSeatTarget(chat)
	return chat, door, seat, err
}

// TargetRefusal is a verb's target failure as both doors answer it: a name nothing answers to is
// pfmcall.UnknownChat, a name several chats answer to an ambiguous refusal listing them; a scan that could not
// look stays the error it is.
func TargetRefusal(err error, name string) error {
	var ambiguous *headless.AmbiguousTargetError
	switch {
	case errors.Is(err, ErrUnknownChat):
		return pfmcall.UnknownChat(name)
	case errors.As(err, &ambiguous):
		return pfmcall.AmbiguousChat(ambiguous.Error())
	}
	return err
}

// ResolveRefusal is a resolve's outcome as both doors answer it, given the call engine's code and candidate detail
// for name: nil for one target, pfmcall.UnknownChat for none, pfmcall.AmbiguousChat listing the candidates for
// several; any other code is a resolve that could not run, an error naming it.
func ResolveRefusal(code int, detail, name string) error {
	switch code {
	case 0:
		return nil
	case pfmcall.CodeUnknown:
		return pfmcall.UnknownChat(name)
	case pfmcall.CodeAmbiguous:
		return pfmcall.AmbiguousChat(detail)
	}
	return fmt.Errorf("resolve target %q failed with code %d: %s", name, code, detail)
}

// CallAnswer is a call's outcome as both doors answer it, given the call engine's result for name. A result every
// live namespace missed is decided by the registry (DormantCallTarget): a dead chat's refusal, or dormant, the
// resume ladder's turn on ladderTarget, which only the CLI door runs (a door that cannot answers
// pfmcall.DeadResumable when ladderTarget is the chat's transcript); a name nothing answers to is pfmcall.UnknownChat's
// refusal.
func CallAnswer(
	ctx context.Context, result CallResult, name string, runtime *pfmconfig.Runtime,
) (answer CallResult, ladderTarget string, dormant bool) {
	answer, ladderTarget, dormant = DormantCallTarget(ctx, result, name, runtime)
	if answer.Code == pfmcall.CodeUnknown &&
		!strings.Contains(strings.ToLower(answer.Message), pfmcall.StatusAmbiguous) {
		unknown := pfmcall.UnknownChat(name)
		answer.Status, answer.Message = unknown.Status, unknown.Message
	}
	return answer, ladderTarget, dormant
}

// ResumableRefusal is a call result, for a door that runs no resume ladder, refusing a dead chat whose
// transcript takes a resumed turn (pfmcall.DeadResumable).
func ResumableRefusal(result CallResult, name string) CallResult {
	refusal := pfmcall.DeadResumable(name)
	result.Status, result.Code, result.Message = refusal.Status, refusal.Code, refusal.Message
	return result
}

// Captured is what chat capture read: the chat, its seat, the screen text and whether a tail cut it.
type Captured struct {
	Chat      headless.Chat
	Seat      CallTarget
	Text      string
	Truncated bool
}

// Capture reads a live chat's whole scrollback and keeps its last tail rows (TailRows); tail 0 or less keeps every
// row. A seat that could not be read is a pfmcall.StatusDead refusal naming why; tmux that never started
// (pfmtmux.CouldNotRun) is a read that could not look, an error, never a dead chat.
func Capture(ctx context.Context, runtime *pfmconfig.Runtime, name string, tail int) (Captured, error) {
	chat, door, seat, err := liveSeat(ctx, name, runtime)
	if err != nil {
		return Captured{Chat: chat}, err
	}
	text, err := CaptureSeat(ctx, door, seat, pfmcall.FullScrollback)
	if pfmtmux.CouldNotRun(err) {
		return Captured{Chat: chat, Seat: seat}, fmt.Errorf("could not run tmux to capture %q: %w", seat.Pane, err)
	}
	if err != nil {
		return Captured{Chat: chat, Seat: seat}, pfmcall.Refusal{
			Status: pfmcall.StatusDead, Code: pfmcall.CodeDead, Message: err.Error(),
		}
	}
	text, truncated := TailRows(text, tail)
	return Captured{Chat: chat, Seat: seat, Text: text, Truncated: truncated}, nil
}

// TailRows drops a capture's trailing blank rows — a screen's unpainted bottom, never its content — and keeps its
// last n rows, ending in a newline; n 0 or less keeps every row. cut says rows were dropped from the top.
func TailRows(text string, n int) (kept string, cut bool) {
	rows := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for len(rows) > 0 && strings.TrimSpace(rows[len(rows)-1]) == "" {
		rows = rows[:len(rows)-1]
	}
	if n > 0 && len(rows) > n {
		rows, cut = rows[len(rows)-n:], true
	}
	if len(rows) == 0 {
		return "", cut
	}
	return strings.Join(rows, "\n") + "\n", cut
}

// Keyed is what chat keys sent: the chat, its seat, the keys that landed, and whether every key landed (Landed
// stays true when only the read-back after them failed).
type Keyed struct {
	Chat   headless.Chat
	Seat   CallTarget
	Sent   KeysSent
	Landed bool
}

// PressKeys presses request's keys in a live chat (SendKeys). A seat that refused a key, or the read-back after them,
// is a refusal naming it (KeysRefusal); Sent still counts the keys that landed.
func PressKeys(ctx context.Context, runtime *pfmconfig.Runtime, name string, request KeysRequest) (Keyed, error) {
	chat, door, seat, err := liveSeat(ctx, name, runtime)
	if err != nil {
		return Keyed{Chat: chat}, err
	}
	sent, err := SendKeys(ctx, door, seat, request)
	keyed := Keyed{Chat: chat, Seat: seat, Sent: sent, Landed: err == nil || errors.Is(err, ErrKeyCapture)}
	return keyed, keysRefusal(err, request, sent)
}

// keysRefusal is SendKeys' failure as both doors answer it: a key this seat cannot press is refused (code 2), a key
// that did not land or a read-back that failed is pfmcall.StatusDead naming which; any other error stays itself.
func keysRefusal(err error, request KeysRequest, sent KeysSent) error {
	switch {
	case errors.Is(err, ErrKeyCapability):
		return pfmcall.Refusal{Status: statusRefused, Code: 2, Message: err.Error()}
	case errors.Is(err, ErrKeySend):
		return pfmcall.Refusal{
			Status: pfmcall.StatusDead, Code: pfmcall.CodeDead,
			Message: fmt.Sprintf("%s: %v", request.Keys[sent.Count], err),
		}
	case errors.Is(err, ErrKeyCapture):
		return pfmcall.Refusal{
			Status: pfmcall.StatusDead, Code: pfmcall.CodeDead, Message: fmt.Sprintf("capture: %v", err),
		}
	}
	return err
}

// LastRefusal is a last answer's failure as both doors answer it, given what LastAnswer returned; a failure
// that is no refusal (a step that could not run) stays the error it is.
func LastRefusal(err error, result LastResult, name string) error {
	var target *TargetError
	switch {
	case errors.As(err, &target):
		return TargetRefusal(err, name)
	case errors.Is(err, ErrNoTranscript) && result.Chat.Engine == pfmengine.OpenCode:
		return pfmcall.Refusal{Status: statusUnsupported, Code: 1, Message: fmt.Sprintf(
			"reading OpenCode session content is not supported (%q)", result.Chat.Name)}
	case errors.Is(err, ErrNoTranscript):
		return pfmcall.NoTranscript(result.Chat.Name)
	case errors.Is(err, ErrNoAnswer):
		return pfmcall.NotAnswered(result.Chat.Name)
	case errors.Is(err, ErrTurnInProgress):
		return pfmcall.Refusal{Status: statusInProgress, Code: 1, Message: err.Error()}
	}
	return err
}

// StatusCode is a status's code at both doors: 0 for a chat that is alive, pfmcall.CodeDead for one that is not.
func StatusCode(status headless.Status) int {
	if status.Alive() {
		return 0
	}
	return pfmcall.CodeDead
}
