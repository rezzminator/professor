package professor

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// renderProjectReport is the one report-writing path both entries share: it
// builds the report, captures the pinned→current diff for every UPDATED
// item, and writes the human or JSON body. Both callers use projectReportExit
// for the same clean/review/failure mapping. A non-nil scan adds retired-name
// hits in unpinned files as review items and unreadable files as failures.
func renderProjectReport(
	root, home string,
	jsonOutput bool,
	stdout io.Writer,
	scan RetiredNameScanner,
) (reviewRequired, failed bool) {
	report, err := buildProjectReport(root, home)
	if err != nil {
		writeProjectFailure(stdout, jsonOutput, err)
		return false, true
	}
	diffFailed := captureProjectDiffs(&report)
	scanFailed := captureRetiredNames(&report, scan)
	if jsonOutput {
		if err := writeProjectJSON(stdout, report); err != nil {
			writeProjectFailure(stdout, false, err)
			return false, true
		}
	} else {
		writeProjectHuman(stdout, report)
	}
	return report.reviewRequired() != 0, diffFailed || scanFailed
}

// RetiredNameHit is one retired command, agent, skill, phase, tool or path
// name found on one line of a project file the baseline does not pin.
type RetiredNameHit struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Successor string `json:"successor,omitempty"`
}

// RetiredNameFailure is one file or directory the retired-name scan could
// not read: the scan of it did not happen, so it is never reported clean.
type RetiredNameFailure struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

// RetiredNameScan is the retired-name pass's result; both slices are non-nil
// once a scan ran.
type RetiredNameScan struct {
	Hits     []RetiredNameHit     `json:"hits"`
	Failures []RetiredNameFailure `json:"failures"`
}

// RetiredNameScanner scans root's files for retired names, skipping every
// slash-separated root-relative path in pinned. The registry and the scan
// live in internal/update (update.ScanRetiredNames), which imports this
// package, so the caller passes it in.
type RetiredNameScanner func(root string, pinned map[string]bool) RetiredNameScan

// captureRetiredNames runs scan over the project, skipping every pinned
// local file (the item diff already covers it), and records the result on
// the report. A nil scan records nothing: the report then carries no
// retired-name section at all, never an empty one.
func captureRetiredNames(report *projectReport, scan RetiredNameScanner) (anyFailed bool) {
	if scan == nil {
		return false
	}
	pinned := make(map[string]bool, len(report.Items))
	for index := range report.Items {
		if local := report.Items[index].Local; local != "" {
			pinned[local] = true
		}
	}
	result := scan(report.Root, pinned)
	if result.Hits == nil {
		result.Hits = []RetiredNameHit{}
	}
	if result.Failures == nil {
		result.Failures = []RetiredNameFailure{}
	}
	report.Retired = &result
	return len(result.Failures) != 0
}

// writeRetiredNamesHuman is the human report's RETIRED-NAME block: the hit
// count, one `path:line` row per hit, then one UNREADABLE row per file the
// scan could not read. A report built without a scan writes nothing.
func writeRetiredNamesHuman(stdout io.Writer, scan *RetiredNameScan) {
	if scan == nil {
		return
	}
	fmt.Fprintf(stdout, "  %-13s %d\n", "RETIRED-NAME", len(scan.Hits))
	for _, hit := range scan.Hits {
		successor := "no successor"
		if hit.Successor != "" {
			successor = "now " + hit.Successor
		}
		fmt.Fprintf(stdout, "    %s:%d   %s — retired %s, %s\n", hit.Path, hit.Line, hit.Name, hit.Kind, successor)
	}
	for _, failure := range scan.Failures {
		fmt.Fprintf(stdout, "    %s   retired-name scan UNREADABLE — %s\n", failure.Path, failure.Error)
	}
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
		raw, missing, pinErr := adoptGitShowTemplate(
			report.Store.runner,
			report.Store.Root,
			item.Pin.PinnedSHA,
			item.Template,
		)
		if pinErr != nil {
			item.DiffError = pinErr.Error()
			anyFailed = true
			continue
		}
		if missing || fmt.Sprintf("sha256:%x", sha256.Sum256(raw)) != item.Pin.TemplateHash {
			item.DiffSkipped = "pin was taken from uncommitted or untracked template bytes; committed history cannot show its exact change"
			continue
		}
		item.Diff = diff
	}
	return anyFailed
}

// projectTerminal is the report's one JSON terminal, first match wins: any
// UPDATED item whose diff could not be read, or any file the retired-name
// scan could not read, makes it FAILED (never counted as review), else
// REVIEW REQUIRED (retired-name hits included), else clean.
// writeProjectHuman appends "; nothing was written." to the first two.
func projectTerminal(report projectReport) string {
	unreadable := 0
	for index := range report.Items {
		if report.Items[index].DiffError != "" {
			unreadable++
		}
	}
	var failures []string
	if unreadable != 0 {
		failures = append(failures, fmt.Sprintf("%d item(s) could not be read", unreadable))
	}
	if report.Retired != nil && len(report.Retired.Failures) != 0 {
		failures = append(
			failures,
			fmt.Sprintf("%d file(s) could not be scanned for retired names", len(report.Retired.Failures)),
		)
	}
	if len(failures) != 0 {
		return "FAILED — " + strings.Join(failures, ", ")
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
	env := storeGitEnv(store.Root)
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
	reviewRequired, failed := renderProjectReport(root, home, jsonOutput, stdout, nil)
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
// or store unreadable, any UPDATED diff unreadable, any file scan could not
// read). It resolves the root itself and shares its report writing with
// renderProjectCheck; scan (update.ScanRetiredNames from doctor) adds the
// retired-name hits in the project's unpinned files.
func RunProjectUpdates(rootFlag, home string, jsonOutput bool, stdout io.Writer, scan RetiredNameScanner) int {
	root, found, err := ResolveProjectRoot(rootFlag)
	if err != nil {
		writeProjectFailure(stdout, jsonOutput, err)
		return 3
	}
	if !found {
		writeProjectFailure(stdout, jsonOutput, errBaselineNotFound)
		return 3
	}
	reviewRequired, failed := renderProjectReport(root, home, jsonOutput, stdout, scan)
	return projectReportExit(reviewRequired, failed)
}

func writeUnavailablePinHistory(stdout io.Writer, report projectReport, item projectReportItem) {
	fmt.Fprintf(
		stdout,
		"      upstream change UNAVAILABLE — %s; compare by hand: diff %s %s\n",
		item.DiffSkipped,
		filepath.Join(
			report.Root,
			filepath.FromSlash(item.Local),
		),
		filepath.Join(report.Store.Templates, filepath.FromSlash(item.Template)),
	)
}

func writeProjectJSON(stdout io.Writer, r projectReport) error {
	type blueprintJSON struct {
		Pinned  string `json:"pinned"`
		Current string `json:"current"`
	}
	payload := struct {
		Professor      string                `json:"professor"`
		Blueprint      blueprintJSON         `json:"blueprint"`
		Counts         map[projectStatus]int `json:"counts"`
		Items          []projectReportItem   `json:"items"`
		Ignored        []string              `json:"ignored"`
		RetiredNames   *RetiredNameScan      `json:"retiredNames,omitempty"`
		ReviewRequired int                   `json:"reviewRequired"`
		Terminal       string                `json:"terminal"`
	}{
		r.Root,
		blueprintJSON{r.Baseline.Blueprint.SHA, r.Store.SHA},
		r.Counts, r.Items, r.Baseline.Ignored,
		r.Retired, r.reviewRequired(), "",
	}
	payload.Terminal = projectTerminal(r)
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return fmt.Errorf("encode project report: %w", err)
	}
	return nil
}
