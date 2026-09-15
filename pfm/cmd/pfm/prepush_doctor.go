package main

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
)

const expectedHooksPath = ".githooks"

type prePushGate struct {
	Repository string
	Actual     string
	State      string
	Error      error
}

// prePushGateProbeOverride keeps command-package tests independent of the
// checkout that runs them. Production leaves it nil; the dedicated pre-push
// tests clear the test default and exercise inspectPrePushGate end to end.
var prePushGateProbeOverride func(context.Context) prePushGate

func printPrePushDoctor(ctx context.Context, stdout io.Writer) int {
	var gate prePushGate
	if prePushGateProbeOverride != nil {
		gate = prePushGateProbeOverride(ctx)
	} else {
		gate = inspectPrePushGate(ctx)
	}
	switch gate.State {
	case "outside-repository":
		fmt.Fprintln(stdout, "doctor: pre-push gate=not-applicable outside-repository")
		return 0
	case "not-configured":
		fmt.Fprintln(stdout, "doctor: pre-push gate=not-configured hook=.githooks/pre-push ABSENT")
		return 0
	case "unavailable":
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
	case "broken":
		fmt.Fprintf(stdout, "doctor: pre-push gate=BROKEN core.hooksPath=%s error=%v\n", gate.Actual, gate.Error)
		return 1
	default:
		fmt.Fprintf(stdout, "doctor: pre-push gate=UNREADABLE error=%v\n", gate.Error)
		return 1
	}
}

func inspectPrePushGate(ctx context.Context) prePushGate {
	cwd, err := os.Getwd()
	if err != nil {
		return prePushGate{State: "unreadable", Error: fmt.Errorf("resolve working directory: %w", err)}
	}
	git := deps.Executable("git")
	repositoryBytes, err := exec.CommandContext(ctx, git, "-C", cwd, "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return prePushGate{State: "unavailable", Error: err}
		}
		message := strings.TrimSpace(string(repositoryBytes))
		if strings.Contains(strings.ToLower(message), "not a git repository") {
			return prePushGate{State: "outside-repository"}
		}
		return prePushGate{State: "unreadable", Error: fmt.Errorf("resolve repository: %w: %s", err, message)}
	}
	repository := filepath.Clean(strings.TrimSpace(string(repositoryBytes)))
	hook := filepath.Join(repository, expectedHooksPath, "pre-push")
	hookInfo, hookErr := os.Stat(hook)

	actualBytes, configErr := exec.CommandContext(ctx, git, "-C", repository, "config", "--get", "core.hooksPath").
		CombinedOutput()
	actual := strings.TrimSpace(string(actualBytes))
	if configErr != nil {
		var exitErr *exec.ExitError
		if !(errors.As(configErr, &exitErr) && exitErr.ExitCode() == 1 && actual == "") {
			return prePushGate{
				Repository: repository,
				State:      "unreadable",
				Error:      fmt.Errorf("read core.hooksPath: %w: %s", configErr, actual),
			}
		}
	}

	if errors.Is(hookErr, os.ErrNotExist) && actual == "" {
		return prePushGate{Repository: repository, State: "not-configured"}
	}
	if hookErr != nil {
		return prePushGate{
			Repository: repository,
			Actual:     actual,
			State:      "broken",
			Error:      fmt.Errorf("inspect %s: %w", hook, hookErr),
		}
	}
	if !hookInfo.Mode().IsRegular() || hookInfo.Mode().Perm()&0o111 == 0 {
		return prePushGate{
			Repository: repository,
			Actual:     actual,
			State:      "broken",
			Error:      fmt.Errorf("%s is not an executable regular file", hook),
		}
	}

	if !installer.PrePushGateArmed(repository, actual) {
		return prePushGate{Repository: repository, Actual: actual, State: "unwired"}
	}
	return prePushGate{Repository: repository, Actual: actual, State: "armed"}
}
