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
			name: "reload-intercept blocks the prompt while the reload is scheduled",
			wantStderr: "pfm internal reload-intercept: schedule reload (it may still be scheduled: " +
				"its worker logs to reload-*.log in /jail/sid) timed out after 100ms\n",
			wantCode: 2,
			call: func(block func(), stdout, stderr io.Writer) int {
				front := func(context.Context, []string, io.Writer, io.Writer, config.Runtime) int { block(); return 0 }
				return ReloadIntercept(
					strings.NewReader(`{"prompt":"/reload"}`),
					stdout,
					stderr,
					config.Runtime{Paths: paths.Values{SIDDir: "/jail/sid"}},
					front,
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
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			oldIdentify := epicInjectIdentify
			release, done := make(chan struct{}), make(chan struct{})
			block := func() {
				defer close(done)
				select {
				case <-release:
				case <-time.After(2 * time.Second):
				}
			}
			t.Cleanup(func() {
				close(release)
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("blocked hook body never returned")
				}
				epicInjectIdentify = oldIdentify
			})
			var stdout, stderr bytes.Buffer
			started := time.Now()
			code := test.call(block, &stdout, &stderr)
			if elapsed := time.Since(started); elapsed >= time.Second {
				t.Fatalf("hook answered after %s, want under 1s (deadline 100ms)", elapsed)
			}
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

// TestPromptHookPassesTheBodyAnswerThroughWithinTheDeadline: a body that
// finishes in time keeps its own output and exit code.
func TestPromptHookPassesTheBodyAnswerThroughWithinTheDeadline(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := RunPromptHook("pfm internal reload-intercept", &stdout, &stderr, 2,
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
	cases := []struct {
		name       string
		pause      time.Duration // before BeginCommit
		span       time.Duration // between start and record
		wantOpened bool
		wantRecord bool
		wantStderr string
	}{
		{
			name: "a deadline between start and record waits for the record",
			span: 250 * time.Millisecond, wantOpened: true, wantRecord: true,
			wantStderr: "pfm test-hook: record worker timed out after 100ms, " +
				"then waited up to 500ms for the step it had started\n",
		},
		{
			name:  "a commit asked for past the deadline never starts",
			pause: 200 * time.Millisecond, wantOpened: false, wantRecord: false,
			wantStderr: "pfm test-hook: start worker timed out after 100ms\n",
		},
		{
			name: "a span that outlasts the grace is answered over",
			span: 1000 * time.Millisecond, wantOpened: true, wantRecord: false,
			wantStderr: "pfm test-hook: record worker timed out after 100ms, " +
				"and the step it had started was still running 500ms later; it may be cut short\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			opened, recorded := false, false
			finished := make(chan struct{})
			var stdout, stderr bytes.Buffer
			started := time.Now()
			code := RunPromptHook("pfm test-hook", &stdout, &stderr, 2,
				func(ctx context.Context, step func(string), _, _ io.Writer) int {
					defer close(finished)
					step("start worker")
					time.Sleep(tc.pause)
					end, ok := deps.BeginCommit(ctx)
					if !ok {
						return 1
					}
					mu.Lock()
					opened = true
					mu.Unlock()
					step("record worker")
					time.Sleep(tc.span)
					mu.Lock()
					recorded = true
					mu.Unlock()
					end()
					time.Sleep(400 * time.Millisecond) // the body outlives its commit
					return 0
				})
			if elapsed := time.Since(started); elapsed >= 900*time.Millisecond {
				t.Fatalf("hook answered after %s, want within the deadline plus the grace (600ms)", elapsed)
			}
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
			<-finished
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
	for _, tc := range []struct {
		name string
		call func(front func(context.Context)) int
	}{
		{name: "reload", call: func(front func(context.Context)) int {
			return ReloadIntercept(strings.NewReader(`{"prompt":"/reload"}`), io.Discard, io.Discard, config.Runtime{},
				func(ctx context.Context, _ []string, _, _ io.Writer, _ config.Runtime) int { front(ctx); return 0 })
		}},
		{name: "exit", call: func(front func(context.Context)) int {
			return ExitIntercept(strings.NewReader(`{"prompt":"e"}`), io.Discard, io.Discard, config.Runtime{},
				func(ctx context.Context, _ []string, _, _ io.Writer, _ ...config.Runtime) int { front(ctx); return 0 })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opened := make(chan bool, 1)
			tc.call(func(ctx context.Context) {
				time.Sleep(150 * time.Millisecond)
				end, ok := deps.BeginCommit(ctx)
				if ok {
					end()
				}
				opened <- ok
			})
			if <-opened {
				t.Fatal("the front opened a commit past the hook deadline: its ctx does not carry the hook's gate")
			}
		})
	}
}
