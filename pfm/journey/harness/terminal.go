package harness

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// A terminal opens at the size an owner's window has and keeps this much raw output as evidence.
const (
	Cols    = 140
	Rows    = 40
	rawKeep = 256 << 10
)

// Key is a key's bytes as a terminal sends them.
type Key string

// The keys an owner presses besides text.
const (
	Enter     Key = "\r"
	Esc       Key = "\x1b"
	Tab       Key = "\t"
	Up        Key = "\x1b[A"
	Down      Key = "\x1b[B"
	Right     Key = "\x1b[C"
	Left      Key = "\x1b[D"
	Backspace Key = "\x7f"
)

// Ctrl is Ctrl pressed with a letter: Ctrl('c') is the interrupt.
func Ctrl(letter rune) Key { return Key(string(letter & 0x1f)) }

// Terminal is a terminal as one step drives it: a failed wait fails that step.
type Terminal struct {
	s *session
	c *Ctx
}

// Name is the terminal's name.
func (t *Terminal) Name() string { return t.s.name }

// Pid is the pid of the terminal's login shell.
func (t *Terminal) Pid() int { return t.s.cmd.Process.Pid }

// Type sends text as typed.
func (t *Terminal) Type(text string) {
	t.c.t.Helper()
	if _, err := t.s.master.WriteString(text); err != nil {
		t.c.Failf("type into terminal %s: %v", t.s.name, err)
	}
}

// typedPiece is the most bytes TypeDrawn sends before the receiver has drawn what came before it: Herdr
// forwards each typed key to its pane as one entry of a 1024-entry queue and drops every key that finds it
// full, so a longer burst typed while the pane's writer lags loses keys mid-text.
const typedPiece = 256

// TypeDrawn sends text as typed, in pieces of at most typedPiece bytes cut on rune boundaries; before each
// piece after the first it waits on the wait door until drawn holds for the text sent so far and the
// screen, so one piece at most is ever on its way. Nothing is awaited after the last piece: a text ending
// in Enter is submitted by it.
func (t *Terminal) TypeDrawn(text string, drawn func(typed, screen string) bool) {
	t.c.t.Helper()
	for start := 0; start < len(text); {
		end := min(start+typedPiece, len(text))
		for end < len(text) && !utf8.RuneStart(text[end]) {
			end--
		}
		if start > 0 {
			typed := text[:start]
			t.WaitScreen(fmt.Sprintf("the first %d typed bytes drawn", len(typed)), func(screen string) bool {
				return drawn(typed, screen)
			})
		}
		t.Type(text[start:end])
		start = end
	}
}

// Press sends keys in order.
func (t *Terminal) Press(keys ...Key) {
	t.c.t.Helper()
	var text strings.Builder
	for _, key := range keys {
		text.WriteString(string(key))
	}
	t.Type(text.String())
}

// Screen is the visible rows as text, as the owner sees them now, trailing blanks trimmed.
func (t *Terminal) Screen() string { return t.s.screen() }

// Raw is the tail of everything the terminal printed, escape sequences included.
func (t *Terminal) Raw() []byte {
	t.s.mu.Lock()
	defer t.s.mu.Unlock()
	return append([]byte(nil), t.s.raw...)
}

// WaitFor waits until text is on the screen.
func (t *Terminal) WaitFor(text string) {
	t.c.t.Helper()
	t.WaitScreen(fmt.Sprintf("%q on the screen", text), func(screen string) bool {
		return strings.Contains(screen, text)
	})
}

// WaitGone waits until text is no longer on the screen.
func (t *Terminal) WaitGone(text string) {
	t.c.t.Helper()
	t.WaitScreen(fmt.Sprintf("%q gone from the screen", text), func(screen string) bool {
		return !strings.Contains(screen, text)
	})
}

// WaitScreen waits on the wait door until cond holds for the screen; one still unmet past the door's
// hung-only limit fails the step with what was awaited, the terminal's name and the whole screen.
func (t *Terminal) WaitScreen(what string, cond func(screen string) bool) {
	t.c.t.Helper()
	from, screen := t.c.now(), ""
	label := "terminal " + t.s.name + ": " + what
	met := t.c.door(t.c.deadline, func(tb testing.TB) {
		testwait.Until(tb, label, func() (bool, string) {
			screen = t.s.screen()
			return cond(screen), framed(screen)
		})
	})
	t.c.noteWait(label, from, met)
	if !met {
		t.c.Failf(
			"terminal %s: waited for %s, past the hung-only limit; the screen:\n%s",
			t.s.name,
			what,
			framed(screen),
		)
	}
}

// Resize changes the terminal's size; the child sees SIGWINCH.
func (t *Terminal) Resize(cols, rows int) {
	t.c.t.Helper()
	t.s.emu.Resize(cols, rows)
	conn, err := t.s.master.SyscallConn()
	if err == nil {
		size := &unix.Winsize{Row: uint16(rows), Col: uint16(cols)}
		ctlErr := conn.Control(func(fd uintptr) { err = unix.IoctlSetWinsize(int(fd), unix.TIOCSWINSZ, size) })
		err = errors.Join(err, ctlErr)
	}
	if err != nil {
		t.c.Failf("resize terminal %s: %v", t.s.name, err)
	}
}

// framed draws the screen between rules so its blank rows stay visible in a test log.
func framed(screen string) string {
	rule := strings.Repeat("─", Cols)
	return rule + "\n" + screen + "\n" + rule
}
