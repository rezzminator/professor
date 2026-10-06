package professor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmpaths "github.com/rezzminator/professor/pfm/internal/paths"
)

// renderProjectReport is the one report-writing path both entries share: it
// builds the report, captures the pinned→current diff for every UPDATED
// item, and writes the human or JSON body. Both callers use projectReportExit
// for the same clean/review/failure mapping.
func renderProjectReport(root, home string, jsonOutput bool, stdout io.Writer) (reviewRequired, failed bool) {
	report, err := buildProjectReport(root, home)
	if err != nil {
		writeProjectFailure(stdout, jsonOutput, err)
		return false, true
	}
	diffFailed := captureProjectDiffs(&report)
	if jsonOutput {
		if err := writeProjectJSON(stdout, report); err != nil {
			writeProjectFailure(stdout, false, err)
			return false, true
		}
	} else {
		writeProjectHuman(stdout, report)
	}
	return report.reviewRequired() != 0, diffFailed
}

// captureProjectDiffs fills Diff/DiffError/DiffSkipped on every UPDATED item.
// It never runs inside buildProjectReport, whose other callers (PrintDoctor and
// runProjectPin) run no git. A self-hosted pin or store is skipped: its
// review lines stay the existing two-line self-hosted body.
func captureProjectDiffs(report *projectReport) (anyFailed bool) {
	for index := range report.Items {
		item := &report.Items[index]
		if item.Status != projectUpdated {
			continue
		}
		if item.Pin.PinnedSHA == UnknownSelfHostedSHA || report.Store.SHA == UnknownSelfHostedSHA {
			item.DiffSkipped = "self-hosted pin or store: no git history"
			continue
		}
		diff, err := diffUpdatedTemplate(report.Store, *item)
		if err != nil {
			item.DiffError = err.Error()
			anyFailed = true
			continue
		}
		item.Diff = diff
	}
	return anyFailed
}

// projectTerminal is the report's one JSON terminal, first match wins: any
// UPDATED item whose diff could not be read makes it FAILED (counted by
// DiffError, never by the review count), else REVIEW REQUIRED, else clean.
// writeProjectHuman appends "; nothing was written." to the first two.
func projectTerminal(report projectReport) string {
	unreadable := 0
	for index := range report.Items {
		if report.Items[index].DiffError != "" {
			unreadable++
		}
	}
	if unreadable != 0 {
		return fmt.Sprintf("FAILED — %d item(s) could not be read", unreadable)
	}
	if review := report.reviewRequired(); review != 0 {
		return fmt.Sprintf("REVIEW REQUIRED — %d items", review)
	}
	return "clean"
}

// gitObjectName is the only shape a pinned SHA may take before it reaches
// `git diff`: baseline.json ships inside the project, and a value git reads as
// an option (`--output=<file>`) would write wherever the project says.
var gitObjectName = regexp.MustCompile(`^[0-9a-fA-F]{4,64}$`)

// diffUpdatedTemplate runs the pinned→working-tree diff for one UPDATED
// item. The user's gitconfig never shapes it (--no-ext-diff, --no-textconv,
// --no-color: the report and the JSON carry git's plain unified diff). In the dev
// fence it reaches the store's git through the same GIT_DIR contract
// storeSHAWithRunner resolves the store SHA by.
func diffUpdatedTemplate(store Store, item projectReportItem) (string, error) {
	if store.runner == nil {
		return "", errors.New("git diff: runner is nil")
	}
	if !gitObjectName.MatchString(item.Pin.PinnedSHA) {
		return "", fmt.Errorf(
			"git diff: pinned SHA %q in .professor/baseline.json is not a git object name",
			item.Pin.PinnedSHA,
		)
	}
	argv := []string{
		deps.Executable("git"), "diff", "--no-ext-diff", "--no-textconv", "--no-color",
		item.Pin.PinnedSHA, "--", "templates/" + item.Template,
	}
	env := deps.WithoutGitRepoVars(os.Environ())
	if gitDir, useFenceGit := pfmpaths.DevRepoGitDir(store.Root); useFenceGit {
		env = append(env, "GIT_DIR="+gitDir, "GIT_WORK_TREE="+store.Root)
	}
	result, err := store.runner.Run(context.Background(), argv, deps.RunOptions{Dir: store.Root, Env: env})
	if err == nil && result.ExitCode != 0 {
		err = gitExitError{code: result.ExitCode}
	}
	if err != nil {
		return "", adoptGitFailure("git diff", err, string(result.Stderr))
	}
	return string(result.Stdout), nil
}

// renderProjectCheck is the bare `pfm update` post-update report: 0 clean, 1
// review required, 3 failure (an unreadable diff is a failure).
func renderProjectCheck(root, home string, jsonOutput bool, stdout io.Writer) int {
	reviewRequired, failed := renderProjectReport(root, home, jsonOutput, stdout)
	return projectReportExit(reviewRequired, failed)
}

func projectReportExit(reviewRequired, failed bool) int {
	if failed {
		return 3
	}
	if reviewRequired {
		return 1
	}
	return 0
}

// RunProjectUpdates is the only entry for `pfm doctor --project-updates`: 0
// clean, 1 at least one review item, 3 failure (baseline not found, baseline
// or store unreadable, any UPDATED diff unreadable). It resolves the root
// itself and shares its report writing with renderProjectCheck.
func RunProjectUpdates(rootFlag, home string, jsonOutput bool, stdout io.Writer) int {
	root, found, err := ResolveProjectRoot(rootFlag)
	if err != nil {
		writeProjectFailure(stdout, jsonOutput, err)
		return 3
	}
	if !found {
		writeProjectFailure(stdout, jsonOutput, errBaselineNotFound)
		return 3
	}
	reviewRequired, failed := renderProjectReport(root, home, jsonOutput, stdout)
	return projectReportExit(reviewRequired, failed)
}
