package deps

import (
	"context"
	"fmt"
)

// fakeScript is one registered Run answer: a fixed result and error
// (Script), or a handler computing them at call time (Handle).
type fakeScript struct {
	prefix []string
	result RunResult
	err    error
	handle func(context.Context, RunCall) (RunResult, error)
}

// Script registers result/err for every Run call whose argv starts with
// prefix.
func (f *FakeRunner) Script(prefix []string, result RunResult, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts = append(f.scripts, fakeScript{
		prefix: append([]string(nil), prefix...),
		result: result,
		err:    err,
	})
}

// Handle registers handler for every Run call whose argv starts with
// prefix: the answer is computed at call time from the call and its context,
// so a fake can hang until the caller's deadline or keep state between calls.
// It competes with Script by the same longest-prefix rule; the handler runs
// outside the fake's lock, so it may block or call the fake again.
func (f *FakeRunner) Handle(prefix []string, handler func(ctx context.Context, call RunCall) (RunResult, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts = append(f.scripts, fakeScript{prefix: append([]string(nil), prefix...), handle: handler})
}

// Run records the call on the ledger, then answers with the longest
// registered prefix match — its handler's answer for a Handle — or
// UnscriptedError when nothing matches.
func (f *FakeRunner) Run(ctx context.Context, argv []string, opts RunOptions) (RunResult, error) {
	call := RunCall{Argv: append([]string(nil), argv...), Opts: opts}
	f.mu.Lock()
	f.calls = append(f.calls, call)
	var best *fakeScript
	for index := range f.scripts {
		script := &f.scripts[index]
		if !argvHasPrefix(argv, script.prefix) {
			continue
		}
		if best == nil || len(script.prefix) > len(best.prefix) {
			best = script
		}
	}
	if best == nil {
		f.mu.Unlock()
		return RunResult{ExitCode: -1}, UnscriptedError{Argv: append([]string(nil), argv...)}
	}
	answer := *best
	f.mu.Unlock()
	if answer.handle != nil {
		return answer.handle(ctx, RunCall{Argv: append([]string(nil), argv...), Opts: opts})
	}
	return answer.result, answer.err
}

// Calls returns every Run call FakeRunner has recorded, in call order. Each
// entry's Argv is its own copy — mutating a returned call never reaches the
// ledger, the same isolation Run already gives the recorded argv against
// the caller's own slice.
func (f *FakeRunner) Calls() []RunCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := make([]RunCall, len(f.calls))
	for index, call := range f.calls {
		calls[index] = RunCall{Argv: append([]string(nil), call.Argv...), Opts: call.Opts}
	}
	return calls
}

func argvHasPrefix(argv, prefix []string) bool {
	if len(prefix) > len(argv) {
		return false
	}
	for index, want := range prefix {
		if argv[index] != want {
			return false
		}
	}
	return true
}

// UnscriptedError names the argv a FakeRunner received with nothing
// registered for it.
type UnscriptedError struct {
	Argv []string
}

func (err UnscriptedError) Error() string {
	return fmt.Sprintf("deps: FakeRunner: unscripted call %q", err.Argv)
}
