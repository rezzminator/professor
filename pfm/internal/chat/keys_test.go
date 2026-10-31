package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/clock"
)

// TestKeyValidAcceptsTheKeysTmuxPresses keeps the tmux key-name gate from
// rejecting a key a caller can use or accepting a token tmux would type.
func TestKeyValidAcceptsTheKeysTmuxPresses(t *testing.T) {
	for _, key := range []string{
		"Enter", "Escape", "Tab", "BTab", "BSpace", "Space", "Up", "Down",
		"Left", "Right", "Home", "End", "PageUp", "PageDown", "F1", "F12",
		"C-c", "C-o", "M-x", "S-Tab", "C-M-a",
	} {
		if !KeyValid(key) {
			t.Fatalf("%q rejected, want it accepted", key)
		}
	}
	for _, key := range []string{"F0", "F13", "Esc", "Ctrl-C", "C-", ""} {
		if KeyValid(key) {
			t.Fatalf("%q accepted, want it rejected", key)
		}
	}
}

// keysDoor records every press, type and read the keys verb makes; failAt
// refuses the send at that index, captureErr the read-back.
type keysDoor struct {
	calls      []string
	failAt     int
	captureErr error
}

func (door *keysDoor) send(kind, key string) error {
	if door.failAt > 0 && len(door.calls) == door.failAt {
		return errors.New("pane gone")
	}
	door.calls = append(door.calls, kind+":"+key)
	return nil
}

func (door *keysDoor) SendKey(_ context.Context, _ CallTarget, key string) error {
	return door.send("key", key)
}

func (door *keysDoor) SendLiteral(_ context.Context, _ CallTarget, text string) error {
	return door.send("text", text)
}

func (door *keysDoor) CaptureTarget(_ context.Context, _ CallTarget, lines int) (string, error) {
	door.calls = append(door.calls, fmt.Sprintf("capture:%d", lines))
	return "screen", door.captureErr
}

// keysClock records every pause instead of sleeping it.
type keysClock struct {
	clock.Clock
	pauses []time.Duration
}

func (fake *keysClock) Sleep(_ context.Context, d time.Duration) error {
	fake.pauses = append(fake.pauses, d)
	return nil
}

// TestSendKeysIsTheOneKeysVerb pins what every keys door (CLI and MCP) gets
// from the verb: names refused before the seat is touched, a backend's
// capability refusal before the first key, keys paced Delay apart, the count
// that landed when the seat dies mid-sequence, and the settled read-back.
func TestSendKeysIsTheOneKeysVerb(t *testing.T) {
	herdr := CallTarget{Backend: pfmcall.HerdrBackend, SocketPath: "fixture.sock", Pane: "w1:p1"}
	tmux := CallTarget{SocketPath: "fixture", Pane: "%1"}
	for _, test := range []struct {
		name       string
		seat       CallTarget
		request    KeysRequest
		failAt     int
		captureErr error
		kind       error
		count      int
		calls      string
		pauses     string
	}{
		{name: "name refused", seat: tmux, request: KeysRequest{Keys: []string{"Enter", "Esc"}}, kind: ErrKeyName},
		{name: "no keys", seat: tmux, request: KeysRequest{Literal: true}, kind: ErrKeyName},
		{
			name: "literal skips names", seat: tmux,
			request: KeysRequest{Keys: []string{"Esc", "hi"}, Literal: true, Delay: time.Millisecond},
			count:   2, calls: "text:Esc,text:hi", pauses: "1ms",
		},
		{
			name: "herdr capability refused", seat: herdr,
			request: KeysRequest{Keys: []string{"Enter", "Home"}}, kind: ErrKeyCapability,
		},
		{
			name: "paced and captured", seat: tmux,
			request: KeysRequest{Keys: []string{"Escape", "Enter"}, Delay: 5 * time.Millisecond, Capture: true},
			count:   2, calls: "key:Escape,key:Enter,capture:0", pauses: "5ms,400ms",
		},
		{
			name: "dies mid-sequence", seat: tmux, failAt: 1,
			request: KeysRequest{Keys: []string{"Escape", "Enter", "Tab"}},
			kind:    ErrKeySend, count: 1, calls: "key:Escape",
		},
		{
			name: "capture fails after every key", seat: tmux, captureErr: errors.New("unreadable"),
			request: KeysRequest{Keys: []string{"Enter"}, Capture: true},
			kind:    ErrKeyCapture, count: 1, calls: "key:Enter,capture:0", pauses: "400ms",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			door := &keysDoor{failAt: test.failAt, captureErr: test.captureErr}
			clk := &keysClock{}
			test.request.Clock = clk
			sent, err := SendKeys(context.Background(), door, test.seat, test.request)
			if test.kind == nil && err != nil || test.kind != nil && !errors.Is(err, test.kind) {
				t.Fatalf("err = %v, want kind %v", err, test.kind)
			}
			var pauses []string
			for _, pause := range clk.pauses {
				pauses = append(pauses, pause.String())
			}
			if sent.Count != test.count || strings.Join(door.calls, ",") != test.calls ||
				strings.Join(pauses, ",") != test.pauses {
				t.Fatalf("sent=%+v calls=%v pauses=%v, want count %d calls %q pauses %q",
					sent, door.calls, pauses, test.count, test.calls, test.pauses)
			}
			if test.request.Capture && test.kind == nil && sent.Screen != "screen\n" {
				t.Fatalf("screen = %q, want the read ending in a newline", sent.Screen)
			}
		})
	}
}
