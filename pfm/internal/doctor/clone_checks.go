package doctor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/semver"
)

// cloneProbeTimeout bounds every git or gh call the clone rows make.
const cloneProbeTimeout = 10 * time.Second

// pfmBuild is what the running pfm says it was built from: the stamped
// version (config.DevelopmentVersion when unstamped) and the VCS revision Go
// embeds, empty when the build carried none.
type pfmBuild struct {
	Version  string
	Revision string
	Modified bool
}

// currentPfmBuild reads the running binary's VCS stamp beside its version.
func currentPfmBuild(version string) pfmBuild {
	build := pfmBuild{Version: version}
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				build.Revision = setting.Value
			case "vcs.modified":
				build.Modified = setting.Value == printedTrue
			}
		}
	}
	return build
}

const (
	gitCommand = "git"
	// printedTrue is a true boolean as Go's build info and gh's --jq print it.
	printedTrue = "true"
)

// printProfessorCloneDoctor prints the rows about the professor clone pfm
// installs from (paths.GlobalSourceRepo): pfm_build, git_push and
// leak_terms. A clone that is not there is one named row and no count: there
// is nothing to compare, push or scan; a copy with no .git has no HEAD or
// origin, so only leak_terms runs.
func printProfessorCloneDoctor(
	ctx context.Context, stdout io.Writer, home string, build pfmBuild, runner deps.Runner, filter warningFilter,
) (warnings int) {
	clone, err := paths.GlobalSourceRepo(home)
	if err != nil {
		fmt.Fprintf(
			stdout,
			"doctor: professor_clone UNREADABLE error=%v — pfm_build, git_push and leak_terms not checked\n",
			err,
		)
		return 1
	}
	if info, statErr := os.Stat(clone); errors.Is(statErr, fs.ErrNotExist) {
		fmt.Fprintf(
			stdout,
			"doctor: professor_clone none at %s — pfm_build, git_push and leak_terms do not apply\n",
			clone,
		)
		return 0
	} else if statErr != nil || !info.IsDir() {
		fmt.Fprintf(
			stdout,
			"doctor: professor_clone UNREADABLE %s error=%v — pfm_build, git_push and leak_terms not checked\n",
			clone,
			statErr,
		)
		return 1
	}
	if _, gitErr := os.Lstat(filepath.Join(clone, ".git")); errors.Is(gitErr, fs.ErrNotExist) {
		fmt.Fprintf(
			stdout,
			"doctor: professor_clone %s is not a git checkout — pfm_build and git_push do not apply\n",
			clone,
		)
		return printLeakTermsDoctor(stdout, clone, filter)
	}
	return printPfmBuildDoctor(ctx, stdout, clone, build, runner) +
		printGitPushDoctor(ctx, stdout, clone, runner, filter) + printLeakTermsDoctor(stdout, clone, filter)
}

// printPfmBuildDoctor compares the running pfm with the clone: its stamped
// version against VERSION, then its vcs.revision against HEAD. Every gap is a
// warning, never a failure: pfm update rolls back on a doctor failure, and an
// update to a release the clone already holds leaves the binary behind it,
// still newer than the one it replaced. An unstamped binary or a probe that
// could not run is never read as current.
func printPfmBuildDoctor(
	ctx context.Context, stdout io.Writer, clone string, build pfmBuild, runner deps.Runner,
) (warnings int) {
	fix := "make -C " + filepath.Join(clone, "pfm") + " host-install"
	raw, err := os.ReadFile(filepath.Join(clone, "VERSION"))
	if err != nil {
		fmt.Fprintf(stdout, "doctor: pfm_build UNVERIFIED clone VERSION unreadable: %v\n", err)
		return 1
	}
	cloneVersion := strings.TrimSpace(string(raw))
	if build.Version == config.DevelopmentVersion || build.Version == "" {
		fmt.Fprintf(stdout, "doctor: pfm_build UNSTAMPED revision=%s — this pfm carries no version stamp, so "+
			"whether it is behind %s (VERSION %s) is unknown — fix: %s\n", orNone(build.Revision), clone, cloneVersion, fix)
		return 1
	}
	buildVersion, cloneSemver := semver.WithV(build.Version), semver.WithV(cloneVersion)
	if !semver.Valid(buildVersion) || !semver.Valid(cloneSemver) {
		fmt.Fprintf(stdout, "doctor: pfm_build UNVERIFIED version=%s clone VERSION=%s — not comparable versions\n",
			build.Version, cloneVersion)
		return 1
	}
	switch order := semver.Compare(buildVersion, cloneSemver); {
	case order < 0:
		fmt.Fprintf(stdout, "doctor: pfm_build BEHIND version=%s clone VERSION=%s — fix: %s\n",
			build.Version, cloneVersion, fix)
		return 1
	case order > 0:
		fmt.Fprintf(stdout, "doctor: pfm_build AHEAD version=%s clone VERSION=%s — %s is older than this pfm — "+
			"fix: update the clone (git -C %s pull), then %s\n", build.Version, cloneVersion, clone, clone, fix)
		return 1
	}
	if _, release := semver.ParseVersion(cloneSemver); release && build.Revision == "" {
		// A release VERSION sits on its release commit alone (develop carries
		// the next -alpha), and pfm update builds without a vcs.revision.
		fmt.Fprintf(
			stdout,
			"doctor: pfm_build ok version=%s = clone VERSION %s (a release: its VERSION names one commit)\n",
			build.Version,
			cloneVersion,
		)
		return 0
	}
	if build.Revision == "" {
		fmt.Fprintf(stdout, "doctor: pfm_build UNVERIFIED version=%s revision=(none) — the binary carries no "+
			"vcs.revision, so whether it is behind HEAD is unknown — fix: %s\n", build.Version, fix)
		return 1
	}
	head, err := cloneGit(ctx, runner, clone, nil, "rev-parse", "HEAD")
	if err != nil {
		fmt.Fprintf(stdout, "doctor: pfm_build UNVERIFIED version=%s revision=%s — could not read HEAD: %v\n",
			build.Version, build.Revision, err)
		return 1
	}
	modified := ""
	if build.Modified {
		modified = " (built from a modified tree)"
	}
	if head == build.Revision {
		fmt.Fprintf(
			stdout,
			"doctor: pfm_build ok version=%s revision=%s = HEAD%s\n",
			build.Version,
			short(head),
			modified,
		)
		return 0
	}
	result, runErr := runCloneGit(ctx, runner, clone, nil, "merge-base", "--is-ancestor", build.Revision, head)
	switch {
	case runErr == nil && result.ExitCode == 0:
		fmt.Fprintf(stdout, "doctor: pfm_build BEHIND version=%s revision=%s HEAD=%s%s — fix: %s\n",
			build.Version, short(build.Revision), short(head), modified, fix)
		return 1
	case runErr == nil && result.ExitCode == 1:
		fmt.Fprintf(stdout, "doctor: pfm_build DIVERGED version=%s revision=%s is not an ancestor of HEAD=%s%s — "+
			"fix: %s\n", build.Version, short(build.Revision), short(head), modified, fix)
		return 1
	}
	fmt.Fprintf(stdout, "doctor: pfm_build UNVERIFIED version=%s revision=%s HEAD=%s — ancestry unknown: %v\n",
		build.Version, short(build.Revision), short(head), gitError(result, runErr))
	return 1
}

// cloneGit runs one git command in the clone and returns its trimmed stdout,
// an error naming the exit when it did not exit 0.
func cloneGit(
	ctx context.Context,
	runner deps.Runner,
	clone string,
	options *deps.RunOptions,
	args ...string,
) (string, error) {
	result, err := runCloneGit(ctx, runner, clone, options, args...)
	if err != nil || result.ExitCode != 0 {
		return "", gitError(result, err)
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

func runCloneGit(
	ctx context.Context, runner deps.Runner, clone string, options *deps.RunOptions, args ...string,
) (deps.RunResult, error) {
	ctx, cancel := context.WithTimeout(ctx, cloneProbeTimeout)
	defer cancel()
	opts := deps.RunOptions{}
	if options != nil {
		opts = *options
	}
	return runner.Run(ctx, append([]string{gitCommand, "-C", clone}, args...), opts)
}

func gitError(result deps.RunResult, err error) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("exit %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
}

func short(revision string) string {
	if len(revision) > 12 {
		return revision[:12]
	}
	return revision
}

func orNone(value string) string {
	if value == "" {
		return "(none)"
	}
	return value
}
