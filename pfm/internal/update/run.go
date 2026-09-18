package update

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"hostops/pfm/internal/atomicfile"
	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/config"
	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/installer"
	"hostops/pfm/internal/professor"
	"hostops/pfm/internal/semver"
)

const (
	checkAction    = "check"
	doctorCommand  = "doctor"
	updateCommand  = "update"
	installCommand = "install"
	jsonFormat     = "json"
)

// These seams keep update tests entirely inside their throwaway repositories;
// production uses the real build/install/doctor functions below.
var (
	updateBuildCandidate  = buildUpdateCandidate
	updateApplyInstall    = applyUpdateInstall
	updateRunDoctor       = runUpdateDoctor
	updateBaselineDoctor  = runUpdateBaselineDoctor
	updateRollbackInstall = applyUpdateInstall
	updateRollbackDoctor  = runUpdateDoctor
)

// Run replaces the installed binary with a reproducible candidate, doctors it, and rolls back an untrusted verdict.
func Run(args []string, stdout, stderr io.Writer, runtimes ...config.Runtime) int {
	if len(args) > 0 {
		switch args[0] {
		case checkAction, "adopt", "pin", "ignore", "drop":
			runtime, err := config.OptionalRuntime(runtimes)
			if err != nil {
				fmt.Fprintf(stderr, "pfm update: config: %v\n", err)
				return 1
			}
			return professor.RunProjectUpdate(args[0], args[1:], stdout, stderr, runtime)
		}
	}
	flags := cli.NewFlagSet(
		updateCommand,
		"usage: pfm update [--to vX.Y.Z] [--repo PATH] [--skip-harvest] [--root DIR] [--json]\n       pfm update {check|adopt|pin|ignore|drop} [options]",
		stderr,
	)
	target := flags.String("to", "", "target semantic-version tag")
	repoFlag := flags.String("repo", "", "source clone to update")
	skipHarvest := flags.Bool("skip-harvest", false, "leave the optional harvestpy runtime unmanaged")
	projectRoot := flags.String("root", "", "project root used for the post-update template report")
	jsonOutput := flags.Bool(jsonFormat, false, "write the project report as one JSON object")
	positional, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(positional) != 0 {
		flags.Usage()
		return 2
	}
	runtime, err := config.OptionalRuntime(runtimes)
	if err != nil {
		fmt.Fprintf(stderr, "pfm update: config: %v\n", err)
		return 1
	}
	repo := strings.TrimSpace(*repoFlag)
	if repo == "" {
		repo, err = installer.ReadSourceRepoMarker(runtime.Paths.Home)
		if err != nil {
			fmt.Fprintf(stderr, "pfm update: %v\n", err)
			return 1
		}
	}
	repo, err = filepath.Abs(repo)
	if err != nil {
		fmt.Fprintf(stderr, "pfm update: resolve repository: %v\n", err)
		return 1
	}
	if err := updateRepository(context.Background(), repo, *target, *skipHarvest, stdout, stderr, runtime); err != nil {
		fmt.Fprintf(stderr, "pfm update: %v\n", err)
		return 1
	}
	postArgs := []string{"--root", *projectRoot}
	if *jsonOutput {
		postArgs = append(postArgs, "--json")
	}
	return professor.RunProjectUpdate("", postArgs, stdout, stderr, runtime)
}

func updateRepository(
	ctx context.Context,
	repo, requestedTag string,
	skipHarvest bool,
	stdout, stderr io.Writer,
	runtime config.Runtime,
) (err error) {
	previousRef, err := updateGitOutput(ctx, repo, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return fmt.Errorf("resolve current revision: %w", err)
	}
	previousRef = strings.TrimSpace(previousRef)

	if err := updateGitRun(ctx, repo, "fetch", "--tags"); err != nil {
		return fmt.Errorf("fetch tags: %w", err)
	}
	tagOutput, err := updateGitOutput(ctx, repo, "tag", "--list")
	if err != nil {
		return fmt.Errorf("list tags: %w", err)
	}
	tags := strings.Fields(tagOutput)
	target := strings.TrimSpace(requestedTag)
	if target == "" {
		target, err = semver.SelectHighest(tags)
		if err != nil {
			return fmt.Errorf("resolve latest release: %w", err)
		}
	} else if _, ok := semver.ParseVersion(target); !ok {
		return fmt.Errorf("invalid target tag %q (expected vMAJOR.MINOR.PATCH)", target)
	}
	if !slices.Contains(tags, target) {
		return fmt.Errorf("target tag %q is not present after fetch", target)
	}
	status, err := updateGitOutput(ctx, repo, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("inspect worktree: %w", err)
	}
	if strings.TrimSpace(status) != "" {
		return errors.New("refuse dirty worktree; commit or stash changes before update")
	}
	sourceAlreadyContainsTarget := updateGitRun(ctx, repo, "merge-base", "--is-ancestor", target, previousRef) == nil
	if !sourceAlreadyContainsTarget {
		if err := updateGitRun(ctx, repo, "merge-base", "--is-ancestor", previousRef, target); err != nil {
			return fmt.Errorf("target %s does not fast-forward the current source branch", target)
		}
	} else {
		currentTag, describeErr := updateGitOutput(ctx, repo, "describe", "--tags", "--abbrev=0", previousRef)
		currentVersion, currentOK := semver.ParseVersion(strings.TrimSpace(currentTag))
		targetVersion, targetOK := semver.ParseVersion(target)
		if describeErr == nil && currentOK && targetOK && targetVersion.Less(currentVersion) {
			return fmt.Errorf("target %s would downgrade source from %s", target, strings.TrimSpace(currentTag))
		}
	}

	managedRoot := filepath.Dir(installer.SourceRepoPath(runtime.Paths.Home))
	stage, err := os.MkdirTemp(filepath.Dir(managedRoot), "update-")
	if err != nil {
		return fmt.Errorf("stage update beside managed root: %w", err)
	}
	worktreeAdded := false
	defer func() {
		var cleanupErr error
		if worktreeAdded {
			if removeErr := updateGitRun(
				ctx,
				repo,
				"worktree",
				"remove",
				"--force",
				filepath.Join(stage, "source"),
			); removeErr != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("cleanup staged source worktree: %w", removeErr))
			}
		}
		if removeErr := os.RemoveAll(stage); removeErr != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("cleanup update stage %s: %w", stage, removeErr))
		}
		if cleanupErr == nil {
			return
		}
		if err != nil {
			err = errors.Join(err, cleanupErr)
			return
		}
		fmt.Fprintf(stderr, "pfm update: cleanup warning after successful update: %v\n", cleanupErr)
	}()
	stagedSource := filepath.Join(stage, "source")
	if err := updateGitRun(ctx, repo, "worktree", "add", "--detach", "--quiet", stagedSource, target); err != nil {
		return fmt.Errorf("stage source at %s: %w", target, err)
	}
	worktreeAdded = true
	candidateA := filepath.Join(stage, "pfm-a")
	candidateB := filepath.Join(stage, "pfm-b")
	if err := updateBuildCandidate(ctx, stagedSource, target, candidateA); err != nil {
		return fmt.Errorf("build candidate first pass: %w", err)
	}
	if err := updateBuildCandidate(ctx, stagedSource, target, candidateB); err != nil {
		return fmt.Errorf("build candidate second pass: %w", err)
	}
	hashA, err := fileHash(candidateA)
	if err != nil {
		return fmt.Errorf("hash first candidate: %w", err)
	}
	hashB, err := fileHash(candidateB)
	if err != nil {
		return fmt.Errorf("hash second candidate: %w", err)
	}
	if hashA != hashB {
		return fmt.Errorf("candidate hash mismatch: first=%s second=%s", hashA, hashB)
	}
	if err := updateGitRun(ctx, repo, "worktree", "remove", "--force", stagedSource); err != nil {
		return fmt.Errorf("remove staged source worktree: %w", err)
	}
	worktreeAdded = false

	ledger, err := installer.ReadBinaryOwnership(runtime.Paths.Home)
	if err != nil {
		return err
	}
	if len(ledger.Paths) == 0 {
		return errors.New("binary ownership ledger is empty; refusing to overwrite PATH copies")
	}
	installSourceRepo := preferredUpdateSourceRepo(runtime.Paths.Home, repo)
	replacements := make([]updateReplacement, 0, len(ledger.Paths))
	for _, targetPath := range ledger.Paths {
		if strings.TrimSpace(targetPath) == "" || !filepath.IsAbs(targetPath) {
			return fmt.Errorf("binary ownership ledger contains invalid path %q", targetPath)
		}
		backup := filepath.Join(stage, fmt.Sprintf("previous-%d", len(replacements)))
		if err := copyUpdateFile(targetPath, backup); err != nil {
			return fmt.Errorf("preserve owned binary %s: %w", targetPath, err)
		}
		replacements = append(replacements, updateReplacement{target: targetPath, backup: backup})
	}
	hookSnapshots, err := snapshotUpdateOwnedFiles(runtime)
	if err != nil {
		return fmt.Errorf("snapshot hook files before install: %w", err)
	}
	// Read current health before replacement so candidate deltas exclude old warnings.
	baselineOutcome, baselineErr := updateBaselineDoctor(ctx, runtime, skipHarvest, stdout, stderr)
	switch {
	case baselineErr != nil:
		fmt.Fprintf(
			stderr,
			"pfm update: baseline doctor did not run: %v (no pre-update warning count to compare against)\n",
			baselineErr,
		)
		baselineOutcome = doctorOutcome{}
	case baselineOutcome.Exit == 2:
		fmt.Fprintln(
			stderr,
			"pfm update: baseline doctor exited 2 (usage) — no pre-update warning count to compare against",
		)
		baselineOutcome = doctorOutcome{}
	}
	sourceAdvanced := false
	if !sourceAlreadyContainsTarget {
		if err := updateGitRun(ctx, repo, "merge", "--ff-only", "--quiet", target); err != nil {
			return fmt.Errorf("fast-forward source branch to %s: %w", target, err)
		}
		sourceAdvanced = true
	}
	for index := range replacements {
		if err := copyUpdateFile(candidateA, replacements[index].target); err != nil {
			rollbackErr := rollbackUpdateState(
				ctx,
				repo,
				installSourceRepo,
				previousRef,
				sourceAdvanced,
				replacements,
				nil,
				runtime,
				skipHarvest,
				stdout,
				stderr,
			)
			return updateFailure(
				fmt.Errorf("replace owned binary %s: %w", replacements[index].target, err),
				rollbackErr,
			)
		}
		replacements[index].replaced = true
	}

	installErr := updateApplyInstall(ctx, candidateA, repo, installSourceRepo, runtime, skipHarvest, stdout, stderr)
	recordUpdateHookAfter(hookSnapshots)
	if installErr != nil {
		return updateFailure(
			fmt.Errorf("install --yes after staging: %w", installErr),
			rollbackUpdateState(
				ctx,
				repo,
				installSourceRepo,
				previousRef,
				sourceAdvanced,
				replacements,
				hookSnapshots,
				runtime,
				skipHarvest,
				stdout,
				stderr,
			),
		)
	}
	candidateConfigPath, candidateConfigNote, configPathErr := updateConfigPathAfterInstall(runtime)
	if configPathErr != nil {
		return updateFailure(
			fmt.Errorf("locate config after update: %w", configPathErr),
			rollbackUpdateState(
				ctx,
				repo,
				installSourceRepo,
				previousRef,
				sourceAdvanced,
				replacements,
				hookSnapshots,
				runtime,
				skipHarvest,
				stdout,
				stderr,
			),
		)
	}
	if candidateConfigNote != "" {
		fmt.Fprintln(stdout, candidateConfigNote)
	}
	candidateOutcome, doctorErr := updateRunDoctor(
		ctx,
		candidateA,
		runtime,
		candidateConfigPath,
		skipHarvest,
		stdout,
		stderr,
	)
	if doctorErr != nil {
		return updateFailure(
			fmt.Errorf("doctor after update: %w", doctorErr),
			rollbackUpdateState(
				ctx,
				repo,
				installSourceRepo,
				previousRef,
				sourceAdvanced,
				replacements,
				hookSnapshots,
				runtime,
				skipHarvest,
				stdout,
				stderr,
			),
		)
	}
	switch candidateOutcome.Exit {
	case 3:
		return updateFailure(
			fmt.Errorf("doctor after update: %d failure(s) — see the doctor rows above", candidateOutcome.Failures),
			rollbackUpdateState(
				ctx,
				repo,
				installSourceRepo,
				previousRef,
				sourceAdvanced,
				replacements,
				hookSnapshots,
				runtime,
				skipHarvest,
				stdout,
				stderr,
			),
		)
	case 0, 1:
		fmt.Fprintf(
			stdout,
			"doctor after update: warnings=%d (before update: %d)\n",
			candidateOutcome.Warnings,
			baselineOutcome.Warnings,
		)
		if candidateOutcome.Warnings > baselineOutcome.Warnings {
			newRows := diffNewDoctorWarningRows(baselineOutcome.Output, candidateOutcome.Output)
			if len(newRows) != 0 {
				fmt.Fprintln(stdout, "new warning rows — read them before the next update:")
				for _, row := range newRows {
					fmt.Fprintln(stdout, row)
				}
			}
		}
	default:
		// Unknown/usage exits are not health verdicts.
		return updateFailure(
			fmt.Errorf(
				"doctor after update: exited %d — a doctor that cannot run is not a verdict",
				candidateOutcome.Exit,
			),
			rollbackUpdateState(
				ctx,
				repo,
				installSourceRepo,
				previousRef,
				sourceAdvanced,
				replacements,
				hookSnapshots,
				runtime,
				skipHarvest,
				stdout,
				stderr,
			),
		)
	}
	fmt.Fprintf(stdout, "updated %s from %s\n", target, repo)
	if !sourceAlreadyContainsTarget {
		previousTag, notePaths, notesErr := releaseNotesForUpdate(ctx, repo, previousRef, target)
		switch {
		case notesErr != nil:
			fmt.Fprintf(
				stdout,
				"release notes: cannot list (%v) — read every releases/v*.md in %s newer than your previous install\n",
				notesErr,
				repo,
			)
		case len(notePaths) == 0:
			fmt.Fprintf(stdout, "release notes: none between %s and %s\n", previousTag, target)
		default:
			fmt.Fprintf(
				stdout,
				"release notes to read (%s → %s, %d release(s)):\n",
				previousTag,
				target,
				len(notePaths),
			)
			for _, path := range notePaths {
				fmt.Fprintf(stdout, "  %s\n", filepath.Join(repo, path))
			}
		}
	}
	return nil
}

// releaseNotesForUpdate resolves the two git calls the release-notes report
// needs — the tag previousRef was installed from, and the releases/ listing
// at target — then hands the pure filtering off to update.ReleaseNotes.
func releaseNotesForUpdate(
	ctx context.Context,
	repo, previousRef, target string,
) (previous string, paths []string, err error) {
	previousTag, err := updateGitOutput(ctx, repo, "describe", "--tags", "--abbrev=0", previousRef)
	if err != nil {
		return "", nil, fmt.Errorf("describe previous release: %w", err)
	}
	previousTag = strings.TrimSpace(previousTag)
	listing, err := updateGitOutput(ctx, repo, "ls-tree", "--name-only", target, "releases/")
	if err != nil {
		return previousTag, nil, fmt.Errorf("list release notes at %s: %w", target, err)
	}
	paths, err = ReleaseNotes(previousTag, target, strings.Split(listing, "\n"))
	return previousTag, paths, err
}

type updateReplacement struct {
	target   string
	backup   string
	replaced bool
}

func updateFailure(primary, rollbackErr error) error {
	if rollbackErr != nil {
		return fmt.Errorf(
			"%w; rollback residue: %v; manually repair the reported update-owned state",
			primary,
			rollbackErr,
		)
	}
	return fmt.Errorf("%w; rolled back update-owned changes", primary)
}

func rollbackUpdateReplacements(replacements []updateReplacement, stderr io.Writer) error {
	var rollbackErr error
	for index := len(replacements) - 1; index >= 0; index-- {
		replacement := replacements[index]
		if !replacement.replaced {
			continue
		}
		if err := copyUpdateFile(replacement.backup, replacement.target); err != nil {
			rollbackErr = errors.Join(rollbackErr, fmt.Errorf("%s: %w", replacement.target, err))
			continue
		}
		fmt.Fprintf(stderr, "pfm update: rolled back %s\n", replacement.target)
	}
	return rollbackErr
}

// rollbackUpdateState first restores every owned binary and the source, then
// the hook files the candidate's install rewrote, and only then uses the prior
// binary's embedded installer to converge installer-owned host wiring back to
// the previous release. The hook restore has to come first: that installer
// recognises only hooks IT generates, so a hook only the newer release knows
// would survive it. A clean doctor is part of rollback proof; without it,
// updateFailure reports residue instead of claiming a safe rollback.
func rollbackUpdateState(
	ctx context.Context,
	repo, installSourceRepo, previousRef string,
	sourceAdvanced bool,
	replacements []updateReplacement,
	hookSnapshots []updateFileSnapshot,
	runtime config.Runtime,
	skipHarvest bool,
	stdout, stderr io.Writer,
) error {
	rollbackErr := rollbackUpdateReplacements(replacements, stderr)
	if sourceAdvanced {
		if err := updateGitRun(ctx, repo, "reset", "--keep", previousRef); err != nil {
			return errors.Join(rollbackErr, fmt.Errorf("restore source revision %s: %w", previousRef, err))
		}
		fmt.Fprintf(stderr, "pfm update: rolled back source to %s\n", previousRef)
	}
	rollbackErr = errors.Join(rollbackErr, restoreUpdateHookFiles(hookSnapshots, runtime.Paths.Home, stderr))
	if len(replacements) == 0 {
		return errors.Join(rollbackErr, errors.New("no previous binary is available to restore installer state"))
	}
	previousBinary := replacements[0].backup
	if err := updateRollbackInstall(
		ctx,
		previousBinary,
		repo,
		installSourceRepo,
		runtime,
		skipHarvest,
		stdout,
		stderr,
	); err != nil {
		return errors.Join(rollbackErr, fmt.Errorf("reapply previous installer state: %w", err))
	}
	rollbackOutcome, doctorErr := updateRollbackDoctor(
		ctx,
		previousBinary,
		runtime,
		runtime.Config.Path,
		skipHarvest,
		stdout,
		stderr,
	)
	if doctorErr != nil {
		return errors.Join(rollbackErr, fmt.Errorf("doctor after rollback: %w", doctorErr))
	}
	switch rollbackOutcome.Exit {
	case 0:
		// Clean — nothing to report.
	case 1:
		// Warnings alone are never residue; they are reported (via the tee to
		// stdout above), not claimed as a rollback failure. A rollback to a
		// binary that predates M2's failure tiers ALSO exits 1 on warnings
		// alone, so that case is named rather than misreported as residue.
		if rollbackDoctorPredatesFailureTiers(rollbackOutcome.Output) {
			return errors.Join(
				rollbackErr,
				errors.New(
					"doctor after rollback exited 1 (an older pfm exits 1 on warnings alone; read the rows above before repairing anything)",
				),
			)
		}
	default:
		return errors.Join(
			rollbackErr,
			fmt.Errorf("doctor after rollback: exited %d — see the doctor rows above", rollbackOutcome.Exit),
		)
	}
	return rollbackErr
}

func preferredUpdateSourceRepo(home, repo string) string {
	recorded, err := installer.ReadSourceRepoMarker(home)
	if err != nil {
		return repo
	}
	recordedInfo, recordedErr := os.Stat(recorded)
	repoInfo, repoErr := os.Stat(repo)
	if recordedErr == nil && repoErr == nil && os.SameFile(recordedInfo, repoInfo) {
		return recorded
	}
	return repo
}

func updateGitRun(ctx context.Context, repo string, args ...string) error {
	result, err := currentUpdateRunner().Run(ctx, append([]string{"git"}, args...), deps.RunOptions{Dir: repo})
	if err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf(
			"git %s: exited %d: %s",
			strings.Join(args, " "),
			result.ExitCode,
			strings.TrimSpace(string(result.Stdout)+string(result.Stderr)),
		)
	}
	return nil
}

func updateGitOutput(ctx context.Context, repo string, args ...string) (string, error) {
	result, err := currentUpdateRunner().Run(ctx, append([]string{"git"}, args...), deps.RunOptions{Dir: repo})
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf(
			"git %s: exited %d: %s",
			strings.Join(args, " "),
			result.ExitCode,
			strings.TrimSpace(string(result.Stdout)+string(result.Stderr)),
		)
	}
	return string(result.Stdout), nil
}

func buildUpdateCandidate(ctx context.Context, repo, version, output string) error {
	moduleRoot := repo
	if _, err := os.Stat(filepath.Join(repo, "pfm", "go.mod")); err == nil {
		moduleRoot = filepath.Join(repo, "pfm")
	}
	// The staged worktree needs explicit -buildvcs=false; GOFLAGS is cleared.
	result, err := currentUpdateRunner().Run(ctx, []string{
		"go", "-C", moduleRoot, "build", "-trimpath", "-buildvcs=false", "-ldflags", "-X main.version=" + version,
		"-o", output, "./cmd/pfm",
	}, deps.RunOptions{Dir: repo, Env: envWithEmptyGOFLAGS()})
	if err != nil {
		return fmt.Errorf("go build: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf(
			"go build: exited %d: %s",
			result.ExitCode,
			strings.TrimSpace(string(result.Stdout)+string(result.Stderr)),
		)
	}
	return nil
}

func envWithEmptyGOFLAGS() []string {
	return deps.EnvironmentWith("GOFLAGS", "")
}

func fileHash(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

func copyUpdateFile(source, target string) error {
	raw, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	return atomicfile.Write(target, raw, info.Mode().Perm())
}

// applyUpdateInstall runs a candidate or rollback binary's `install --yes`,
// forwarding --config only when runtime.Config.Path actually exists — a
// defaults-only host (nothing to migrate or restore) must not have that
// forwarded path read as operator-explicit and trip install_command.go's B
// refusal; a real file is still forwarded so migration/restore keep working.
func applyUpdateInstall(
	ctx context.Context,
	candidate, repo, sourceRepo string,
	runtime config.Runtime,
	skipHarvest bool,
	stdout, stderr io.Writer,
) error {
	args := []string{"--yes"}
	if skipHarvest {
		args = append(args, "--skip-harvest")
	}
	configPath := ""
	if runtime.Config.Exists {
		configPath = runtime.Config.Path
	}
	return runUpdateCandidateCommand(
		ctx,
		candidate,
		configPath,
		repo,
		sourceRepo,
		stdout,
		stderr,
		installCommand,
		args...)
}

// runUpdateDoctor runs candidate's `doctor` and turns its exit code and
// captured stdout into a doctorOutcome. A non-zero doctor exit is a verdict,
// not a Go error — runUpdateCandidateCommand hands it back as a
// *doctorExitError precisely so this seam can read it as one; only a genuine
// spawn failure (candidate never ran at all) returns a non-nil error here.
// configPath is the caller's explicit choice (updateConfigPathAfterInstall's
// re-resolved path for the post-install candidate doctor, runtime.Config.Path
// otherwise) — never derived from runtime here.
func runUpdateDoctor(
	ctx context.Context,
	candidate string,
	_ config.Runtime,
	configPath string,
	skipHarvest bool,
	stdout, stderr io.Writer,
) (doctorOutcome, error) {
	var args []string
	if skipHarvest {
		args = []string{"--skip-harvest"}
	}
	// Doctor normally inspects the current repository's publication hook as
	// well as host health. An update is validating the newly installed host
	// binary, not whichever source checkout invoked it, so run from a fresh
	// non-repository directory and keep an unwired maintainer checkout from
	// rolling back an otherwise healthy update.
	doctorDir, err := os.MkdirTemp("", "pfm-update-doctor-")
	if err != nil {
		return doctorOutcome{}, fmt.Errorf("create isolated doctor directory: %w", err)
	}
	defer func() {
		if cleanupErr := os.RemoveAll(doctorDir); cleanupErr != nil {
			fmt.Fprintf(stderr, "pfm update: cleanup isolated doctor directory %s: %v\n", doctorDir, cleanupErr)
		}
	}()
	runErr := runUpdateCandidateCommand(
		ctx,
		candidate,
		configPath,
		doctorDir,
		"",
		stdout,
		stderr,
		doctorCommand,
		args...)
	var exitErr *doctorExitError
	switch {
	case runErr == nil:
		return doctorOutcome{Exit: 0}, nil
	case errors.As(runErr, &exitErr):
		warnings, failures := parseDoctorTally(exitErr.output)
		return doctorOutcome{Exit: exitErr.code, Warnings: warnings, Failures: failures, Output: exitErr.output}, nil
	default:
		return doctorOutcome{}, runErr
	}
}

// runUpdateBaselineDoctor is updateBaselineDoctor's production seam: it runs
// the CURRENTLY RUNNING binary's own doctor, in a subprocess, before the
// update touches anything. It must run before any owned binary is replaced —
// os.Executable() resolves to a real file path, and a doctor run after
// replacement would read the NEW candidate, defeating the baseline. A
// baseline that cannot run is never fatal to the update: the caller treats a
// non-nil error, or an exit code doctor never returns a verdict on, as "no
// baseline to compare against."
func runUpdateBaselineDoctor(
	ctx context.Context,
	runtime config.Runtime,
	skipHarvest bool,
	stdout, stderr io.Writer,
) (doctorOutcome, error) {
	self, err := os.Executable()
	if err != nil {
		return doctorOutcome{}, fmt.Errorf("resolve current binary for baseline doctor: %w", err)
	}
	return runUpdateDoctor(ctx, self, runtime, runtime.Config.Path, skipHarvest, stdout, stderr)
}
