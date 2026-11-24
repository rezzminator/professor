package harness

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// Ctx is a step's handle on its node: it fails the step, reads state, waits, keeps evidence and tears
// down what the step started.
type Ctx struct {
	t          *testing.T
	hooks      Hooks
	parent     *Ctx
	path       string
	concurrent bool
	now        func() time.Time
	unique     func(string) string
	window     [2]time.Time
	failed     bool
	waited     func(WaitTime) // the walker's record of every named wait; nil keeps none
	// deadline is the runner deadline the step's waits run under, t.Deadline; a harness test sets one
	// already past to reach a wait's miss without waiting (as testwait.Expired does for a context).
	deadline func() (time.Time, bool)

	mu        sync.Mutex
	terminals map[string]*session
	expected  []string
}

// Result is what a command run directly printed and how it exited.
type Result struct {
	Stdout, Stderr string
	Code           int
}

// stepAbort unwinds a failed step to the walker, which recovers it.
type stepAbort struct{}

func newCtx(
	t *testing.T,
	hooks Hooks,
	parent *Ctx,
	path string,
	concurrent bool,
	now func() time.Time,
	unique func(string) string,
) *Ctx {
	c := &Ctx{
		t: t, hooks: hooks, parent: parent, path: path, concurrent: concurrent, now: now, unique: unique,
		deadline: t.Deadline, terminals: map[string]*session{},
	}
	c.window[0] = now()
	return c
}

// over is the together node's window as one more Ctx on the parent's test: the log gate's view of
// every branch at once.
func (c *Ctx) over(t *testing.T) *Ctx {
	group := newCtx(t, c.hooks, c.parent, c.path, false, c.now, c.unique)
	group.window = [2]time.Time{c.window[0], c.now()}
	group.expected = c.Expected()
	group.waited = c.waited
	return group
}

// guard runs body and reports whether it failed: by Failf, or by a panic, which it records with its stack.
func (c *Ctx) guard(body func()) (failed bool) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(stepAbort); !ok {
				c.t.Errorf("%s: panic: %v\n%s", c.path, r, debug.Stack())
			}
			c.failed, failed = true, true
		}
	}()
	body()
	return c.failed
}

// Path is the node's path from the root, its names joined by "/".
func (c *Ctx) Path() string { return c.path }

// Concurrent reports whether this step runs beside other branches (under a together node).
func (c *Ctx) Concurrent() bool { return c.concurrent }

// Window is when the step (or a together node's whole set of branches) started and ended; the end is
// zero while the step runs.
func (c *Ctx) Window() (from, to time.Time) { return c.window[0], c.window[1] }

// Failed reports whether the step has failed so far.
func (c *Ctx) Failed() bool { return c.failed }

// Failf fails the step with a message and ends it; the walker then starts none of its children.
func (c *Ctx) Failf(format string, args ...any) {
	c.t.Helper()
	c.t.Errorf("%s: "+format, append([]any{c.path}, args...)...)
	c.failed = true
	panic(stepAbort{})
}

// Logf adds a line to the step's log.
func (c *Ctx) Logf(format string, args ...any) {
	c.t.Helper()
	c.t.Logf(format, args...)
}

// Read runs argv directly as the owner, with the suite's environment, to read state.
func (c *Ctx) Read(argv ...string) Result {
	c.t.Helper()
	ctx := testwait.Context(doorT{TB: c.t, deadline: c.deadline})
	result, err := deps.RealRunner{}.Run(ctx, argv, deps.RunOptions{Env: c.hooks.Env, ProcessGroup: true})
	if err != nil {
		c.Failf("read %q: %v", argv, err)
	}
	if ctx.Err() != nil {
		c.Failf("read %q: no answer within the hung-only limit %s", argv, testwait.HungLimit)
	}
	return Result{Stdout: string(result.Stdout), Stderr: string(result.Stderr), Code: result.ExitCode}
}

// Unique is a run-unique name for a resource this step creates.
func (c *Ctx) Unique(label string) string { return c.unique(label) }

// Expect declares an ERROR log record this step causes on purpose; the suite's log gate passes it.
func (c *Ctx) Expect(pattern string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.expected = append(c.expected, pattern)
}

// Expected is every pattern this step declared.
func (c *Ctx) Expected() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.expected...)
}

// Artifact saves data as evidence under the node's own directory; without an artifact directory it is
// dropped.
func (c *Ctx) Artifact(name string, data []byte) {
	if c.hooks.Artifacts == "" {
		return
	}
	dir := filepath.Join(c.hooks.Artifacts, filepath.FromSlash(c.path))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		c.t.Errorf("%s: artifact %s: %v", c.path, name, err)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		c.t.Errorf("%s: artifact %s: %v", c.path, name, err)
	}
}

// Cleanup registers teardown to run once this node's whole subtree has ended, failed or not: after
// every step under it kept its evidence and before its terminals close (t.Cleanup runs last in, first
// out, and the terminals' close was registered first). A failure inside it is reported, never a verdict.
func (c *Ctx) Cleanup(teardown func()) { c.t.Cleanup(func() { c.guard(teardown) }) }

// Terminal is the terminal named name: the one this node or an ancestor opened, or a new one owned by
// this node and closed when its subtree ends.
func (c *Ctx) Terminal(name string) *Terminal {
	c.t.Helper()
	for owner := c; owner != nil; owner = owner.parent {
		owner.mu.Lock()
		s := owner.terminals[name]
		owner.mu.Unlock()
		if s != nil {
			return &Terminal{s: s, c: c}
		}
	}
	s, err := openSession(name, c.hooks.Shell, c.hooks.Env)
	if err != nil {
		c.Failf("open terminal %s: %v", name, err)
	}
	c.mu.Lock()
	c.terminals[name] = s
	c.mu.Unlock()
	return &Terminal{s: s, c: c}
}

// Terminals is every terminal this node's path can see, nearest first, for evidence.
func (c *Ctx) Terminals() []*Terminal {
	var out []*Terminal
	for owner := c; owner != nil; owner = owner.parent {
		owner.mu.Lock()
		for _, s := range owner.terminals {
			out = append(out, &Terminal{s: s, c: c})
		}
		owner.mu.Unlock()
	}
	return out
}

func (c *Ctx) closeTerminals() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for name, s := range c.terminals {
		if err := s.close(c); err != nil {
			c.t.Errorf("%s: close terminal %s: %v", c.path, name, err)
		}
	}
	c.terminals = map[string]*session{}
}
