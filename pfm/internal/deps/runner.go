package deps

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
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
	// Start launches argv and returns as soon as it is running, never
	// waiting on it here — the shape every streaming or detached spawn
	// (chat_dispatch.go's hook runner, chat_reload_command.go's detached
	// self re-exec, inject.CommandThenSpawner.Spawn) needs instead of Run's
	// wait-to-completion contract.
	Start(ctx context.Context, argv []string, opts StartOptions) (Process, error)
}

// StartOptions configures one Runner.Start call: the environment and
// working directory the child inherits (the same defaults RunOptions
// takes), where its stdout/stderr are wired, and whether it detaches into
// its own session.
type StartOptions struct {
	Env            []string
	Dir            string
	Stdout, Stderr io.Writer
	// Detach starts the child in its own session (SysProcAttr{Setsid: true}
	// on unix) and releases it immediately after Start — the process is
	// never waited on, because a detached child is expected to outlive this
	// one. Process.Wait on a Detach process reports that error rather than
	// blocking forever on a process this Runner no longer holds.
	Detach bool
}

// Process is the running child Runner.Start hands back: its pid, a Wait to
// block for its exit, and a Release to give it up without waiting.
type Process interface {
	Pid() int
	Wait() error
	Release() error
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
	// A nil err IS the zero exit code — ExitCode(nil) answers -1 ("never
	// reached one"), which is right for a process that never ran but wrong
	// here: this command ran and exited 0.
	exitCode := 0
	if err != nil {
		exitCode = ExitCode(err)
	}
	result := RunResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: exitCode}
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

// Start launches argv and returns immediately. A non-Detach start wraps
// ctx's cancellation (exec.CommandContext) exactly as Run does; a Detach
// start runs in its own session and is released right after Start, so
// nothing here holds its process table entry, and Wait on the returned
// Process reports that rather than blocking on a process this Runner no
// longer owns.
func (RealRunner) Start(ctx context.Context, argv []string, opts StartOptions) (Process, error) {
	if len(argv) == 0 {
		return nil, errors.New("deps: RealRunner.Start: empty argv")
	}
	var command *exec.Cmd
	if opts.Detach {
		// A detached child must outlive this call, so it is never wired to
		// ctx's cancellation the way a streamed, waited-on child is.
		command = exec.Command(argv[0], argv[1:]...)
		command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	} else {
		command = exec.CommandContext(ctx, argv[0], argv[1:]...)
	}
	command.Env = opts.Env
	command.Dir = opts.Dir
	command.Stdout = opts.Stdout
	command.Stderr = opts.Stderr
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("start %q: %w", argv[0], err)
	}
	if opts.Detach {
		pid := command.Process.Pid
		if err := command.Process.Release(); err != nil {
			return nil, fmt.Errorf("release detached %q: %w", argv[0], err)
		}
		return &releasedProcess{pid: pid, argv0: argv[0]}, nil
	}
	return &realProcess{command: command}, nil
}

// realProcess wraps a started, non-detached *exec.Cmd this Runner still
// owns: Wait and Release forward to it directly.
type realProcess struct {
	command *exec.Cmd
}

func (p *realProcess) Pid() int { return p.command.Process.Pid }

func (p *realProcess) Wait() error { return p.command.Wait() }

func (p *realProcess) Release() error { return p.command.Process.Release() }

// releasedProcess is what Start(Detach: true) hands back: the pid it saw
// before releasing the process handle. Nothing here can wait on the real
// child any more — Wait says so by name instead of blocking forever on a
// process table entry this Runner gave up.
type releasedProcess struct {
	pid   int
	argv0 string
}

func (p *releasedProcess) Pid() int { return p.pid }

func (p *releasedProcess) Wait() error {
	return fmt.Errorf(
		"deps: Process.Wait: %q (pid %d) was started detached and released; it cannot be waited on",
		p.argv0,
		p.pid,
	)
}

func (p *releasedProcess) Release() error { return nil }

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
	mu           sync.Mutex
	scripts      []fakeScript
	lookups      map[string]fakeLookup
	calls        []RunCall
	startScripts []fakeStartScript
	starts       []StartCall
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

// StartCall is one Start invocation FakeRunner recorded on its ledger —
// Calls' Start counterpart.
type StartCall struct {
	Argv []string
	Opts StartOptions
}

type fakeStartScript struct {
	prefix  []string
	pid     int
	waitErr error
	err     error
}

// fakeProcess is the scripted Process FakeRunner.Start hands back: a fixed
// pid and a fixed Wait error, never a real process.
type fakeProcess struct {
	pid     int
	waitErr error
}

func (p *fakeProcess) Pid() int       { return p.pid }
func (p *fakeProcess) Wait() error    { return p.waitErr }
func (p *fakeProcess) Release() error { return nil }

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

// ScriptStart registers the Process a Start call whose argv starts with
// prefix gets back: pid, the error Process.Wait answers, and the error
// Start itself returns (non-nil err short-circuits before a Process is
// built, the same shape Script's err does for Run).
func (f *FakeRunner) ScriptStart(prefix []string, pid int, waitErr, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.startScripts = append(f.startScripts, fakeStartScript{
		prefix:  append([]string(nil), prefix...),
		pid:     pid,
		waitErr: waitErr,
		err:     err,
	})
}

// Start records the call on the Start ledger, then answers with the
// longest registered ScriptStart match, or a default Process (pid 4242,
// Wait nil) when nothing was scripted — a caller that never cares about the
// spawned process's identity does not have to script one.
func (f *FakeRunner) Start(_ context.Context, argv []string, opts StartOptions) (Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts = append(f.starts, StartCall{Argv: append([]string(nil), argv...), Opts: opts})
	var best *fakeStartScript
	for index := range f.startScripts {
		script := &f.startScripts[index]
		if !argvHasPrefix(argv, script.prefix) {
			continue
		}
		if best == nil || len(script.prefix) > len(best.prefix) {
			best = script
		}
	}
	if best == nil {
		return &fakeProcess{pid: 4242}, nil
	}
	if best.err != nil {
		return nil, best.err
	}
	return &fakeProcess{pid: best.pid, waitErr: best.waitErr}, nil
}

// Starts returns every Start call FakeRunner has recorded, in call order —
// Calls' Start counterpart, with the same copy-on-read isolation.
func (f *FakeRunner) Starts() []StartCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	starts := make([]StartCall, len(f.starts))
	for index, call := range f.starts {
		starts[index] = StartCall{Argv: append([]string(nil), call.Argv...), Opts: call.Opts}
	}
	return starts
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
