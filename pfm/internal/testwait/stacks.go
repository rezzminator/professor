package testwait

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

// Blocked reports whether some goroutine is blocked (any state goroutineWorking
// does not name) with frames as consecutive calls on its stack, innermost first,
// each matched as the suffix of a function name ("mcpserv.waitForProxyRequest",
// "mcpserv.(*stdioProxy).forwardRequest"). Its state names the matching
// goroutine, or how many goroutines hold the frames and in which states. It
// is a predicate for Until, never a wait of its own. A goroutine inside a
// system call counts as blocked, one that returns at once too: Blocked waits
// for a block that is sure to come and never proves that a call blocked.
func Blocked(frames ...string) (bool, string) {
	var unblocked []string
	all := Goroutines(true)
	for i := range all {
		g := &all[i]
		if !holdsFrames(g.Frames, frames) {
			continue
		}
		if !goroutineWorking(g.Status) {
			return true, g.Header
		}
		unblocked = append(unblocked, g.Status)
	}
	held := strings.Join(frames, " <- ")
	return false, fmt.Sprintf("%d goroutines hold %s, none blocked %v", len(unblocked), held, unblocked)
}

// Goroutine is one goroutine of a stack dump: its id, the id of the
// goroutine whose go statement started it (0 when none is printed), its
// header ("goroutine 7 [select]") and state ("select"), its frames innermost
// first, and the go statement that started it. internal/testjail's real-wait
// tripwire reads it to name the test a goroutine belongs to.
type Goroutine struct {
	ID, Creator    int64
	Header, Status string
	Frames         []Frame
	CreatedBy      Frame
}

// Frame is one call of a goroutine's stack: the function as a trace prints
// it, without its arguments ("chat.(*Share).Scan"), and its file and line.
type Frame struct {
	Function, File string
	Line           int
}

// Goroutines dumps the stack of the calling goroutine, first, and with all
// of every other goroutine of the process too.
func Goroutines(all bool) []Goroutine {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, all)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	var dump []Goroutine
	for _, trace := range strings.Split(string(buf), "\n\n") {
		lines := strings.Split(trace, "\n")
		header, status, ok := goroutineHeader(lines[0])
		if !ok {
			continue
		}
		g := Goroutine{ID: goroutineID(header), Header: header, Status: status}
		for i := 1; i < len(lines); i++ {
			line := lines[i]
			if line == "" || strings.HasPrefix(line, "\t") || strings.HasPrefix(line, "...") {
				continue
			}
			var frame Frame
			if function, found := strings.CutPrefix(line, "created by "); found {
				function, creator, _ := strings.Cut(function, " in goroutine ")
				g.Creator, _ = strconv.ParseInt(creator, 10, 64)
				frame.Function = function
			} else if cut := strings.LastIndex(line, "("); cut > 0 {
				frame.Function = line[:cut]
			} else {
				frame.Function = line
			}
			if i+1 < len(lines) && strings.HasPrefix(lines[i+1], "\t") {
				location, _, _ := strings.Cut(strings.TrimPrefix(lines[i+1], "\t"), " ")
				if colon := strings.LastIndex(location, ":"); colon > 0 {
					frame.File = location[:colon]
					frame.Line, _ = strconv.Atoi(location[colon+1:])
				}
			}
			if strings.HasPrefix(line, "created by ") {
				g.CreatedBy = frame
			} else {
				g.Frames = append(g.Frames, frame)
			}
		}
		dump = append(dump, g)
	}
	return dump
}

// goroutineHeader splits "goroutine 7 [select, 2 minutes]:" into the header
// without its colon and the state ("select").
func goroutineHeader(line string) (header, status string, ok bool) {
	open, end := strings.Index(line, " ["), strings.LastIndex(line, "]:")
	if !strings.HasPrefix(line, "goroutine ") || open < 0 || end < open {
		return "", "", false
	}
	status, _, _ = strings.Cut(line[open+2:end], ",")
	return strings.TrimSuffix(line, ":"), status, true
}

func holdsFrames(stack []Frame, frames []string) bool {
	functions := make([]string, len(stack))
	for i, frame := range stack {
		functions[i] = frame.Function
	}
	for start := 0; start+len(frames) <= len(functions); start++ {
		held := true
		for i, frame := range frames {
			if !strings.HasSuffix(functions[start+i], frame) {
				held = false
				break
			}
		}
		if held {
			return true
		}
	}
	return false
}

// driveFamily is the goroutines of one Drive: its roots, the goroutine that
// called Drive and the call's own, and every goroutine a member created,
// learned from the dumps' "created by … in goroutine N" lines across looks,
// so a member outlives its creator and a goroutine the test started before
// the call belongs too.
type driveFamily struct {
	members map[int64]bool
}

// driveWaits holds the innermost frames of the only system calls a member
// may sit in while the family is idle, each a call that can only wait for an
// outside event. A row joins when a test of this tree proves the need and the
// call can only wait (a wait on a child process, a file lock); a read, write,
// open, stat or sync never does: a member in any other system call is working.
var driveWaits = [][]string{
	{"os/signal.signal_recv"}, // signal.Notify's loop, waiting for the next signal
}

// goroutineWorking reports whether a goroutine in status goes on without an outside
// event: running or runnable, preempted, moving its stack, or held by the collector.
func goroutineWorking(status string) bool {
	switch status {
	case "running", "runnable", "preempted", "copystack",
		"GC assist wait", "GC assist marking", "garbage collection":
		return true
	}
	return false
}

// working names the goroutines keeping the family from idle in one dump, "" when
// none: a working member (goroutineWorking), a member in a system call driveWaits
// does not hold, and a working goroutine of an orphan line: one whose creator exited
// unseen (a grandchild of the call whose parent returned between two looks) or is
// not printed (running on another thread), and all it started, at any depth. An
// orphan line in a system call is not counted (another test's leftover blocked in a
// read would stall every Drive): the rule's one blind spot.
func (f *driveFamily) working(all []Goroutine) string {
	present := map[int64]bool{}
	for i := range all {
		present[all[i].ID] = true
	}
	for grew := true; grew; {
		grew = false
		for i := range all {
			if g := &all[i]; !f.members[g.ID] && f.members[g.Creator] {
				f.members[g.ID], grew = true, true
			}
		}
	}
	orphans := map[int64]bool{}
	for grew := true; grew; {
		grew = false
		for i := range all {
			g := &all[i]
			root := !present[g.Creator] && !f.members[g.Creator] && (g.Creator != 0 || len(g.Frames) == 0)
			if !orphans[g.ID] && !f.members[g.ID] && (root || orphans[g.Creator]) {
				orphans[g.ID], grew = true, true
			}
		}
	}
	var busy []string
	for i := range all {
		g := &all[i]
		switch {
		case i == 0: // the dump's own goroutine: this look
		case f.members[g.ID] && g.Status == "syscall" && !driveWaiting(g.Frames):
			busy = append(busy, g.Header)
		case goroutineWorking(g.Status) && (f.members[g.ID] || orphans[g.ID]):
			busy = append(busy, g.Header)
		}
	}
	return strings.Join(busy, ", ")
}

// driveWaiting reports whether a stack's innermost frames are a row of driveWaits.
func driveWaiting(stack []Frame) bool {
	for _, row := range driveWaits {
		if len(stack) >= len(row) && holdsFrames(stack[:len(row)], row) {
			return true
		}
	}
	return false
}

// currentGoroutine is the id of the goroutine calling it.
func currentGoroutine() int64 {
	buf := make([]byte, 64)
	header, _, _ := strings.Cut(string(buf[:runtime.Stack(buf, false)]), "\n")
	return goroutineID(header)
}

// goroutineID reads N from a "goroutine N [state]" header; 0 when it holds none.
func goroutineID(header string) int64 {
	fields := strings.Fields(header)
	if len(fields) < 2 {
		return 0
	}
	id, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return id
}
