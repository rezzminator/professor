package update

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"hostops/pfm/internal/config"
	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/obs"
)

var updateRunner deps.Runner = obs.Runner(deps.RealRunner{})

func currentUpdateRunner() deps.Runner {
	if updateRunner == nil {
		return obs.Runner(deps.RealRunner{})
	}
	return updateRunner
}

func updateProcessExitCode(err error) int {
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return deps.ExitCode(err)
}

// StubRunnerForTest replaces the update process seam and restores it when the
// caller is done. Git, candidate builds, and candidate commands all cross the
// same runner, so update tests never need a real repository process table.
func StubRunnerForTest(runner deps.Runner) func() {
	previous := updateRunner
	updateRunner = runner
	return func() { updateRunner = previous }
}

// StubBaselineDoctorForTest replaces the pre-update subprocess doctor with a
// clean verdict and returns a restore function. It exists for cmd/pfm's two
// retained binary-contract tests: os.Executable there is the Go test binary,
// not a runnable pfm binary.
func StubBaselineDoctorForTest() func() {
	previous := updateBaselineDoctor
	updateBaselineDoctor = func(
		context.Context,
		config.Runtime,
		bool,
		io.Writer,
		io.Writer,
	) (doctorOutcome, error) {
		return doctorOutcome{}, nil
	}
	return func() {
		updateBaselineDoctor = previous
	}
}

// doctorOutcome carries a doctor's exit/tallies and captured output for diffs.
type doctorOutcome struct {
	Exit     int
	Warnings int
	Failures int
	Output   string
}

// doctorExitError preserves a doctor's nonzero verdict separately from spawn failure.
type doctorExitError struct {
	code   int
	output string
}

func (e *doctorExitError) Error() string {
	if strings.TrimSpace(e.output) != "" {
		return fmt.Sprintf("doctor exited %d: %s", e.code, strings.TrimSpace(lastLine(e.output)))
	}
	return fmt.Sprintf("doctor exited %d", e.code)
}

func lastLine(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	return lines[len(lines)-1]
}

var (
	doctorTallyPattern = regexp.MustCompile(`(?m)^doctor: (warnings|failures)=(\d+)$`)
	doctorHexPattern   = regexp.MustCompile(`(?i)\b[0-9a-f]{8,}\b`)
	doctorDigitPattern = regexp.MustCompile(`\d+`)
)

// parseDoctorTally reads doctor summary rows; an omitted tier is zero.
func parseDoctorTally(output string) (warnings, failures int) {
	for _, match := range doctorTallyPattern.FindAllStringSubmatch(output, -1) {
		count, err := strconv.Atoi(match[2])
		if err != nil {
			continue
		}
		if match[1] == "warnings" {
			warnings = count
		} else {
			failures = count
		}
	}
	return warnings, failures
}

// normalizeDoctorRow masks volatile numbers and long hex values before diffing.
func normalizeDoctorRow(line string) string {
	masked := doctorHexPattern.ReplaceAllString(line, "#")
	return doctorDigitPattern.ReplaceAllString(masked, "#")
}

func isDoctorSummaryLine(line string) bool {
	return strings.HasPrefix(line, "doctor: warnings=") ||
		strings.HasPrefix(line, "doctor: failures=") ||
		line == "doctor: clean"
}

// diffNewDoctorWarningRows returns candidate rows absent from the baseline.
func diffNewDoctorWarningRows(baselineOutput, candidateOutput string) []string {
	baselineRows := make(map[string]bool)
	for _, line := range strings.Split(baselineOutput, "\n") {
		if line == "" {
			continue
		}
		baselineRows[normalizeDoctorRow(line)] = true
	}
	var newRows []string
	for _, line := range strings.Split(candidateOutput, "\n") {
		if line == "" || isDoctorSummaryLine(line) {
			continue
		}
		if baselineRows[normalizeDoctorRow(line)] {
			continue
		}
		newRows = append(newRows, line)
	}
	return newRows
}

// rollbackDoctorPredatesFailureTiers recognizes old doctors that exit 1 on warnings.
func rollbackDoctorPredatesFailureTiers(output string) bool {
	return !strings.Contains(output, "doctor: failures=") &&
		!strings.Contains(output, "doctor: warnings=") &&
		!strings.Contains(output, "doctor: clean")
}

func runUpdateCandidateCommand(
	ctx context.Context,
	candidate string,
	configPath string,
	workingDir string,
	sourceRepo string,
	stdout, stderr io.Writer,
	commandName string,
	commandArgs ...string,
) error {
	args := make([]string, 0, len(commandArgs)+3)
	if configPath != "" {
		args = append(args, "--config", configPath)
	}
	args = append(args, commandName)
	args = append(args, commandArgs...)
	var captured bytes.Buffer
	stdoutWriter := io.MultiWriter(stdout, &captured)
	process, err := currentUpdateRunner().Start(ctx, append([]string{candidate}, args...), deps.StartOptions{
		Dir: workingDir, Env: updateSourceRepoEnv(sourceRepo), Stdout: stdoutWriter, Stderr: stderr,
	})
	if err != nil {
		return fmt.Errorf("target candidate %s: %w", commandName, err)
	}
	waitErr := process.Wait()
	if waitErr == nil {
		return nil
	}
	exitCode := updateProcessExitCode(waitErr)
	if exitCode < 0 {
		return fmt.Errorf("target candidate %s wait: %w", commandName, waitErr)
	}
	if commandName == doctorCommand {
		return &doctorExitError{code: exitCode, output: captured.String()}
	}
	return fmt.Errorf(
		"target candidate %s: exited %d: %w output=%q",
		commandName,
		exitCode,
		waitErr,
		strings.TrimSpace(captured.String()),
	)
}

func updateSourceRepoEnv(sourceRepo string) []string {
	return deps.EnvironmentWith("PFM_SOURCE_REPO", sourceRepo)
}
