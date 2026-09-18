package deps

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	pfmengine "hostops/pfm/internal/engine"
)

const ProbeTimeout = 5 * time.Second

// DefaultSelfDoctorTimeout bounds the self-doctor summary call on its own —
// separately from the ordinary version-probe timeout. A codex/claude
// self-doctor can legitimately run past a few seconds while scanning a large
// transcript corpus, and that must read as a named timeout, never as a
// broken engine that blocks install preflight.
const DefaultSelfDoctorTimeout = 30 * time.Second

type State string

const (
	StateOK      State = "ok"
	StateMissing State = "missing"
	StateBroken  State = "broken"
	StateSkipped State = "skipped"
	// StateCancelled is a probe stopped by its caller's context. It is an
	// unanswered dependency check, distinct from both a broken command and a
	// child timeout that this package imposed itself.
	StateCancelled State = "cancelled"
	// StateTimeout is a version probe that outran its bound: the binary
	// resolved, it was executed, and it answered nothing in time. That is an
	// unanswered question, not a diagnosis — a loaded box, a cold binary, or a
	// network mount produces it from a perfectly healthy install, and calling
	// it "broken" sends the reader after a corrupt install that does not
	// exist. The sibling self-doctor path has drawn this line since it was
	// written; the version path had not.
	StateTimeout State = "timeout"
)

// Result is one dependency's probe result.
type Result struct {
	Entry      Entry
	State      State
	Path       string
	Version    string
	SelfDoctor string
	Error      string
	Raw        string
	VerboseErr string
	// ExitCode is the version probe's process exit code, or -1 when the
	// failure never reached one (lookup, timeout, cancellation).
	ExitCode int
}

// ProbeOptions supplies the only variability required by tests and callers.
type ProbeOptions struct {
	GOOS         string
	SkipHarvest  bool
	SkipEngines  map[pfmengine.ID]bool
	Provisioning bool
	VerboseDir   string
	LookPath     func(string) (string, error)
	Timeout      time.Duration
	// SelfDoctorTimeout bounds only the self-doctor calls. Unset falls back
	// to Timeout (so existing callers that only ever set Timeout keep
	// driving the self-doctor bound), and Timeout unset too falls back to
	// DefaultSelfDoctorTimeout.
	SelfDoctorTimeout time.Duration
	// Runner is the deps.Runner seam every version and self-doctor probe
	// child process launches through. obs cannot import deps (obs.Runner's
	// own signature names deps.Runner), so this package cannot wrap its own
	// default — a nil Runner falls back to a bare RealRunner{} here, and the
	// production callers hand in obs.Runner(deps.RealRunner{}) themselves so
	// the door is still observed end to end.
	Runner Runner
}

// selfDoctorTimeout resolves the effective self-doctor bound: an explicit
// SelfDoctorTimeout wins, then the general Timeout (existing test setups
// already drive the self-doctor probe through it), then the 30s default.
func selfDoctorTimeout(options ProbeOptions) time.Duration {
	if options.SelfDoctorTimeout > 0 {
		return options.SelfDoctorTimeout
	}
	if options.Timeout > 0 {
		return options.Timeout
	}
	return DefaultSelfDoctorTimeout
}

// Probe resolves and runs every applicable registry entry with a per-command
// timeout. It never maps execution or parse failures to absence.
func Probe(ctx context.Context, entries []Entry, options ProbeOptions) []Result {
	if options.GOOS == "" {
		options.GOOS = runtime.GOOS
	}
	if options.LookPath == nil {
		options.LookPath = exec.LookPath
	}
	results := make([]Result, 0, len(entries))
	for index := range entries {
		entry := entries[index]
		result := Result{Entry: entry}
		switch {
		case !entry.AppliesTo(options.GOOS):
			result.State = StateSkipped
			result.Error = "not this platform"
		case entry.Engine != "" && options.SkipEngines[entry.Engine]:
			result.State = StateSkipped
			result.Error = "--skip-engine " + pfmengine.MustLookup(entry.Engine).LongName
		case entry.Harvest && options.SkipHarvest:
			result.State = StateSkipped
			result.Error = "--skip-harvest"
		case entry.Harvest && options.Provisioning:
			result.State = StateSkipped
			result.Error = "provisioned by install"
		default:
			result = probeOne(ctx, entry, options)
		}
		results = append(results, result)
	}
	return results
}

func probeOne(ctx context.Context, entry Entry, options ProbeOptions) Result {
	result := Result{Entry: entry, ExitCode: -1}
	path, err := options.LookPath(entry.Command)
	if err != nil {
		result.State = StateMissing
		if info, statErr := os.Stat(
			entry.Command,
		); (statErr == nil && !info.IsDir()) ||
			(statErr != nil && !errors.Is(statErr, os.ErrNotExist)) {
			result.State = StateBroken
			result.Error = err.Error()
		} else if !errors.Is(
			err,
			exec.ErrNotFound,
		) {
			result.State = StateBroken
			result.Error = err.Error()
		}
		return result
	}
	result.Path = path
	if len(entry.VersionArgs) != 0 {
		output, runErr := boundedOutput(ctx, options.Timeout, options.Runner, path, entry.VersionArgs...)
		result.Raw = string(output)
		if verboseErr := writeVerbose(options.VerboseDir, entry.Name+"-version", output); verboseErr != nil {
			result.VerboseErr = verboseErr.Error()
		}
		if runErr != nil {
			if termination, ok := runErr.(probeContextError); ok {
				if termination.ownTimeout {
					result.State = StateTimeout
					result.Error = fmt.Sprintf("timeout (%s)", effectiveTimeout(options.Timeout))
				} else {
					result.State = StateCancelled
					result.Error = parentContextError(termination.err)
				}
				return result
			}
			if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
				result.State = StateCancelled
				result.Error = parentContextError(runErr)
				return result
			}
			result.State = StateBroken
			result.ExitCode = ExitCode(runErr)
			result.Error = commandError(runErr, output)
			return result
		}
		version, parseErr := entry.Parse(string(output))
		if parseErr != nil {
			result.State = StateBroken
			result.Error = parseErr.Error()
			return result
		}
		result.Version = version
		if entry.MinVersion != "" && !atLeast(version, entry.MinVersion) {
			result.State = StateBroken
			result.Error = fmt.Sprintf("version %s is below minimum %s", version, entry.MinVersion)
			return result
		}
	}
	result.State = StateOK
	if len(entry.SelfDoctorArgs) != 0 {
		var selfDoctorRaw string
		result.SelfDoctor, selfDoctorRaw, err = probeSelfDoctor(
			ctx,
			path,
			entry,
			options.VerboseDir,
			selfDoctorTimeout(options),
			options.Runner,
		)
		if err != nil {
			result.VerboseErr = err.Error()
		}
		switch {
		case result.SelfDoctor == string(StateCancelled):
			result.State = StateCancelled
			result.Error = selfDoctorRaw
		case result.SelfDoctor == string(StateBroken):
			result.State = StateBroken
			if selfDoctorRaw != "" {
				result.Error = fmt.Sprintf("self-doctor failed raw=%q", selfDoctorRaw)
			} else {
				result.Error = "self-doctor failed"
			}
		case strings.HasPrefix(result.SelfDoctor, "timeout"):
			// The binary already answered --version; a self-doctor summary
			// that merely outran its own bound is not a broken engine and
			// must never block install preflight.
			result.Error = fmt.Sprintf("self-doctor %s", result.SelfDoctor)
		}
	}
	return result
}

// probeSelfDoctor returns the self-doctor status label, the raw first output
// line for a genuine ("broken") failure or parent-stop reason, and any
// verbose-write error. The --help probe's own timeout still reads as
// "broken" — a self-doctor that cannot even answer --help within the bound
// is unsupported or hung, not a legitimate slow summary. Only the summary
// call itself (entry.SelfDoctorArgs) distinguishes a timeout from a real
// failure, because that is the call the regression this guards against
// actually outruns.
func probeSelfDoctor(
	ctx context.Context,
	path string,
	entry Entry,
	verboseDir string,
	timeout time.Duration,
	runner Runner,
) (string, string, error) {
	helpArgs := []string{entry.SelfDoctorArgs[0], "--help"}
	help, helpErr := boundedOutputWithEnvironment(ctx, timeout, terminalEnvironment(), runner, path, helpArgs...)
	if err := writeVerbose(verboseDir, entry.Name+"-self-doctor-help", help); err != nil {
		return "", "", err
	}
	if helpErr != nil {
		if termination, ok := helpErr.(probeContextError); ok && !termination.ownTimeout {
			return string(StateCancelled), parentContextError(termination.err), nil
		}
		if errors.Is(helpErr, context.DeadlineExceeded) {
			return string(StateBroken), "", nil
		}
		var exitErr interface{ ExitCode() int }
		if errors.As(helpErr, &exitErr) {
			return "unavailable", "", nil
		}
		return string(StateBroken), "", nil
	}
	output, err := boundedOutputWithEnvironment(
		ctx,
		timeout,
		terminalEnvironment(),
		runner,
		path,
		entry.SelfDoctorArgs...)
	if writeErr := writeVerbose(verboseDir, entry.Name+"-self-doctor", output); writeErr != nil {
		return "", "", writeErr
	}
	if err != nil {
		if termination, ok := err.(probeContextError); ok && !termination.ownTimeout {
			return string(StateCancelled), parentContextError(termination.err), nil
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return fmt.Sprintf("timeout (%s)", timeout), "", nil
		}
		if strings.Contains(strings.ToLower(string(output)), "tty") ||
			strings.Contains(strings.ToLower(string(output)), "interactive") {
			return "unavailable (interactive-only)", "", nil
		}
		return string(StateBroken), selfDoctorFailureLine(string(output)), nil
	}
	return "ok", "", nil
}

func selfDoctorFailureLine(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.Contains(trimmed, "✗") || strings.Contains(lower, "[fail]") ||
			strings.HasPrefix(lower, "fail") || strings.Contains(lower, "error:") {
			return trimmed
		}
	}
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if index == 0 && len(lines) > 1 && strings.Contains(strings.ToLower(trimmed), "doctor") {
			continue
		}
		return trimmed
	}
	return ""
}

// effectiveTimeout is the single truth for the bound a probe actually ran
// under, so the duration named in a timeout report is the one enforced rather
// than the one requested.
func effectiveTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return ProbeTimeout
	}
	return timeout
}

func boundedOutput(
	parent context.Context,
	timeout time.Duration,
	runner Runner,
	path string,
	args ...string,
) ([]byte, error) {
	return boundedOutputWithEnvironment(parent, timeout, nil, runner, path, args...)
}

// boundedOutputWithEnvironment runs path+args through runner (nil defaults to
// a bare RealRunner{} — see ProbeOptions.Runner's doc comment for why this
// package cannot default to the observed door itself) and joins stdout with
// stderr, the same shape exec.Cmd.CombinedOutput gave every caller before
// this crossed the Runner seam. runner.Run folds an ordinary non-zero exit
// into RunResult.ExitCode rather than an error (deps.Runner's documented
// contract), so a completed-but-failing command is reported back here as a
// runnerExitStatus — the *exec.ExitError-shaped signal probeOne and
// probeSelfDoctor already classify on.
func boundedOutputWithEnvironment(
	parent context.Context,
	timeout time.Duration,
	environment []string,
	runner Runner,
	path string,
	args ...string,
) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, effectiveTimeout(timeout))
	defer cancel()
	if runner == nil {
		runner = RealRunner{}
	}
	result, err := runner.Run(ctx, append([]string{path}, args...), RunOptions{Env: environment})
	output := append(result.Stdout, result.Stderr...)
	if ctx.Err() != nil {
		if parentErr := parent.Err(); parentErr != nil {
			return output, probeContextError{err: parentErr}
		}
		return output, probeContextError{err: context.DeadlineExceeded, ownTimeout: true}
	}
	if err != nil {
		return output, err
	}
	if result.ExitCode != 0 {
		return output, runnerExitStatus{exitCode: result.ExitCode}
	}
	return output, nil
}

// runnerExitStatus is a completed command's non-zero exit, carried back
// through the Runner seam the same way probeOne and probeSelfDoctor already
// read one off a bare exec.CommandContext's *exec.ExitError — via ExitCode(),
// the "any error naming its own exit code" duck type internal/update and
// internal/installer already use for the same reason.
type runnerExitStatus struct {
	exitCode int
}

func (status runnerExitStatus) Error() string {
	return fmt.Sprintf("exit status %d", status.exitCode)
}

func (status runnerExitStatus) ExitCode() int { return status.exitCode }

type probeContextError struct {
	err        error
	ownTimeout bool
}

func (err probeContextError) Error() string { return err.err.Error() }

func (err probeContextError) Unwrap() error { return err.err }

func parentContextError(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "cancelled by parent context"
	case errors.Is(err, context.DeadlineExceeded):
		return "parent context deadline exceeded before probe timeout"
	default:
		return fmt.Sprintf("probe stopped by parent context: %v", err)
	}
}

// terminalEnvironment gives interactive engine doctors a real terminal
// capability description even though PFM itself runs them without a TTY.
// Inheriting TERM=dumb makes Codex report a host defect that is not present.
func terminalEnvironment() []string {
	environment := os.Environ()
	for index, value := range environment {
		if strings.HasPrefix(value, "TERM=") {
			environment[index] = "TERM=xterm-256color"
			return environment
		}
	}
	return append(environment, "TERM=xterm-256color")
}

// ExitCode reads a process exit code out of err, or -1 when err never
// reached one (a lookup failure, timeout, or cancellation, none of which
// ran the command to completion). It reads both a bare exec.CommandContext's
// *exec.ExitError and a Runner-crossed runnerExitStatus, so probeOne's own
// VersionArgs branch reports the real code either way.
func ExitCode(err error) int {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return coded.ExitCode()
	}
	return -1
}

func commandError(err error, output []byte) string {
	line := FirstLine(string(output))
	if line == "" {
		return err.Error()
	}
	return fmt.Sprintf("%v raw=%q", err, line)
}

func writeVerbose(directory, name string, output []byte) error {
	if len(output) == 0 {
		return nil
	}
	safe := regexp.MustCompile(`[^a-zA-Z0-9._-]+`).ReplaceAllString(name, "-")
	return WriteVerboseFile(directory, safe+".log", output)
}

// WriteVerboseFile is the one raw-filesystem writer for ad hoc verbose
// evidence outside a dependency probe's own name+".log" convention (see
// writeVerbose above) — a no-op when directory is empty. name is written
// exactly as given; the caller owns its shape (a fixed evidence filename like
// "harness-prompt.stderr"), never untrusted input, so it is neither
// sanitized nor suffixed here. Centralizing this keeps cmd/pfm's raw
// os.WriteFile count at zero — every verbose-evidence write routes through
// this package, the same discipline atomicfile.Write applies to a whole-file
// replace.
func WriteVerboseFile(directory, name string, output []byte) error {
	if directory == "" {
		return nil
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create verbose directory: %w", err)
	}
	if err := os.WriteFile(filepath.Join(directory, name), output, 0o600); err != nil {
		return fmt.Errorf("write verbose output %s: %w", name, err)
	}
	return nil
}

// AtLeast reports whether version satisfies minimum, comparing the numeric
// fields of each in order. It is the ONE version comparison in pfm: a second
// one disagrees the moment a pre-release suffix appears ("0.2.73-beta" splits
// to a non-numeric third field, which a naive Atoi reads as 0), and two
// answers to "is the installed tool new enough" means doctor and install
// disagree about the same binary.
func AtLeast(version, minimum string) bool { return atLeast(version, minimum) }

func atLeast(version, minimum string) bool {
	left := numericVersion(version)
	right := numericVersion(minimum)
	for index := 0; index < len(left) || index < len(right); index++ {
		var l, r int
		if index < len(left) {
			l = left[index]
		}
		if index < len(right) {
			r = right[index]
		}
		if l != r {
			return l > r
		}
	}
	return true
}

func numericVersion(value string) []int {
	fields := regexp.MustCompile(`\d+`).FindAllString(value, -1)
	result := make([]int, 0, len(fields))
	for _, field := range fields {
		number, _ := strconv.Atoi(field)
		result = append(result, number)
	}
	return result
}
