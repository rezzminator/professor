package chat

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/clock"
)

var namedKeys = map[string]bool{
	"Enter": true, "Escape": true, "Space": true, "Tab": true, "BTab": true,
	"BSpace": true, "Delete": true, "DC": true, "Insert": true, "IC": true,
	"Home": true, "End": true, "Up": true, "Down": true, "Left": true,
	"Right": true, "PageUp": true, "PPage": true, "PageDown": true,
	"NPage": true, "PgUp": true, "PgDn": true,
}

var (
	functionKey = regexp.MustCompile(`^F([1-9]|1[012])$`)
	modifier    = regexp.MustCompile(`^([CMS]-)+`)
)

// KeysDefaultDelay spaces consecutive keystrokes. A TUI still painting a
// frame drops a key that lands in the same millisecond as the one before it —
// the failure that leaves a delivered prompt sitting unsent in the composer —
// so the default gap is wide enough for a redraw.
const KeysDefaultDelay = 120 * time.Millisecond

// keysSettle lets the pane finish redrawing before a keys capture reads it.
const keysSettle = 400 * time.Millisecond

// The refusals SendKeys can answer, told apart with errors.Is; the error's
// own text stays the cause's, so every caller words its prefix as before.
var (
	// ErrKeyName: a key tmux would type as text, refused before any seat is touched.
	ErrKeyName = errors.New("key name refused")
	// ErrKeyCapability: the seat's backend cannot press one of the keys; nothing was sent.
	ErrKeyCapability = errors.New("key capability refused")
	// ErrKeySend: the seat refused a key mid-sequence; KeysSent.Count says how many landed.
	ErrKeySend = errors.New("key send failed")
	// ErrKeyCapture: every key landed, the read-back after them failed.
	ErrKeyCapture = errors.New("keys capture failed")
)

// keysFailure classifies cause under kind without changing its message.
type keysFailure struct{ kind, cause error }

func (failure keysFailure) Error() string   { return failure.cause.Error() }
func (failure keysFailure) Unwrap() []error { return []error{failure.kind, failure.cause} }

// SeatDoor is the call engine's per-seat door the keys and capture verbs
// reach a resolved pane through; *CallEngine is the production one.
type SeatDoor interface {
	SendKey(context.Context, CallTarget, string) error
	SendLiteral(context.Context, CallTarget, string) error
	CaptureTarget(context.Context, CallTarget, int) (string, error)
}

// KeysRequest is one keys verb call: what to press on a resolved seat and how.
type KeysRequest struct {
	Keys []string
	// Literal types each entry as text instead of pressing it as a key name.
	Literal bool
	// Delay is the pause between consecutive keys; zero or less sends them back to back.
	Delay time.Duration
	// Capture reads the visible screen once the keys land and the pane settles.
	Capture bool
	// Clock paces Delay and the settle; nil is the real clock.
	Clock clock.Clock
}

// KeysSent is what landed: the count of keys sent and, with Capture, the screen.
type KeysSent struct {
	Count  int
	Screen string
}

// KeyValid reports whether key satisfies the tmux key-name contract the chat
// keys verb and its MCP tool validate against. Unknown names must be rejected
// by callers before SendKey is invoked.
func KeyValid(key string) bool {
	if key == "" {
		return false
	}
	base := modifier.ReplaceAllString(key, "")
	if base == "" {
		return false
	}
	if base != key && len([]rune(base)) == 1 {
		return true
	}
	return namedKeys[base] || functionKey.MatchString(base)
}

// KeyNames returns the stable refusal-message suffix every keys door uses.
func KeyNames() string {
	names := make([]string, 0, len(namedKeys))
	for name := range namedKeys {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, " ") + " F1-F12, each optionally prefixed C- M- S-"
}

// Validate refuses words tmux would otherwise type as literal text,
// as ErrKeyName; a literal request has no names to refuse. Callers run it at
// data entry, before resolving a seat; SendKeys runs it again so no door can
// skip it.
func (request KeysRequest) Validate() error {
	if len(request.Keys) == 0 {
		return keysFailure{ErrKeyName, errors.New("keys must contain at least one key")}
	}
	if request.Literal {
		return nil
	}
	for _, key := range request.Keys {
		if !KeyValid(key) {
			return keysFailure{
				ErrKeyName,
				fmt.Errorf(
					"%q is not a tmux key — tmux would type it as text; send it literal (--literal, MCP literal=true) to mean that, or use one of: %s",
					key,
					KeyNames(),
				),
			}
		}
	}
	return nil
}

// SendKeys is the one keys verb: it validates the names and the seat's key
// capabilities before the first mutation, then presses (or types) each key
// Delay apart through door, and with Capture reads the settled screen.
// A pause the context cancels returns ctx's error unclassified.
func SendKeys(ctx context.Context, door SeatDoor, seat CallTarget, request KeysRequest) (KeysSent, error) {
	if err := request.Validate(); err != nil {
		return KeysSent{}, err
	}
	if !request.Literal {
		if err := pfmcall.ValidateKeyCapabilities(seat, request.Keys); err != nil {
			return KeysSent{}, keysFailure{ErrKeyCapability, err}
		}
	}
	clk := request.Clock
	if clk == nil {
		clk = clock.Real
	}
	var sent KeysSent
	for index, key := range request.Keys {
		if index > 0 && request.Delay > 0 {
			if err := clk.Sleep(ctx, request.Delay); err != nil {
				return sent, fmt.Errorf("pause before key %q: %w", key, err)
			}
		}
		send := door.SendKey
		if request.Literal {
			send = door.SendLiteral
		}
		if err := send(ctx, seat, key); err != nil {
			return sent, keysFailure{ErrKeySend, err}
		}
		sent.Count++
	}
	if !request.Capture {
		return sent, nil
	}
	if err := clk.Sleep(ctx, keysSettle); err != nil {
		return sent, fmt.Errorf("settle before capture: %w", err)
	}
	screen, err := CaptureSeat(ctx, door, seat, 0)
	if err != nil {
		return sent, keysFailure{ErrKeyCapture, err}
	}
	sent.Screen = screen
	return sent, nil
}

// CaptureSeat is the one read of a resolved seat: tailLines 0 is the visible
// screen, pfmcall.FullScrollback the whole joined history. The text always
// ends in a newline, so a printer never runs its last line into the next.
func CaptureSeat(ctx context.Context, door SeatDoor, seat CallTarget, tailLines int) (string, error) {
	text, err := door.CaptureTarget(ctx, seat, tailLines)
	if err != nil {
		return "", err
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text, nil
}
