// Package harness walks a tree of owner steps on Go's own test runner: one node is one t.Run, a
// together node's children run as parallel subtests, a failed step blocks its own descendants and
// nothing else, and the run ends with a timeline. It knows nothing of the product it drives.
//
// Entry points: Do, Together and Run. The other files:
//
//   - ctx.go: Ctx, the step's handle on its node — Read, Poll, Unique, Expect, Artifact, Failf, Cleanup.
//   - terminal.go: Terminal, a kernel PTY running the owner's login shell read through a screen
//     emulator — Type, Press, Screen, WaitFor, WaitGone, WaitScreen, Resize.
//   - session.go: the open terminal under a Terminal — the PTY, its login shell's session, the
//     emulator's pumps and the hang-up.
package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// Node is one step of the tree and the steps that start from the state it returns.
type Node[S any] struct {
	Name     string          // [a-z0-9-]+, unique among siblings
	Step     func(*Ctx, S) S // nil: a node that only groups
	Together bool            // its children run concurrently
	Children []Node[S]
}

// Do is a node whose children run in order, each from the state its predecessor's subtree started with.
func Do[S any](name string, step func(*Ctx, S) S, children ...Node[S]) Node[S] {
	return Node[S]{Name: name, Step: step, Children: children}
}

// Together is a node whose children run concurrently, each from this node's output state.
func Together[S any](name string, step func(*Ctx, S) S, children ...Node[S]) Node[S] {
	return Node[S]{Name: name, Step: step, Together: true, Children: children}
}

// Hooks is everything the suite hands the walker.
type Hooks struct {
	// Env is the whole environment of every terminal and every Read: "NAME=value" entries.
	Env []string
	// Shell is the argv of a terminal's login shell, e.g. {"/usr/bin/zsh", "-l", "-i"}.
	Shell []string
	// Artifacts is the directory evidence and timeline.tsv land in; empty keeps none.
	Artifacts string
	// Budget bounds the whole tree's wall time, first step start to last step end; zero: none.
	Budget time.Duration
	// AfterStep runs after every step with its verdict; under a together node it runs again once over
	// the together node's whole window, with Window spanning every branch (Ctx.Concurrent is false).
	AfterStep func(c *Ctx, failed bool)
	// Clock measures and waits; nil is the real clock.
	Clock clock.Clock
}

var nameShape = regexp.MustCompile(`^[a-z0-9-]+$`)

// record is one node's line of the timeline.
type record struct {
	path    string
	verdict string // PASS, FAIL, BLOCKED
	start   time.Duration
	seconds float64
}

// walker holds one Run's shared state.
type walker[S any] struct {
	hooks   Hooks
	clock   clock.Clock
	runID   string
	started time.Time
	mu      sync.Mutex
	records []record
	uniques map[string]int
}

// Run validates root, then walks it under t from start: every node a subtest named for it.
func Run[S any](t *testing.T, root Node[S], start S, hooks Hooks) {
	t.Helper()
	if err := validate(root, root.Name); err != nil {
		t.Fatalf("tree: %v", err)
	}
	w := &walker[S]{hooks: hooks, clock: hooks.Clock, uniques: map[string]int{}}
	if w.clock == nil {
		w.clock = clock.Real
	}
	w.started = w.clock.Now()
	w.runID = fmt.Sprintf("%x", w.started.UnixNano()&0xffffff)
	w.node(t, nil, root, start, root.Name, false)
	elapsed := w.clock.Now().Sub(w.started)
	w.report(t)
	if hooks.Budget > 0 && elapsed > hooks.Budget {
		t.Errorf("budget: the tree took %.1fs, allowed %.0fs", elapsed.Seconds(), hooks.Budget.Seconds())
	}
}

func validate[S any](node Node[S], path string) error {
	if !nameShape.MatchString(node.Name) {
		return fmt.Errorf("node %q at %s: a name is [a-z0-9-]+", node.Name, path)
	}
	seen := map[string]bool{}
	for _, child := range node.Children {
		if seen[child.Name] {
			return fmt.Errorf("node %q under %s: two siblings share the name", child.Name, path)
		}
		seen[child.Name] = true
		if err := validate(child, path+"/"+child.Name); err != nil {
			return err
		}
	}
	return nil
}

// node runs one node as a subtest of t. A together node's window check runs here, on t, once every
// branch has finished: Go resumes parallel subtests only after their parent's body returns.
func (w *walker[S]) node(t *testing.T, parent *Ctx, node Node[S], in S, path string, parallel bool) {
	var c *Ctx
	t.Run(node.Name, func(t *testing.T) {
		if parallel {
			t.Parallel()
		}
		c = newCtx(t, w.hooks, parent, path, parallel || parent != nil && parent.concurrent, w.clock, w.unique)
		t.Cleanup(c.closeTerminals) // after the whole subtree, parallel branches included
		out, failed := in, false
		if node.Step != nil {
			out, failed = runStep(c, node.Step, in)
			c.window[1] = w.clock.Now()
			if w.hooks.AfterStep != nil {
				after := c.guard(func() { w.hooks.AfterStep(c, failed) })
				failed = failed || after
			}
		}
		if node.Step == nil {
			c.window[1] = w.clock.Now()
		}
		w.record(path, verdict(failed), c.window[0], c.window[1])
		if failed {
			if names := descendants(node.Children, path); len(names) > 0 {
				t.Logf("BLOCKED %d steps under %s: %s", len(names), path, strings.Join(names, ", "))
				for _, name := range names {
					w.record(name, "BLOCKED", c.window[1], c.window[1])
				}
			}
			return
		}
		for _, child := range node.Children {
			w.node(t, c, child, out, path+"/"+child.Name, node.Together)
		}
	})
	if !node.Together || c == nil || c.failed {
		return
	}
	group := c.over(t)
	failed := w.hooks.AfterStep != nil && group.guard(func() { w.hooks.AfterStep(group, false) })
	w.widen(path, group.window[1], failed)
}

// runStep runs step under c; a failure (Failf, a failed wait, a panic) is recovered here.
func runStep[S any](c *Ctx, step func(*Ctx, S) S, in S) (out S, failed bool) {
	out = in
	failed = c.guard(func() { out = step(c, in) })
	return out, failed || c.failed
}

func verdict(failed bool) string {
	if failed {
		return "FAIL"
	}
	return "PASS"
}

func descendants[S any](children []Node[S], path string) []string {
	var names []string
	for _, child := range children {
		names = append(names, path+"/"+child.Name)
		names = append(names, descendants(child.Children, path+"/"+child.Name)...)
	}
	return names
}

func (w *walker[S]) unique(label string) string {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.uniques[label]++
	if n := w.uniques[label]; n > 1 {
		return fmt.Sprintf("%s-%s-%d", label, w.runID, n)
	}
	return label + "-" + w.runID
}

func (w *walker[S]) record(path, verdict string, from, to time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.records = append(w.records, record{path, verdict, from.Sub(w.started), to.Sub(from).Seconds()})
}

// widen stretches a together node's record over every branch it ran, failing it when its window's
// check failed.
func (w *walker[S]) widen(path string, to time.Time, failed bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for i := range w.records {
		if w.records[i].path == path {
			w.records[i].seconds = to.Sub(w.started.Add(w.records[i].start)).Seconds()
			if failed {
				w.records[i].verdict = "FAIL"
			}
		}
	}
}

// report prints the timeline in start order and the count line, and writes timeline.tsv.
func (w *walker[S]) report(t *testing.T) {
	w.mu.Lock()
	records := append([]record(nil), w.records...)
	w.mu.Unlock()
	sortRecords(records)
	counts := map[string]int{}
	var text, tsv strings.Builder
	tsv.WriteString("path\tverdict\tstart_s\tseconds\n")
	for _, r := range records {
		counts[r.verdict]++
		fmt.Fprintf(&text, "%-7s %7.2fs %7.2fs  %s\n", r.verdict, r.start.Seconds(), r.seconds, r.path)
		fmt.Fprintf(&tsv, "%s\t%s\t%.3f\t%.3f\n", r.path, r.verdict, r.start.Seconds(), r.seconds)
	}
	fmt.Fprintf(&text, "PASS %d · FAIL %d · BLOCKED %d\n", counts["PASS"], counts["FAIL"], counts["BLOCKED"])
	fmt.Print("TIMELINE\n" + text.String())
	if w.hooks.Artifacts == "" {
		return
	}
	file := filepath.Join(w.hooks.Artifacts, "timeline.tsv")
	if err := os.MkdirAll(w.hooks.Artifacts, 0o755); err != nil {
		t.Errorf("timeline: %v", err)
	} else if err := os.WriteFile(file, []byte(tsv.String()), 0o644); err != nil {
		t.Errorf("timeline: write %s: %v", file, err)
	}
}

// sortRecords orders by start offset, then path, so the timeline reads top to bottom as it ran.
func sortRecords(records []record) {
	for i := 1; i < len(records); i++ {
		for j := i; j > 0 && less(records[j], records[j-1]); j-- {
			records[j], records[j-1] = records[j-1], records[j]
		}
	}
}

func less(a, b record) bool {
	if a.start != b.start {
		return a.start < b.start
	}
	return a.path < b.path
}
