package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// The herdr-integration check keeps Herdr's own bundled integration for
// claude, codex or opencode off the host: when one is installed, Herdr holds
// the pane's agent state under its own source and pfm's reporter is locked out
// of it (docs/design/hooks/hooks.md § Herdr reporter). Herdr is optional: a
// host without it passes.
const (
	herdrIntegrationRow = "doctor: herdr-integration:"
	// herdrStatusBound caps the one subprocess the check starts.
	herdrStatusBound = 20 * time.Second
	// herdrNotInstalled opens the state `herdr integration status` prints for
	// an engine whose bundled integration is absent; every other state
	// (current, outdated, …) is an installed integration.
	herdrNotInstalled = "not installed"
	herdrFixStatus    = "run herdr integration status by hand and repair what it reports, then rerun pfm doctor"
)

// The reporter-errors check reads the per-session error logs the Herdr
// reporter appends to when a report fails (tmux `run-shell -b` discards the
// reporter's stderr, so the log is the only trace of such a failure).
const (
	herdrReporterRow = "doctor: herdr-reporter-errors:"
	// herdrReporterWindowHours is how far back an error still counts.
	herdrReporterWindowHours = 24
	herdrReporterWindow      = herdrReporterWindowHours * time.Hour
)

// herdrJudgedEngines are the engines pfm reports for, in the order the rows
// print; Herdr names them by the engine's long name.
func herdrJudgedEngines() []string {
	return []string{
		herdr.Label(pfmengine.Claude), herdr.Label(pfmengine.Codex), herdr.Label(pfmengine.OpenCode),
	}
}

// printHerdrDoctor prints the herdr-integration rows and returns their warning
// and failure counts. The binary is $HERDR_BIN_PATH (set inside a Herdr pane),
// else `herdr` on doctor's PATH; neither is not a failure. The status command
// printing one `{engine}: {state} ({path})` line per integration is read
// line by line, by the engine word before the first colon; a command that
// fails, or output without a line for a judged engine, is a WARN naming what
// was seen, never read as "not installed".
func printHerdrDoctor(ctx context.Context, stdout io.Writer, dependencies Dependencies) (warnings, failures int) {
	runner := dependencies.Runner
	warn := func(need, checked, key, detail string) {
		warnings++
		fmt.Fprintf(
			stdout,
			"%s WARN need=%s checked=%s %s=%q — fix: %s\n",
			herdrIntegrationRow,
			need,
			checked,
			key,
			detail,
			herdrFixStatus,
		)
	}
	binary, ok := resolveHerdrBinary(runner, dependencies, func(checked, detail, fix string) {
		warnings++
		fmt.Fprintf(
			stdout,
			"%s WARN need=herdr binary checked=%s error=%q — fix: %s\n",
			herdrIntegrationRow,
			checked,
			detail,
			fix,
		)
	})
	if binary == "" {
		if ok {
			fmt.Fprintf(stdout, "%s ok n/a (herdr not installed)\n", herdrIntegrationRow)
		}
		return warnings, failures
	}

	command := "herdr integration status"
	bounded, cancel := context.WithTimeout(ctx, herdrStatusBound)
	defer cancel()
	result, err := runner.Run(bounded, []string{binary, "integration", "status"}, deps.RunOptions{
		WaitDelay: 2 * time.Second, ProcessGroup: true,
	})
	switch {
	case bounded.Err() != nil:
		warn("integration status", command, "error", fmt.Sprintf("timed out after %s", herdrStatusBound))
		return warnings, failures
	case err != nil:
		warn("integration status", command, "error", err.Error())
		return warnings, failures
	case result.ExitCode != 0:
		warnings++
		fmt.Fprintf(stdout, "%s WARN need=integration status checked=%s exit=%d stderr=%q — fix: %s\n",
			herdrIntegrationRow, command, result.ExitCode, capturedLine(result.Stderr, "empty"), herdrFixStatus)
		return warnings, failures
	}

	states := herdrIntegrationStates(result.Stdout)
	var unread []string
	engines := herdrJudgedEngines()
	for _, engine := range engines {
		state, found := states[engine]
		switch {
		case !found:
			unread = append(unread, engine)
		case !strings.HasPrefix(state, herdrNotInstalled):
			failures++
			fmt.Fprintf(
				stdout,
				"%s FAIL need=no bundled %s integration checked=%s state=%q — fix: herdr integration uninstall %s\n",
				herdrIntegrationRow,
				engine,
				command,
				state,
				engine,
			)
		}
	}
	if len(unread) > 0 {
		warn("integration status", command, "error", "no line for "+strings.Join(unread, ", ")+" in the output")
	}
	if warnings == 0 && failures == 0 {
		fmt.Fprintf(
			stdout,
			"%s ok %s: no bundled Herdr integration installed\n",
			herdrIntegrationRow,
			strings.Join(engines, ", "),
		)
	}
	return warnings, failures
}

// resolveHerdrBinary finds the herdr executable: $HERDR_BIN_PATH when set,
// else `herdr` on PATH. An empty binary with ok true means herdr is absent
// (no PATH entry, no variable); a lookup that failed any other way, or a
// variable naming something not executable, is reported through unusable and
// returns ok false.
func resolveHerdrBinary(
	runner deps.Runner, dependencies Dependencies, unusable func(checked, detail, fix string),
) (binary string, ok bool) {
	if configured := dependencies.Env.Get(herdr.EnvBin); configured != "" {
		path, err := runner.LookPath(configured)
		if err != nil {
			unusable(herdr.EnvBin+"="+configured, err.Error(),
				"unset "+herdr.EnvBin+" or point it at the herdr executable, then rerun pfm doctor")
			return "", false
		}
		return path, true
	}
	path, err := runner.LookPath("herdr")
	switch {
	case err == nil:
		return path, true
	case errors.Is(err, exec.ErrNotFound):
		return "", true
	default:
		unusable("herdr on doctor's PATH", err.Error(), "remove the relative entry from PATH, then rerun pfm doctor")
		return "", false
	}
}

// herdrIntegrationStates maps each engine word of a `{engine}: {state}` status
// line to its state text. Lines without a colon are skipped; an installed
// state wins over a "not installed" line of the same engine, whatever the order.
func herdrIntegrationStates(output []byte) map[string]string {
	states := map[string]string{}
	for _, line := range strings.Split(string(output), "\n") {
		engine, state, found := strings.Cut(line, ":")
		engine, state = strings.TrimSpace(engine), strings.TrimSpace(state)
		if !found || engine == "" || state == "" {
			continue
		}
		if previous, seen := states[engine]; !seen || strings.HasPrefix(previous, herdrNotInstalled) {
			states[engine] = state
		}
	}
	return states
}

// printHerdrReporterErrors prints the herdr-reporter-errors row and returns
// its warning count: an ok row when no reporter error is logged in dir within
// herdrReporterWindow of now, else one WARN naming the count, the newest
// error and its session log. An unreadable log directory or log is a WARN
// carrying the read error, never an ok row.
func printHerdrReporterErrors(stdout io.Writer, dir string, now time.Time) (warnings int) {
	records, err := herdr.RecentErrors(dir, now.Add(-herdrReporterWindow))
	switch {
	case err != nil:
		fmt.Fprintf(
			stdout,
			"%s WARN need=readable reporter error log checked=%s error=%q — "+
				"fix: repair or delete the log the error names, then rerun pfm doctor\n",
			herdrReporterRow, dir, err.Error(),
		)
		return 1
	case len(records) == 0:
		fmt.Fprintf(stdout, "%s ok none in the last %dh\n", herdrReporterRow, herdrReporterWindowHours)
		return 0
	}
	newest := records[0]
	fmt.Fprintf(
		stdout,
		"%s WARN need=no reporter errors in the last %dh checked=%s count=%d newest=%q session=%s at=%s — "+
			"fix: read %s and repair what it reports; the row clears %dh after the last error\n",
		herdrReporterRow, herdrReporterWindowHours, dir, len(records), newest.Message, newest.Session,
		newest.At.UTC().Format(time.RFC3339), filepath.Join(dir, newest.Session+herdr.ErrorLogSuffix),
		herdrReporterWindowHours,
	)
	return 1
}
