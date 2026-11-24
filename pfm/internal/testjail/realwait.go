package testjail

// realwait.go: the real-wait tripwire (timing-free-tests ruling 13: in the unit tier, production code
// never waits on the real clock). It is always armed in a unit binary; the variable only names the
// report dir. Run installs a realWaitClock as clock.Real: Now passes through, and every Sleep,
// After, NewTimer and NewTicker records the test it ran under and the call site that asked, then
// waits on the clock it replaced, so every test runs as it would without it. Once the tests end the
// run fails when any wait was recorded, one `REAL-WAIT {package} {Test} {site}` line each on stderr,
// and, when PFM_TEST_REAL_WAIT names a directory, writes the same rows and a closing `checked {package}`
// line to {dir}/{package label}.txt for `arch-check.sh --real-wait`. A test on a fake (a Clock it hands over, or testwait.ProcessClock)
// never reaches it. Jail, e2e and journey binaries are not judged: their patience waits on real
// processes. The shipped binary never sees it: testjail is imported by _test.go files alone.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// realWaitEnv names the directory the tripwire writes its report to; unset or empty, it writes none.
const realWaitEnv = "PFM_TEST_REAL_WAIT"

// realWaitClock is clock.Real while the tripwire is armed.
type realWaitClock struct {
	next    clock.Clock // the clock it replaced, every wait's real one
	pkg     string      // this package's directory under the module root: internal/chat
	root    string      // the module root, which every site is written relative to
	report  string      // the report file; "" writes none
	mu      sync.Mutex
	waits   map[string]bool // "{package} {Test} {site}" rows
	ordered []string
}

// armRealWait installs the tripwire as clock.Real, through the wait door's swap, in every unit binary;
// its report is {dir}/{package label}.txt when PFM_TEST_REAL_WAIT names a directory, else none. A set
// variable is cleared so no process a test starts writes the same report. Nil outside a unit binary,
// whose verdict passes the code through.
func armRealWait() *realWaitClock {
	if !unitBinary {
		return nil
	}
	wd, root := moduleLocation()
	label := packageLabel(wd, root, buildInfoPath(), os.Args[0])
	report := ""
	if dir, set := (paths.OSEnv{}).Lookup(realWaitEnv); set {
		if dir != "" {
			report = filepath.Join(dir, label+".txt")
		}
		if err := os.Unsetenv(realWaitEnv); err != nil {
			warnSetup("REAL-WAIT clear %s: %v", realWaitEnv, err)
		}
	}
	pkg, err := filepath.Rel(root, wd)
	if root == "" || err != nil {
		pkg = label
	}
	wire := newRealWaitClock(nil, filepath.ToSlash(pkg), root, report)
	wire.next = testwait.SwapProcessClock(wire)
	return wire
}

func newRealWaitClock(next clock.Clock, pkg, root, report string) *realWaitClock {
	return &realWaitClock{next: next, pkg: pkg, root: root, report: report, waits: map[string]bool{}}
}

func (c *realWaitClock) Now() time.Time { return c.next.Now() }

func (c *realWaitClock) Sleep(ctx context.Context, d time.Duration) error {
	c.record()
	return c.next.Sleep(ctx, d)
}

func (c *realWaitClock) After(d time.Duration) <-chan time.Time {
	c.record()
	return c.next.After(d)
}

func (c *realWaitClock) NewTimer(d time.Duration) clock.Timer {
	c.record()
	return c.next.NewTimer(d)
}

func (c *realWaitClock) NewTicker(d time.Duration) clock.Ticker {
	c.record()
	return c.next.NewTicker(d)
}

// record notes the running test and the site that called a wait method, once per pair.
func (c *realWaitClock) record() {
	row := c.pkg + " " + realWaitTest() + " " + c.site()
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.waits[row] {
		c.waits[row] = true
		c.ordered = append(c.ordered, row)
	}
}

// site is the first frame above the tripwire outside pfm's clock package: the code that waited.
func (c *realWaitClock) site() string {
	pcs := make([]uintptr, 32)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(4, pcs)]) // past Callers, site, record, the method
	for {
		frame, more := frames.Next()
		if !strings.Contains(frame.Function, "/internal/clock.") {
			file := frame.File
			if rel, err := filepath.Rel(c.root, file); err == nil && c.root != "" && !strings.HasPrefix(rel, "..") {
				file = filepath.ToSlash(rel)
			}
			return fmt.Sprintf("%s:%d", file, frame.Line)
		}
		if !more {
			return "(no caller outside the clock package)"
		}
	}
}

// realWaitTest names the top-level test the calling goroutine belongs to: a Test frame on its own
// stack, else on the go statement that started it or on the stack of the goroutine that ran it,
// followed to the oldest living ancestor. A goroutine whose ancestry runs through one that exited
// before any Test frame is seen is "(unattributed: started by {function})"; one with no ancestor at
// all runs outside every test, "(no test)".
func realWaitTest() string {
	self := testwait.Goroutines(false)[0]
	if name, ok := realWaitTestFrame(self.Frames...); ok {
		return name
	}
	byID := map[int64]testwait.Goroutine{}
	all := testwait.Goroutines(true)
	for i := range all {
		byID[all[i].ID] = all[i]
	}
	seen := map[int64]bool{}
	for g := self; ; {
		if name, ok := realWaitTestFrame(g.CreatedBy); ok {
			return name
		}
		parent, alive := byID[g.Creator]
		if g.Creator == 0 || seen[g.Creator] {
			return "(no test)"
		}
		if !alive {
			return "(unattributed: started by " + g.CreatedBy.Function + ")"
		}
		if name, ok := realWaitTestFrame(parent.Frames...); ok {
			return name
		}
		seen[g.Creator], g = true, parent
	}
}

// realWaitTestFrame finds the frame of a test function in a _test.go file and returns its top-level
// name: "chat.TestScan.func1.2" is TestScan.
func realWaitTestFrame(frames ...testwait.Frame) (string, bool) {
	for _, frame := range frames {
		if !strings.HasSuffix(frame.File, "_test.go") {
			continue
		}
		name := frame.Function[strings.LastIndex(frame.Function, "/")+1:]
		_, name, _ = strings.Cut(name, ".")
		name, _, _ = strings.Cut(name, ".")
		rest, isTest := strings.CutPrefix(name, "Test")
		if first, _ := utf8.DecodeRuneInString(rest); isTest && (rest == "" || !unicode.IsLower(first)) {
			return name, true
		}
	}
	return "", false
}

// verdict restores the clock it replaced, reports every recorded wait and writes the report file;
// any wait fails the run. A nil tripwire passes code through.
func (c *realWaitClock) verdict(code int) int {
	if c == nil {
		return code
	}
	testwait.SwapProcessClock(c.next)
	c.mu.Lock()
	rows := append([]string(nil), c.ordered...)
	c.mu.Unlock()
	sort.Strings(rows)
	for _, row := range rows {
		warnSetup("REAL-WAIT %s", row)
	}
	if len(rows) > 0 {
		warnSetup("REAL-WAIT %d waits on the real clock in the unit tier: hand the code a clock.Fake "+
			"(timing-free-tests door 13)", len(rows))
		code = max(code, 1)
	}
	if c.report == "" {
		return code
	}
	text := strings.Join(append(rows, "checked "+c.pkg), "\n") + "\n"
	if err := os.MkdirAll(filepath.Dir(c.report), 0o755); err != nil {
		warnSetup("REAL-WAIT could not write its report: %v", err)
		return max(code, 1)
	}
	if err := os.WriteFile(c.report, []byte(text), 0o644); err != nil {
		warnSetup("REAL-WAIT could not write its report: %v", err)
		return max(code, 1)
	}
	return code
}
