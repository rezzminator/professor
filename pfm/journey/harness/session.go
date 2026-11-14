package harness

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
)

// session is one open terminal: the PTY's controller end, the login shell leading its session, and
// the screen emulator both pumps run through.
type session struct {
	name   string
	master *os.File
	cmd    *exec.Cmd
	emu    *vt.SafeEmulator
	clock  clock.Clock
	done   chan struct{} // closed once the shell has exited and been reaped

	mu  sync.Mutex
	raw []byte
}

// openSession starts shell as the leader of a new session on a fresh PTY, with env as its whole
// environment plus TERM, in env's HOME. creack/pty needs an *exec.Cmd and a session leader with a
// controlling terminal, which deps.RealRunner.Start cannot express (arch exception C22).
func openSession(name string, shell, env []string, clk clock.Clock) (*session, error) {
	if len(shell) == 0 {
		return nil, errors.New("no login shell: Hooks.Shell is empty")
	}
	cmd := exec.Command(shell[0], shell[1:]...)
	cmd.Env = append(append([]string(nil), env...), "TERM=xterm-256color")
	for _, entry := range env {
		if home, ok := strings.CutPrefix(entry, "HOME="); ok {
			cmd.Dir = home
		}
	}
	master, err := pty.StartWithAttrs(
		cmd, &pty.Winsize{Rows: Rows, Cols: Cols}, &syscall.SysProcAttr{Setsid: true, Setctty: true},
	)
	if err != nil {
		return nil, fmt.Errorf("start %q on a pty: %w", shell, err)
	}
	if master, err = pollable(master); err != nil {
		return nil, errors.Join(err, cmd.Process.Kill(), cmd.Wait())
	}
	s := &session{name: name, master: master, cmd: cmd, emu: vt.NewSafeEmulator(Cols, Rows), clock: clk}
	s.done = make(chan struct{})
	go s.pumpOut()
	go func() { _, _ = io.Copy(master, s.emu) }() // the emulator's replies: device attributes, cursor reports
	go func() {
		_ = cmd.Wait() // the shell's exit status is not evidence; its screen is
		close(s.done)
	}()
	return s, nil
}

// pollable reopens the PTY's controller end in non-blocking mode, so Go's poller owns it: a Close then
// wakes the blocked reader and releases the descriptor, which is the hang-up. creack/pty hands back a
// blocking file, whose Close waits behind a Read that never returns while the shell holds the terminal.
func pollable(master *os.File) (*os.File, error) {
	// The copy is close-on-exec, as the original was: a plain dup would hand the controller end to
	// every child started later, so a Close would hang nothing up and, on macOS, the shell would stay
	// stuck exiting behind output nobody reads.
	fd, err := unix.FcntlInt(master.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	if closeErr := master.Close(); err != nil || closeErr != nil {
		return nil, fmt.Errorf("reopen the pty: %w", errors.Join(err, closeErr))
	}
	if err := syscall.SetNonblock(fd, true); err != nil {
		return nil, errors.Join(fmt.Errorf("reopen the pty: %w", err), syscall.Close(fd))
	}
	return os.NewFile(uintptr(fd), "/dev/ptmx"), nil
}

// pumpOut feeds the PTY's output to the emulator and keeps its tail.
func (s *session) pumpOut() {
	buf := make([]byte, 32<<10)
	for {
		n, err := s.master.Read(buf)
		if n > 0 {
			s.mu.Lock()
			s.raw = append(s.raw, buf[:n]...)
			if over := len(s.raw) - rawKeep; over > 0 {
				s.raw = append([]byte(nil), s.raw[over:]...)
			}
			s.mu.Unlock()
			_, _ = s.emu.Write(buf[:n])
		}
		if err != nil {
			return // EIO once the last process holding the terminal end has gone, or the master closed
		}
	}
}

// close hangs the PTY up, gives the session closeGrace to end, then kills its process group.
func (s *session) close() error {
	hangup := s.master.Close()
	if s.ended(closeGrace) {
		return errors.Join(hangup, s.emu.Close())
	}
	err := syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
	if err != nil && !deps.GroupGone(s.cmd.Process.Pid, err) {
		return fmt.Errorf("kill the session of %s: %w", s.name, err)
	}
	if !s.ended(closeGrace) {
		return fmt.Errorf(
			"terminal %s: its shell (pid %d) outlived SIGKILL by %s",
			s.name,
			s.cmd.Process.Pid,
			closeGrace,
		)
	}
	return errors.Join(hangup, s.emu.Close())
}

func (s *session) ended(within time.Duration) bool {
	select {
	case <-s.done:
		return true
	case <-s.clock.After(within):
		return false
	}
}

func (s *session) screen() string {
	lines := strings.Split(s.emu.String(), "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}
