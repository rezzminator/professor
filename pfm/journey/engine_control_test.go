//go:build journey

package journey

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/mockengine/control"
	"github.com/rezzminator/professor/pfm/internal/mockengine/control/controltest"
	"github.com/rezzminator/professor/pfm/journey/harness"
)

// engineControlDir holds the fake engines' control FIFOs: prepare.sh's scenario names one per engine
// process, {pid} its process id.
func engineControlDir() string {
	return filepath.Join(ownerHome, ".local", "share", "journey", "control")
}

// engineControl is the control FIFO the fake engine with process id pid reads.
func engineControl(pid int) string {
	return control.ForPID(filepath.Join(engineControlDir(), control.PIDToken), pid)
}

// sessionEngine waits for session's one engine and returns its pid.
func sessionEngine(c *harness.Ctx, session string) int {
	pid := 0
	c.Poll("one engine of session "+session, func() (bool, string) {
		pid = liveEngine(c, session)
		return pid != 0, fmt.Sprintf("engines %v", engines(processes(c)))
	})
	return pid
}

// advanceEngine moves the clock of the fake engine pid by d: a busy stretch of d or less ends.
func advanceEngine(c *harness.Ctx, pid int, d time.Duration) {
	fifo := engineControl(pid)
	c.Control("fake engine "+strconv.Itoa(pid)+" to move its clock "+d.String()+" ("+fifo+")", func(t testing.TB) {
		controltest.AdvanceBy(t, fifo, d)
	})
}

// tickEngine moves the clock of the fake engine reading fifo to its earliest armed wait (a Claude pane's
// statusline refresh tick while it idles) and returns how far it moved.
func tickEngine(c *harness.Ctx, fifo string) time.Duration {
	var moved time.Duration
	c.Control("the fake engine of "+fifo+" to fire its next wait", func(t testing.TB) {
		moved = controltest.Next(t, fifo)
	})
	return moved
}

// nudgeEngine hands the looking wait of the fake engine pid one look: a hold or a background agent
// whose file is gone ends on it.
func nudgeEngine(c *harness.Ctx, pid int) {
	fifo := engineControl(pid)
	c.Control("fake engine "+strconv.Itoa(pid)+" to take a look ("+fifo+")", func(t testing.TB) {
		controltest.Nudge(t, fifo)
	})
}

// crashReader waits until a fake engine other than the one whose pid is previous holds fifo, and returns
// its pid: the one reader a command sent now reaches.
func crashReader(c *harness.Ctx, fifo, previous string) string {
	reader := ""
	c.Poll("a fake engine other than "+previous+" to hold "+fifo, func() (bool, string) {
		r := c.Read("cat", control.ReaderPath(fifo))
		reader = strings.TrimSpace(r.Stdout)
		return r.Code == 0 && reader != "" &&
				reader != previous, fmt.Sprintf(
				"%s holds pid %q (exit %d)",
				control.ReaderPath(fifo),
				reader,
				r.Code,
			)
	})
	return reader
}
