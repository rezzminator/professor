package update

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

type scriptedUpdateProcess struct{ waitErr error }

func (process scriptedUpdateProcess) Pid() int       { return 1001 }
func (process scriptedUpdateProcess) Wait() error    { return process.waitErr }
func (process scriptedUpdateProcess) Release() error { return nil }
func (process scriptedUpdateProcess) StdinPipe() (io.WriteCloser, error) {
	return nil, errors.New("stdin pipe not scripted")
}

func (process scriptedUpdateProcess) StdoutPipe() (io.ReadCloser, error) {
	return nil, errors.New("stdout pipe not scripted")
}
func (process scriptedUpdateProcess) Kill() error      { return nil }
func (process scriptedUpdateProcess) KillGroup() error { return nil }

type scriptedUpdateRunner struct {
	stdout    string
	stderr    string
	waitErr   error
	runResult deps.RunResult
	runErr    error
	startEnv  *[]string
}

func (runner scriptedUpdateRunner) Run(context.Context, []string, deps.RunOptions) (deps.RunResult, error) {
	if runner.runResult.Stdout != nil || runner.runResult.Stderr != nil || runner.runResult.ExitCode != 0 ||
		runner.runErr != nil {
		return runner.runResult, runner.runErr
	}
	return deps.RunResult{ExitCode: -1}, errors.New("Run was used; update candidate must stream through Start")
}

func (runner scriptedUpdateRunner) LookPath(string) (string, error) {
	return "", errors.New("LookPath was not scripted")
}

func (runner scriptedUpdateRunner) Start(
	_ context.Context,
	_ []string,
	options deps.StartOptions,
) (deps.Process, error) {
	if runner.startEnv != nil {
		*runner.startEnv = append([]string(nil), options.Env...)
	}
	if options.Stdout != nil {
		if _, err := io.WriteString(options.Stdout, runner.stdout); err != nil {
			return nil, err
		}
	}
	if options.Stderr != nil {
		if _, err := io.WriteString(options.Stderr, runner.stderr); err != nil {
			return nil, err
		}
	}
	return scriptedUpdateProcess{waitErr: runner.waitErr}, nil
}

type scriptedUpdateExitError struct{ code int }

func (err scriptedUpdateExitError) Error() string { return "scripted candidate exit" }
func (err scriptedUpdateExitError) ExitCode() int { return err.code }

// TestRunUpdateCandidateCommandStreamsAndCapturesDoctor is a REGRESSION test
// for the candidate doctor capture boundary: the old update gate diffed the
// doctor's stdout, while stderr remained live operator output. Keeping those
// streams separate also avoids two os/exec copy goroutines writing the same
// bytes.Buffer concurrently.
func TestRunUpdateCandidateCommandStreamsAndCapturesDoctor(t *testing.T) {
	restore := StubRunnerForTest(scriptedUpdateRunner{
		stdout:  "doctor stdout\n",
		stderr:  "doctor stderr\n",
		waitErr: scriptedUpdateExitError{code: 3},
	})
	t.Cleanup(restore)

	var stdout, stderr bytes.Buffer
	err := runUpdateCandidateCommand(
		context.Background(), "/tmp/pfm-candidate", "", t.TempDir(), "", &stdout, &stderr, doctorCommand,
	)
	var doctorErr *doctorExitError
	if !errors.As(err, &doctorErr) {
		t.Fatalf("runUpdateCandidateCommand() error = %v, want doctor exit error", err)
	}
	if doctorErr.code != 3 || doctorErr.output != "doctor stdout\n" {
		t.Fatalf("doctor capture = %#v, want stdout only and exit 3", doctorErr)
	}
	if stdout.String() != "doctor stdout\n" || stderr.String() != "doctor stderr\n" {
		t.Fatalf("live output stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestUpdateGitOutputUsesInjectedRunner(t *testing.T) {
	restore := StubRunnerForTest(scriptedUpdateRunner{runResult: deps.RunResult{
		Stdout:   []byte("HEAD\n"),
		ExitCode: 0,
	}})
	t.Cleanup(restore)

	got, err := updateGitOutput(context.Background(), t.TempDir(), "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("updateGitOutput() error = %v", err)
	}
	if got != "HEAD\n" {
		t.Fatalf("updateGitOutput() = %q, want injected stdout", got)
	}
}

func TestRunUpdateCandidateCommandClearsInheritedSourceRepo(t *testing.T) {
	var capturedEnv []string
	restore := StubRunnerForTest(scriptedUpdateRunner{startEnv: &capturedEnv})
	t.Cleanup(restore)
	t.Setenv("PFM_SOURCE_REPO", "/host/source-that-must-not-leak")

	if err := runUpdateCandidateCommand(
		context.Background(), "/tmp/pfm-candidate", "", t.TempDir(), "", io.Discard, io.Discard, "doctor",
	); err != nil {
		t.Fatalf("runUpdateCandidateCommand() error = %v", err)
	}

	for _, entry := range capturedEnv {
		if entry == "PFM_SOURCE_REPO=" {
			return
		}
	}
	t.Fatalf("candidate environment omitted an explicit empty PFM_SOURCE_REPO: %v", capturedEnv)
}
