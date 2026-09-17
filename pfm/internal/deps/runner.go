package deps

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
)

// RunOptions configures one Runner.Run call: the environment and working
// directory the child process inherits, and the input fed on its stdin. A
// nil Env inherits the caller's own environment and an empty Dir runs in
// the caller's own working directory — the same defaults a bare exec.Cmd
// takes when those fields are left unset.
type RunOptions struct {
	Env   []string
	Dir   string
	Stdin []byte
}

// RunResult is one command's completed run: stdout and stderr split (never
// merged the way probeOne's CombinedOutput is, because a Runner caller — git,
// systemctl, security — routinely needs to tell them apart) plus the
// process exit code, or -1 when the process never reached one (the binary
// could not start, the context was cancelled first).
type RunResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner is the one seam every exec.Command*/exec.LookPath outside the
// internal/tmux façade crosses (docs/dev/trains/testing-foundation/waves/
// 3-unit-law/spec.md § Three seams item 2): git, claude, codex, opencode,
// systemctl, launchctl, security, docker, uv, python, node, jq. RealRunner
// drives the real process table exactly as a bare os/exec call always has;
// FakeRunner scripts a call ledger for tests.
type Runner interface {
	Run(ctx context.Context, argv []string, opts RunOptions) (RunResult, error)
	LookPath(name string) (string, error)
}

// RealRunner runs argv through os/exec.
type RealRunner struct{}

// Run starts argv[0] with argv[1:] as its arguments. A nonzero exit is the
// command answering, not the Runner failing to run it — Run returns a nil
// error with ExitCode set, the same contract probeOne already holds for a
// version-probe subprocess; only a failure to start (lookup, permission,
// context already done) returns a non-nil error.
func (RealRunner) Run(ctx context.Context, argv []string, opts RunOptions) (RunResult, error) {
	if len(argv) == 0 {
		return RunResult{ExitCode: -1}, errors.New("deps: RealRunner.Run: empty argv")
	}
	command := exec.CommandContext(ctx, argv[0], argv[1:]...)
	command.Env = opts.Env
	command.Dir = opts.Dir
	if opts.Stdin != nil {
		command.Stdin = bytes.NewReader(opts.Stdin)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := RunResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: ExitCode(err)}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		return result, fmt.Errorf("run %q: %w", argv[0], err)
	}
	return result, nil
}

// LookPath resolves name on $PATH exactly as exec.LookPath does.
func (RealRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

// FakeRunner scripts Runner responses by argv prefix and records every call
// on a ledger. Script order does not matter: the LONGEST matching prefix
// wins, so a caller can script a broad default ("git") alongside a
// narrower override ("git" "push") without the narrower one being shadowed
// by registration order. A call matching no registered prefix returns
// UnscriptedError — never a silent empty RunResult a caller could mistake
// for "ran and produced nothing" — and LookPath for an unscripted name
// answers ENOENT, the same error a bare exec.LookPath gives for a missing
// binary, which is what a fixture like hostfixture.NoTmux wants for free by
// scripting nothing.
type FakeRunner struct {
	mu      sync.Mutex
	scripts []fakeScript
	lookups map[string]fakeLookup
	calls   []RunCall
}

type fakeScript struct {
	prefix []string
	result RunResult
	err    error
}

type fakeLookup struct {
	path string
	err  error
}

// RunCall is one Run invocation FakeRunner recorded on its ledger.
type RunCall struct {
	Argv []string
	Opts RunOptions
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

// ScriptLookPath registers LookPath(name)'s result.
func (f *FakeRunner) ScriptLookPath(name, path string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lookups == nil {
		f.lookups = map[string]fakeLookup{}
	}
	f.lookups[name] = fakeLookup{path: path, err: err}
}

// Run records the call on the ledger, then answers with the longest
// registered prefix match, or UnscriptedError when nothing matches.
func (f *FakeRunner) Run(_ context.Context, argv []string, opts RunOptions) (RunResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, RunCall{Argv: append([]string(nil), argv...), Opts: opts})
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
		return RunResult{ExitCode: -1}, UnscriptedError{Argv: append([]string(nil), argv...)}
	}
	return best.result, best.err
}

// LookPath answers a scripted name, or ENOENT (exec.ErrNotFound) for one
// nothing scripted.
func (f *FakeRunner) LookPath(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lookups != nil {
		if lookup, ok := f.lookups[name]; ok {
			return lookup.path, lookup.err
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
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
