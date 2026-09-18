package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/installer"
	"hostops/pfm/internal/obs"
)

const (
	expectedHooksPath = ".githooks"
	brokenState       = "broken"
	unreadableState   = "unreadable"
)

type PrePushGate struct {
	Repository string
	Actual     string
	State      string
	Error      error
}

// prePushGateProbeOverride keeps command-package tests independent of the
// checkout that runs them. Production leaves it nil; the dedicated pre-push
// tests clear the test default and exercise inspectPrePushGate end to end.
var PrePushGateProbeOverride func(context.Context) PrePushGate

func printPrePushDoctor(ctx context.Context, stdout io.Writer) int {
	return printPrePushDoctorWithRunner(ctx, stdout, obs.Runner(deps.RealRunner{}))
}

func printPrePushDoctorWithRunner(ctx context.Context, stdout io.Writer, runner deps.Runner) int {
	var gate PrePushGate
	if PrePushGateProbeOverride != nil {
		gate = PrePushGateProbeOverride(ctx)
	} else {
		gate = inspectPrePushGateWithRunner(ctx, runner)
	}
	switch gate.State {
	case "outside-repository":
		fmt.Fprintln(stdout, "doctor: pre-push gate=not-applicable outside-repository")
		return 0
	case "not-configured":
		fmt.Fprintln(stdout, "doctor: pre-push gate=not-configured hook=.githooks/pre-push ABSENT")
		return 0
	case StateUnavailable:
		fmt.Fprintf(stdout, "doctor: pre-push gate=unavailable dependency=git error=%v\n", gate.Error)
		return 0
	case "armed":
		fmt.Fprintf(stdout, "doctor: pre-push gate=armed core.hooksPath=%s\n", gate.Actual)
		return 0
	case "unwired":
		actual := gate.Actual
		if actual == "" {
			actual = "(unset)"
		}
		fmt.Fprintf(
			stdout,
			"doctor: pre-push gate=UNWIRED expected=%s actual=%s — run git config core.hooksPath %s\n",
			expectedHooksPath,
			actual,
			expectedHooksPath,
		)
		return 1
	case brokenState:
		fmt.Fprintf(stdout, "doctor: pre-push gate=BROKEN core.hooksPath=%s error=%v\n", gate.Actual, gate.Error)
		return 1
	default:
		fmt.Fprintf(stdout, "doctor: pre-push gate=UNREADABLE error=%v\n", gate.Error)
		return 1
	}
}

func inspectPrePushGate(ctx context.Context) PrePushGate {
	return inspectPrePushGateWithRunner(ctx, obs.Runner(deps.RealRunner{}))
}

func inspectPrePushGateWithRunner(ctx context.Context, runner deps.Runner) PrePushGate {
	cwd, err := os.Getwd()
	if err != nil {
		return PrePushGate{State: unreadableState, Error: fmt.Errorf("resolve working directory: %w", err)}
	}
	repositoryResult, err := runner.Run(
		ctx,
		[]string{"git", "-C", cwd, "rev-parse", "--show-toplevel"},
		deps.RunOptions{},
	)
	if err != nil || repositoryResult.ExitCode != 0 {
		if errors.Is(err, exec.ErrNotFound) {
			return PrePushGate{State: StateUnavailable, Error: err}
		}
		message := strings.TrimSpace(string(repositoryResult.Stdout) + string(repositoryResult.Stderr))
		if strings.Contains(strings.ToLower(message), "not a git repository") {
			return PrePushGate{State: "outside-repository"}
		}
		if err == nil {
			err = fmt.Errorf("git rev-parse exited %d", repositoryResult.ExitCode)
		}
		return PrePushGate{State: unreadableState, Error: fmt.Errorf("resolve repository: %w: %s", err, message)}
	}
	repository := filepath.Clean(strings.TrimSpace(string(repositoryResult.Stdout)))
	hook := filepath.Join(repository, expectedHooksPath, "pre-push")
	hookInfo, hookErr := os.Stat(hook)

	configResult, configErr := runner.Run(
		ctx,
		[]string{"git", "-C", repository, "config", "--get", "core.hooksPath"},
		deps.RunOptions{},
	)
	actual := strings.TrimSpace(string(configResult.Stdout))
	if configErr != nil || configResult.ExitCode != 0 {
		if configErr != nil || configResult.ExitCode != 1 || actual != "" {
			return PrePushGate{
				Repository: repository,
				State:      unreadableState,
				Error:      fmt.Errorf("read core.hooksPath: %v: %s", configErr, actual),
			}
		}
	}

	if errors.Is(hookErr, os.ErrNotExist) && actual == "" {
		return PrePushGate{Repository: repository, State: "not-configured"}
	}
	if hookErr != nil {
		return PrePushGate{
			Repository: repository,
			Actual:     actual,
			State:      brokenState,
			Error:      fmt.Errorf("inspect %s: %w", hook, hookErr),
		}
	}
	if !hookInfo.Mode().IsRegular() || hookInfo.Mode().Perm()&0o111 == 0 {
		return PrePushGate{
			Repository: repository,
			Actual:     actual,
			State:      brokenState,
			Error:      fmt.Errorf("%s is not an executable regular file", hook),
		}
	}

	if !installer.PrePushGateArmed(repository, actual) {
		return PrePushGate{Repository: repository, Actual: actual, State: "unwired"}
	}
	return PrePushGate{Repository: repository, Actual: actual, State: "armed"}
}
