package hookentry

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/testwait"
	"github.com/rezzminator/professor/pfm/internal/usagehook"
)

// TestPromptHooksAnswerWithinTheirDeadline holds I2: a hook whose work blocks,
// ignoring its context, still answers at the deadline — the timeout line on
// stderr naming the step, nothing on stdout, and the hook's fail code.
func TestPromptHooksAnswerWithinTheirDeadline(t *testing.T) {
	oldDeadline := promptHookDeadline
	promptHookDeadline = 100 * time.Millisecond
	t.Cleanup(func() { promptHookDeadline = oldDeadline })
	cases := []struct {
		name, wantStderr string
		wantCode         int
		call             func(block func(), stdout, stderr io.Writer) int
	}{
		{
			name:       "epic-inject fails open while the chat is identified",
			wantStderr: "pfm internal epic-inject: identify chat timed out after 100ms\n",
			wantCode:   0,
			call: func(block func(), stdout, stderr io.Writer) int {
				epicInjectIdentify = func(context.Context) (resolve.Identity, error) {
					block()
					return resolve.Identity{}, nil
				}
				return EpicInject(strings.NewReader(`{"cwd":"/jail/project"}`), stdout, stderr)
			},
		},
		{
			name: "command-intercept blocks the prompt while the reload is scheduled",
			wantStderr: "pfm internal command-intercept: schedule reload (it may still be scheduled: " +
				"its worker logs to reload-*.log in /jail/sid) timed out after 100ms\n",
			wantCode: 2,
			call: func(block func(), stdout, stderr io.Writer) int {
				front := func(context.Context, []string, io.Writer, io.Writer, config.Runtime) int { block(); return 0 }
				return CommandIntercept(
					strings.NewReader(`{"prompt":"/pfm:reload"}`),
					stdout,
					stderr,
					config.Runtime{Paths: paths.Values{SIDDir: "/jail/sid"}},
					front,
					nil,
				)
			},
		},
		{
			name: "command-intercept blocks the prompt while the space is set",
			wantStderr: "pfm internal command-intercept: set the chat's space (it may still be set: " +
				"check `pfm chat ls`) timed out after 100ms\n",
			wantCode: 2,
			call: func(block func(), stdout, stderr io.Writer) int {
				space := func(context.Context, *config.Runtime, string) (string, error) { block(); return "", nil }
				return CommandIntercept(
					strings.NewReader(`{"prompt":"/pfm:space work"}`), stdout, stderr, config.Runtime{}, nil, space,
				)
			},
		},
		{
			name: "exit-intercept blocks the prompt while the chat is ended",
			wantStderr: "pfm internal exit-intercept: end chat (it may still be ending: " +
				"check `pfm chat ls` before typing it again) timed out after 100ms\n",
			wantCode: 2,
			call: func(block func(), stdout, stderr io.Writer) int {
				front := func(context.Context, []string, io.Writer, io.Writer, ...config.Runtime) int { block(); return 0 }
				return ExitIntercept(strings.NewReader(`{"prompt":"e"}`), stdout, stderr, config.Runtime{}, front)
			},
		},
		{
			name:       "usage-hook fails open while usage is evaluated",
			wantStderr: "pfm usage-hook: evaluate usage timed out after 100ms\n",
			wantCode:   0,
			call: func(block func(), stdout, stderr io.Writer) int {
				evaluate := func(context.Context, usagehook.Options) (string, error) {
					block()
					return "late output\n", nil
				}
				return usageHookWith(stdout, stderr, usagehook.Options{}, evaluate)
			},
		},
	}
	timeouts := swapPromptTimeouts(t)
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			oldIdentify := epicInjectIdentify
			entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			hung := testwait.Context(t).Done()
			block := func() {
				defer close(done)
				close(entered)
				select {
				case <-release:
				case <-hung:
				}
			}
			t.Cleanup(func() {
				close(release)
				testwait.Receive(t, "the blocked hook body to return", done)
				epicInjectIdentify = oldIdentify
			})
			// The deadline passes once the body is blocked in its step.
			timeouts.fireAfter(t, entered, promptHookDeadline)
			var stdout, stderr bytes.Buffer
			code := test.call(block, &stdout, &stderr)
			if code != test.wantCode {
				t.Fatalf("exit code = %d, want %d; stderr=%q", code, test.wantCode, stderr.String())
			}
			if stderr.String() != test.wantStderr {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.wantStderr)
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want nothing past the deadline", stdout.String())
			}
		})
	}
}

// promptTimeouts arms the prompt hooks' deadline and commit grace on contexts
// the test ends by hand, so the order of the body, the deadline and the grace
// is the test's and no host time decides it.
type promptTimeouts struct {
	mu      sync.Mutex
	pending map[time.Duration]context.CancelFunc
	armed   chan time.Duration
}

func swapPromptTimeouts(t *testing.T) *promptTimeouts {
	t.Helper()
	timeouts := &promptTimeouts{pending: map[time.Duration]context.CancelFunc{}, armed: make(chan time.Duration, 16)}
	previous := promptHookTimeout
	promptHookTimeout = func(d time.Duration) (context.Context, context.CancelFunc) {
		ctx, cancel := context.WithCancel(context.Background())
		timeouts.mu.Lock()
		timeouts.pending[d] = cancel
		timeouts.mu.Unlock()
		timeouts.armed <- d
		return ctx, cancel
	}
	t.Cleanup(func() { promptHookTimeout = previous })
	return timeouts
}

// fireAfter ends the contexts armed for each of waits, in order, once ready is
// closed: the first the deadline, the next the grace the hook arms when it
// waits on a commit span.
func (timeouts *promptTimeouts) fireAfter(t *testing.T, ready <-chan struct{}, waits ...time.Duration) {
	t.Helper()
	hung := testwait.Context(t).Done()
	go func() {
		select {
		case <-ready:
		case <-hung:
			return
		}
		for _, d := range waits {
			for armed := d - 1; armed != d; {
				select {
				case armed = <-timeouts.armed:
				case <-hung:
					return
				}
			}
			timeouts.mu.Lock()
			cancel := timeouts.pending[d]
			timeouts.mu.Unlock()
			cancel()
		}
	}()
}

// TestPromptHookPassesTheBodyAnswerThroughWithinTheDeadline: a body that
// finishes in time keeps its own output and exit code.
func TestPromptHookPassesTheBodyAnswerThroughWithinTheDeadline(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := RunPromptHook("pfm internal command-intercept", &stdout, &stderr, 2,
		func(_ context.Context, step func(string), stdout, stderr io.Writer) int {
			step("schedule reload")
			_ = json.NewEncoder(stdout).Encode(map[string]string{"decision": "block"})
			_, _ = io.WriteString(stderr, "note\n")
			return 0
		})
	if code != 0 || stdout.String() != "{\"decision\":\"block\"}\n" || stderr.String() != "note\n" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

// TestPromptHookDeadlineNeverSplitsACommit holds F2c: the deadline applies
// before a commit span starts (start the reload worker, record it); a span
// that started before the deadline completes before the hook answers, so the
// process never exits between the start and its record. The wait is bounded:
// a span still open promptHookCommitGrace past the deadline is answered over,
// and the timeout line says it may be cut short.
func TestPromptHookDeadlineNeverSplitsACommit(t *testing.T) {
	oldDeadline, oldGrace := promptHookDeadline, promptHookCommitGrace
	promptHookDeadline, promptHookCommitGrace = 100*time.Millisecond, 500*time.Millisecond
	t.Cleanup(func() { promptHookDeadline, promptHookCommitGrace = oldDeadline, oldGrace })
	timeouts := swapPromptTimeouts(t)
	cases := []struct {
		name       string
		askLate    bool // the body asks for its commit only once the hook has answered
		spanEnds   string
		fireGrace  bool
		wantOpened bool
		wantRecord bool
		wantStderr string
	}{
		{
			name: "a deadline between start and record waits for the record", spanEnds: "deadline",
			wantOpened: true, wantRecord: true,
			wantStderr: "pfm test-hook: record worker timed out after 100ms, " +
				"then waited up to 500ms for the step it had started\n",
		},
		{
			name: "a commit asked for past the deadline never starts", askLate: true,
			wantOpened: false, wantRecord: false,
			wantStderr: "pfm test-hook: start worker timed out after 100ms\n",
		},
		{
			name: "a span that outlasts the grace is answered over", spanEnds: "answer", fireGrace: true,
			wantOpened: true, wantRecord: false,
			wantStderr: "pfm test-hook: record worker timed out after 100ms, " +
				"and the step it had started was still running 500ms later; it may be cut short\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			opened, recorded := false, false
			ready, answered, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			waits := []time.Duration{promptHookDeadline}
			if tc.fireGrace {
				waits = append(waits, promptHookCommitGrace)
			}
			timeouts.fireAfter(t, ready, waits...)
			var stdout, stderr bytes.Buffer
			code := RunPromptHook("pfm test-hook", &stdout, &stderr, 2,
				func(ctx context.Context, step func(string), _, _ io.Writer) int {
					defer close(finished)
					step("start worker")
					if tc.askLate {
						close(ready)
						<-answered
					}
					end, ok := deps.BeginCommit(ctx)
					if !ok {
						return 1
					}
					mu.Lock()
					opened = true
					mu.Unlock()
					step("record worker")
					if !tc.askLate {
						close(ready)
					}
					if tc.spanEnds == "deadline" {
						<-ctx.Done()
					} else {
						<-answered
					}
					mu.Lock()
					recorded = true
					mu.Unlock()
					end()
					<-answered // the body outlives its commit
					return 0
				})
			mu.Lock()
			gotRecord := recorded
			mu.Unlock()
			if code != 2 || gotRecord != tc.wantRecord {
				t.Fatalf("hook answered code=%d recorded=%v, want code=2 recorded=%v", code, gotRecord, tc.wantRecord)
			}
			if stderr.String() != tc.wantStderr || stdout.Len() != 0 {
				t.Fatalf(
					"hook stderr=%q stdout=%q, want stderr=%q and no stdout",
					stderr.String(),
					stdout.String(),
					tc.wantStderr,
				)
			}
			close(answered)
			testwait.Receive(t, "the hook body to finish after the hook answered", finished)
			mu.Lock()
			gotOpened := opened
			mu.Unlock()
			if gotOpened != tc.wantOpened {
				t.Fatalf("commit opened=%v once the body finished, want %v", gotOpened, tc.wantOpened)
			}
		})
	}
}

// TestPromptInterceptsHandTheirFrontTheCommitGate holds F2c's plumbing: the
// reload and exit fronts receive the hook's ctx, so a commit asked for past
// the deadline is refused there too.
func TestPromptInterceptsHandTheirFrontTheCommitGate(t *testing.T) {
	oldDeadline := promptHookDeadline
	promptHookDeadline = 50 * time.Millisecond
	t.Cleanup(func() { promptHookDeadline = oldDeadline })
	timeouts := swapPromptTimeouts(t)
	for _, tc := range []struct {
		name string
		call func(front func(context.Context)) int
	}{
		{name: "reload", call: func(front func(context.Context)) int {
			return CommandIntercept(strings.NewReader(`{"prompt":"/pfm:reload"}`), io.Discard, io.Discard, config.Runtime{},
				func(ctx context.Context, _ []string, _, _ io.Writer, _ config.Runtime) int { front(ctx); return 0 }, nil)
		}},
		{name: "exit", call: func(front func(context.Context)) int {
			return ExitIntercept(strings.NewReader(`{"prompt":"e"}`), io.Discard, io.Discard, config.Runtime{},
				func(ctx context.Context, _ []string, _, _ io.Writer, _ ...config.Runtime) int { front(ctx); return 0 })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opened := make(chan bool, 1)
			ready, answered := make(chan struct{}), make(chan struct{})
			timeouts.fireAfter(t, ready, promptHookDeadline)
			tc.call(func(ctx context.Context) {
				close(ready)
				<-answered // the hook has answered, its gate closed
				end, ok := deps.BeginCommit(ctx)
				if ok {
					end()
				}
				opened <- ok
			})
			close(answered)
			if ok, _ := testwait.Receive(t, "the front to ask for its commit", opened); ok {
				t.Fatal("the front opened a commit past the hook deadline: its ctx does not carry the hook's gate")
			}
		})
	}
}
