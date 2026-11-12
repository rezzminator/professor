package harness

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// A terminal opens at the size an owner's window has and keeps this much raw output as evidence.
const (
	Cols       = 140
	Rows       = 40
	rawKeep    = 256 << 10
	waitBound  = 10 * time.Second
	closeGrace = 3 * time.Second
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

// Ctrl is Ctrl held with a letter: Ctrl('c') is the interrupt.
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

// WaitFor waits until text is on the screen; within defaults to 10 s.
func (t *Terminal) WaitFor(text string, within ...time.Duration) {
	t.c.t.Helper()
	t.WaitScreen(fmt.Sprintf("%q on the screen", text), bound(within), func(screen string) bool {
		return strings.Contains(screen, text)
	})
}

// WaitGone waits until text is no longer on the screen; within defaults to 10 s.
func (t *Terminal) WaitGone(text string, within ...time.Duration) {
	t.c.t.Helper()
	t.WaitScreen(fmt.Sprintf("%q gone from the screen", text), bound(within), func(screen string) bool {
		return !strings.Contains(screen, text)
	})
}

// WaitScreen waits until cond holds for the screen; a miss fails the step with what was awaited, the
// bound, the terminal's name and the whole screen.
func (t *Terminal) WaitScreen(what string, within time.Duration, cond func(screen string) bool) {
	t.c.t.Helper()
	deadline := t.s.clock.Now().Add(within)
	ticker := t.s.clock.NewTicker(PollTick)
	defer ticker.Stop()
	for {
		screen := t.s.screen()
		if cond(screen) {
			return
		}
		if !t.s.clock.Now().Before(deadline) {
			t.c.Failf("terminal %s: waited %s for %s; the screen:\n%s", t.s.name, within, what, framed(screen))
		}
		<-ticker.C()
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

func bound(within []time.Duration) time.Duration {
	if len(within) > 0 {
		return within[0]
	}
	return waitBound
}

// framed draws the screen between rules so its blank rows stay visible in a test log.
func framed(screen string) string {
	rule := strings.Repeat("─", Cols)
	return rule + "\n" + screen + "\n" + rule
}
