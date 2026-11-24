package hookentry

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// seatExitLogLines and seatExitNoteLines bound the pane's last non-empty lines
// an engine's non-zero exit carries: the log entry keeps more, the seat's
// loss note, which `pfm chat ls` and `pfm restart` print, keeps the error.
const (
	seatExitLogLines  = 20
	seatExitNoteLines = 3
)

// seatExit is one engine run of a seat as it ended: its recipe, its seat, its
// exit status, how long it ran, how long its start window had been open (from
// the seat host's birth for a start inside the host's own first window, the
// instant pfm restart counts from, else from the engine's start), and whether
// the seat's end was already pending or the seat gone.
type seatExit struct {
	run    fleetdb.SeatRun
	seat   fleetdb.Seat
	status int
	ran    time.Duration
	window time.Duration
	ending bool
}

// seatStartWindowOpened is when an engine start's window opened: the seat
// host's wall birth (whole seconds, as chat.RestartBack reads it) while that
// birth's own window was still open at started, else started itself. A start
// pfm restart may already read back never counts as a failed one.
func seatStartWindowOpened(seat fleetdb.Seat, started time.Time) time.Time {
	if seat.SeatHostBirth <= 0 {
		return started
	}
	if birth := time.Unix(seat.SeatHostBirth, 0); started.Sub(birth) < pfmchat.SeatStartWindow {
		return birth
	}
	return started
}

// seatEngineExit logs an engine's non-zero exit, a terminal hangup aside, with
// the recipe's path words, the exit facts and the pane's last lines, read
// through screen while the pane still shows them. A resumed Claude that exited
// inside pfmchat.SeatStartWindow failed its start: the error it returns is the
// failed start's cause, so the seat host keeps the seat, its recipe and the
// pane; nil leaves the exit to close the seat as before. An exit by a signal
// (a status past 128: the seat host forwarded a TERM, INT or QUIT), one under
// a pending end, or one whose pane Herdr is taking away (its server stopping
// or the pane gone) was ended from outside, never a failed start.
func seatEngineExit(ctx context.Context, exit seatExit, screen func(context.Context) (string, error)) error {
	if exit.status == 0 || exit.status == 128+int(syscall.SIGHUP) {
		return nil
	}
	text, err := screen(ctx)
	var lines []string
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimRight(line, " \t\r"); strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	shown := strings.Join(lines[max(0, len(lines)-seatExitLogLines):], "\n")
	note := strings.Join(lines[max(0, len(lines)-seatExitNoteLines):], " | ")
	if err != nil {
		shown = "pane unreadable: " + err.Error()
		note = shown
	}
	obs.Logger(ctx).Warn("seat-host: engine exited non-zero",
		obs.FieldSeat, exit.seat.SeatID, obs.FieldEngine, exit.seat.Engine, "pane", exit.seat.HerdrPane,
		obs.FieldExit, exit.status, obs.FieldDur, exit.ran.Milliseconds(),
		"args", strings.Join(seatPathWords(exit.run.Args), "; "), "cause", shown)
	// shortcut: only a Claude resume marks a resumed start; a Codex or OpenCode resume closes as before,
	// upgrade when their restored recipes carry a resume word pfm reads.
	if !claudelaunch.ArgvResumesInPlace(exit.run.Args) || exit.window >= pfmchat.SeatStartWindow ||
		exit.status > 128 || exit.ending || herdr.ServerStopping(err) || herdr.PaneGone(err) {
		return nil
	}
	return fmt.Errorf("the engine exited %d after %s, inside its %s start window: %s",
		exit.status, exit.ran.Round(time.Millisecond), pfmchat.SeatStartWindow, note)
}

// seatPathWords names each absolute path a recipe's argv carries, after the
// word before it, and whether the file is there now.
func seatPathWords(args []string) []string {
	var words []string
	for index, arg := range args {
		if !filepath.IsAbs(arg) {
			continue
		}
		state := "present"
		if _, err := os.Stat(arg); os.IsNotExist(err) {
			state = "missing"
		} else if err != nil {
			state = "unreadable: " + err.Error()
		}
		word := arg + " (" + state + ")"
		if index > 0 {
			word = args[index-1] + " " + word
		}
		words = append(words, word)
	}
	return words
}

// seatScreen reads seat's pane through the call engine's visible-screen door.
func seatScreen(runtime config.Runtime, seat fleetdb.Seat) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		engine, err := pfmchat.NewCallEngine(false, &runtime)
		if err != nil {
			return "", fmt.Errorf("build the pane reader: %w", err)
		}
		return engine.CaptureScreen(ctx, pfmcall.Target{
			Backend: seat.Backend, SocketPath: seat.HerdrSocket, Pane: seat.HerdrPane, Engine: seat.Engine,
		})
	}
}
