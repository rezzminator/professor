package dream

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/dream/apply"
	"hostops/pfm/internal/dream/artifact"
	"hostops/pfm/internal/dream/corpus"
	"hostops/pfm/internal/dream/gate"
	"hostops/pfm/internal/dream/lane"
	"hostops/pfm/internal/dream/organ"
	"hostops/pfm/internal/dream/resources"
	"hostops/pfm/internal/dream/seat"
)

const (
	distillPromptFile = "dreamer-distill.prompt.md"
	refinerPromptFile = "dreamer-refiner.prompt.md"
	refinerSeat       = "refiner"
	gatePhase         = "gate"
	gateVerdictPass   = "PASS"
)

// NightRequest contains values selected by the caller. Night does not read
// process arguments or environment variables and never applies its own stage.
type NightRequest struct {
	RepoRoot      string
	RegistryBase  string
	ResourcesRoot string
	AgentType     string
	Selection     corpus.Selection
	StartedAt     time.Time
}

// NightResult names the complete deterministic outcome. ApplyEligible means
// an explicit later Apply may record the night; Applied is always false here.
type NightResult struct {
	Repo          artifact.RepoContext
	Lane          artifact.LaneContext
	Stage         artifact.StageLayout
	HoldState     artifact.HoldState
	Empty         bool
	ApplyEligible bool
	Applied       bool
	Survivors     int
	Yield         int
}

// NightGitReader is the night's complete read-only Git boundary.
type NightGitReader interface {
	gate.GitObjectReader
	Head(context.Context) (string, error)
}

// NightPreparedSeats exposes exactly one distill and one refiner attempt. The
// implementation owns the no-retry state machine and the shared pin set.
type NightPreparedSeats interface {
	Config() seat.PinnedConfig
	Verification() seat.Verification
	RunDistill(context.Context, seat.SeatInput) (seat.SeatResult, error)
	RunRefiner(context.Context, seat.SeatInput) (seat.SeatResult, error)
}

// NightSeatRunner prepares the verified configuration before either seat.
type NightSeatRunner interface {
	PrepareNight(context.Context, seat.SeatLaw, string) (NightPreparedSeats, error)
}

// NightDependencies is the test seam around time, Git, tmux/Codex, locking,
// and visible output. Filesystem parsing remains in the owning dream packages.
type NightDependencies struct {
	SeatLaw       seat.SeatLaw
	NewSeatRunner func(seat.EventSink) (NightSeatRunner, error)
	Git           func(string) NightGitReader
	Clock         func() time.Time
	AcquireLock   func(string) (func() error, error)
	Stdout        io.Writer
	Stderr        io.Writer
}

type productionSeatRunner struct {
	runner *seat.Runner
}

func (runner productionSeatRunner) PrepareNight(
	ctx context.Context,
	law seat.SeatLaw,
	stage string,
) (NightPreparedSeats, error) {
	return runner.runner.PrepareNight(ctx, law, stage)
}

// DefaultNightDependencies wires production defaults without resolving any
// host path until a non-empty corpus has passed preflight.
func DefaultNightDependencies() NightDependencies {
	return DefaultNightDependenciesWithCodex("")
}

func DefaultNightDependenciesWithCodex(codexBinary string) NightDependencies {
	return NightDependencies{
		SeatLaw: seat.RequiredSeatLaw(),
		NewSeatRunner: func(events seat.EventSink) (NightSeatRunner, error) {
			runner, err := seat.NewDefaultRunnerWithCodex(events, codexBinary)
			if err != nil {
				return nil, err
			}
			return productionSeatRunner{runner: runner}, nil
		},
		Git: func(repo string) NightGitReader {
			return commandNightGit{repo: repo}
		},
		Clock:       time.Now,
		AcquireLock: acquireRunnerLock,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
	}
}

// Night runs one complete, supervised night. Its literal first operation is
// the luna-law check; a violation reaches no dependency or filesystem effect.
func Night(ctx context.Context, request NightRequest, dependencies NightDependencies) (
	result NightResult,
	returnErr error,
) {
	if err := requireNightSeatLaw(dependencies.SeatLaw); err != nil {
		return NightResult{}, err
	}
	if ctx == nil {
		return NightResult{}, errors.New("dream night requires a context")
	}
	if err := validateNightDependencies(dependencies); err != nil {
		return NightResult{}, err
	}
	repo, err := organ.Resolve(request.RepoRoot, request.RegistryBase)
	if err != nil {
		return NightResult{}, err
	}
	result.Repo = repo
	failureKind := "PREFLIGHT-FAILED"
	failurePath := repo.Organ
	durableFailure := nightFailurePath(repo.Organ)
	defer func() {
		if returnErr == nil {
			clearErr := clearNightFailure(durableFailure)
			if clearErr == nil {
				return
			}
			returnErr = clearErr
			failureKind = "CLEANUP-FAILED"
		}
		markerErr := writeNightFailure(
			durableFailure,
			failureKind,
			returnErr,
			offendingPath(returnErr, failurePath),
			dependencies.Clock(),
		)
		returnErr = errors.Join(returnErr, markerErr)
	}()
	if request.StartedAt.IsZero() {
		return result, errors.New("dream night requires a started-at timestamp")
	}
	if err := validateResourcesRoot(request.ResourcesRoot); err != nil {
		failurePath = request.ResourcesRoot
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, fmt.Errorf("dream night canceled before preflight: %w", err)
	}
	if _, err := organ.Validate(repo); err != nil {
		return result, fmt.Errorf("validate dream organ: %w", err)
	}
	resourceSet := resources.NewResources(request.ResourcesRoot, repo.Organ)
	laneName, err := lane.FromAgentTypeIn(request.AgentType, resourceSet)
	if err != nil {
		return result, err
	}
	laneContext := artifact.LaneContext{AgentType: request.AgentType, Lane: laneName}
	result.Lane = laneContext
	profile, err := lane.ResolveProfile(request.AgentType, laneName, resourceSet)
	if err != nil {
		return result, err
	}
	distillRaw, err := resourceSet.ReadFile(distillPromptFile)
	if err != nil {
		return result, err
	}
	refinerRaw, err := resourceSet.ReadFile(refinerPromptFile)
	if err != nil {
		return result, err
	}
	distillTemplate := string(distillRaw)
	refinerTemplate := string(refinerRaw)

	unlock, err := dependencies.AcquireLock(repo.Organ)
	if err != nil {
		return result, err
	}
	defer func() {
		if unlockErr := unlock(); unlockErr != nil {
			returnErr = errors.Join(returnErr, unlockErr)
		}
	}()

	stage, err := organ.NewStage(repo, laneName, request.StartedAt)
	if err != nil {
		return result, err
	}
	result.Stage = stage
	failurePath = stage.Root
	var logger *nightLogger
	completed := false
	defer func() {
		if completed || returnErr == nil {
			return
		}
		markerErr := writePrivateReplace(
			filepath.Join(stage.Root, "FAILED"),
			[]byte(failureKind+"\t"+oneLine(returnErr.Error())+"\n"),
		)
		if logger != nil {
			logErr := logger.event(nightLogEvent{
				Phase: "exit", At: dependencies.Clock(), ExitReason: "FAILED", Error: returnErr.Error(),
			})
			returnErr = errors.Join(returnErr, markerErr, logErr)
		} else {
			returnErr = errors.Join(returnErr, markerErr)
		}
	}()

	git := dependencies.Git(repo.RepoRoot)
	if git == nil {
		return result, errors.New("dream night Git factory returned nil")
	}
	recordedTree, err := git.Head(ctx)
	if err != nil {
		return result, fmt.Errorf("record repository tree: %w", err)
	}
	fingerprint, err := apply.MapFingerprint(filepath.Join(repo.Organ, "maps"))
	if err != nil {
		return result, fmt.Errorf("fingerprint organ maps: %w", err)
	}
	today := request.StartedAt.Format("2006-01-02")
	if err := writeStageMetadata(repo, laneContext, stage, recordedTree, today, fingerprint); err != nil {
		return result, err
	}
	titles, err := lane.CachedTitles(filepath.Join(repo.Organ, "maps"))
	if err != nil {
		return result, fmt.Errorf("build cached map questions: %w", err)
	}
	if err := writePrivateExclusive(
		filepath.Join(stage.Root, "cached-titles.txt"),
		[]byte(renderLines(titles)),
	); err != nil {
		return result, err
	}

	corpusResult, err := corpus.Enumerate(repo, laneContext, request.Selection, request.StartedAt)
	if err != nil {
		return result, fmt.Errorf("enumerate dream corpus: %w", err)
	}
	if err := corpus.Write(stage, corpusResult); err != nil {
		return result, fmt.Errorf("write dream corpus: %w", err)
	}
	if len(corpusResult.Paths) == 0 {
		if err := organ.RemoveEmptyStage(repo, stage.Root); err != nil {
			return result, fmt.Errorf("remove empty-window stage: %w", err)
		}
		result.Empty = true
		completed = true
		line := fmt.Sprintf(
			"dreamer-night: EMPTY-WINDOW stage=%s (no %s transcripts since %s)",
			stage.Root, request.AgentType, corpusResult.CutoffDescription,
		)
		if _, err := fmt.Fprintln(dependencies.Stdout, line); err != nil {
			return result, fmt.Errorf("write empty-window result: %w", err)
		}
		return result, nil
	}

	if err := organ.CreateLogs(repo, stage.Root); err != nil {
		return result, fmt.Errorf("create night logs: %w", err)
	}
	if err := writePrivateExclusive(
		filepath.Join(stage.Meta, "human-log.txt"),
		[]byte(stage.HumanLog+"\n"),
	); err != nil {
		return result, err
	}
	if err := writePrivateExclusive(
		filepath.Join(stage.Meta, "structured-log.txt"),
		[]byte(stage.StructuredLog+"\n"),
	); err != nil {
		return result, err
	}
	logger = &nightLogger{
		humanPath: stage.HumanLog, structuredPath: stage.StructuredLog,
		stdout: dependencies.Stdout, stderr: dependencies.Stderr, clock: dependencies.Clock,
	}
	if err := logger.event(nightLogEvent{
		Phase: "start", At: request.StartedAt, Repo: repo.RepoRoot, Lane: laneName,
		Stage: stage.Root, RecordedTree: recordedTree,
	}); err != nil {
		return result, err
	}
	if err := logger.event(nightLogEvent{
		Phase: "corpus", At: dependencies.Clock(), Paths: len(corpusResult.Paths),
		Gaps: len(corpusResult.Gaps),
	}); err != nil {
		return result, err
	}

	pinned, err := runPinGate(stage, "gate-pin.log", logger, "preflight", nil)
	if err != nil {
		return result, err
	}
	distillBrief := buildDistillBrief(
		distillTemplate, profile, repo, laneContext, stage, today, recordedTree,
		titles, corpusResult,
	)
	if err := writePrivateExclusive(filepath.Join(stage.Root, "distill-brief.md"), []byte(distillBrief)); err != nil {
		return result, err
	}
	if err := logger.human(fmt.Sprintf(
		"dreamer-night: PREFLIGHT stage=%s paths=%d gaps=%d",
		stage.Root, len(corpusResult.Paths), len(corpusResult.Gaps),
	)); err != nil {
		return result, err
	}

	seatRunner, err := dependencies.NewSeatRunner(logger)
	if err != nil {
		return result, fmt.Errorf("create seat runner: %w", err)
	}
	if seatRunner == nil {
		return result, errors.New("seat runner factory returned nil")
	}
	prepared, err := seatRunner.PrepareNight(ctx, dependencies.SeatLaw, stage.Root)
	if err != nil {
		return result, fmt.Errorf("prepare verified seats: %w", err)
	}
	if prepared == nil {
		return result, errors.New("seat runner returned a nil prepared night")
	}
	token := stageToken(stage.Root)
	failureKind = "DISTILL-FAILED"
	distill, err := prepared.RunDistill(ctx, seat.SeatInput{
		Name:   "dreamer-" + laneName + "-distill-" + token,
		Socket: "dream-" + token + "-d",
		Prompt: distillBrief,
	})
	if err != nil {
		if distill.ExitReason != "" {
			failureKind += "-" + strings.ToUpper(distill.ExitReason)
		}
		return result, fmt.Errorf("distill seat failed once; artifacts preserved at %s: %w", stage.Root, err)
	}
	failureKind = "POST-DISTILL-GATES-FAILED"
	if err := persistSeatResult(stage, "distill", distill); err != nil {
		return result, err
	}
	if err := secureStage(stage.Root); err != nil {
		return result, err
	}
	if _, err := runPinGate(stage, "gate-pin-post-distill.log", logger, "distill", &pinned); err != nil {
		return result, err
	}
	coverage, err := runCoverageGate(stage, pinned, logger)
	if err != nil {
		return result, err
	}
	_ = coverage
	distillAnchors, maps, err := runAnchorGate(
		stage,
		recordedTree,
		git,
		"anchor-results.tsv",
		"anchor-survivors.txt",
		"gate-anchors.log",
		logger,
		"distill",
	)
	if err != nil {
		return result, err
	}
	refinerBrief := buildRefinerBrief(
		refinerTemplate,
		profile,
		repo,
		laneContext,
		stage,
		recordedTree,
		titles,
		distillAnchors.Accepted,
	)
	if err := writePrivateExclusive(filepath.Join(stage.Root, "refiner-brief.md"), []byte(refinerBrief)); err != nil {
		return result, err
	}

	if len(distillAnchors.Accepted) > 0 {
		failureKind = "REFINER-FAILED"
		refiner, err := prepared.RunRefiner(ctx, seat.SeatInput{
			Name:   "dreamer-" + laneName + "-refiner-" + token,
			Socket: "dream-" + token + "-r",
			Prompt: refinerBrief,
		})
		if err != nil {
			if refiner.ExitReason != "" {
				failureKind += "-" + strings.ToUpper(refiner.ExitReason)
			}
			return result, fmt.Errorf("verify seat failed once; artifacts preserved at %s: %w", stage.Root, err)
		}
		failureKind = "POST-REFINER-GATES-FAILED"
		if err := persistSeatResult(stage, refinerSeat, refiner); err != nil {
			return result, err
		}
		if err := secureStage(stage.Root); err != nil {
			return result, err
		}
	} else {
		if err := writePrivateReplace(stage.Verdicts, nil); err != nil {
			return result, err
		}
		if err := writePrivateReplace(
			filepath.Join(stage.Root, "refiner-seat.log"),
			[]byte("VERIFY SKIP zero anchor-valid staged maps\n"),
		); err != nil {
			return result, err
		}
		if err := logger.event(nightLogEvent{
			Phase: "seat.skip", At: dependencies.Clock(), Seat: refinerSeat,
			ExitReason: "zero anchor-valid staged maps",
		}); err != nil {
			return result, err
		}
	}

	if _, err := runPinGate(stage, "gate-pin-post-refine.log", logger, refinerSeat, &pinned); err != nil {
		return result, err
	}
	normalized, err := runVerdictGate(stage, distillAnchors.Accepted, logger)
	if err != nil {
		return result, err
	}
	postAnchors, _, err := runAnchorGate(
		stage,
		recordedTree,
		git,
		"anchor-postrefine.tsv",
		"anchor-postrefine-survivors.txt",
		"gate-anchors-postrefine.log",
		logger,
		refinerSeat,
	)
	if err != nil {
		return result, err
	}
	// The second scan must cover the same staged pool, including maps rejected
	// before refinement; otherwise deletion by a seat could render as success.
	postRefineMaps, err := readMapInputs(stage.Maps)
	if err != nil {
		return result, err
	}
	if !sameMapNames(maps, postRefineMaps) {
		return result, errors.New("staged map set changed during refinement")
	}
	hold, yield, err := apply.DeriveHold(postAnchors.Accepted, normalized)
	if err != nil {
		return result, err
	}
	readyAt := dependencies.Clock()
	failureKind = "HOLD-FAILED"
	if readyAt.IsZero() {
		return result, errors.New("dream clock returned zero time at HOLD")
	}
	if err := writePrivateReplace(
		filepath.Join(stage.Meta, "apply-yield.txt"),
		[]byte(fmt.Sprintf("%d\n", yield)),
	); err != nil {
		return result, err
	}
	if err := writePrivateReplace(
		filepath.Join(stage.Root, "READY-FOR-APPLY"),
		[]byte(fmt.Sprintf("%s\t%s\n", hold, readyAt.Format(time.RFC3339))),
	); err != nil {
		return result, err
	}
	if err := logger.event(nightLogEvent{
		Phase: "hold", At: readyAt, State: hold, Survivors: len(postAnchors.Accepted), Yield: yield,
	}); err != nil {
		return result, err
	}
	holdLine := fmt.Sprintf("dreamer-night: HOLD-BEFORE-APPLY stage=%s", stage.Root)
	if hold == artifact.HoldZeroSurvivors {
		holdLine = fmt.Sprintf("dreamer-night: HOLD-BEFORE-APPLY ZERO-SURVIVORS stage=%s", stage.Root)
	}
	if err := logger.human(holdLine); err != nil {
		return result, err
	}
	if hold == artifact.HoldZeroSurvivors {
		if err := logger.human("dreamer-night: no signed apply command: zero anchor-valid staged maps"); err != nil {
			return result, err
		}
	}
	if err := logger.event(
		nightLogEvent{Phase: "exit", At: dependencies.Clock(), ExitReason: string(hold)},
	); err != nil {
		return result, err
	}

	result.HoldState = hold
	result.ApplyEligible = hold != artifact.HoldZeroSurvivors
	result.Applied = false
	result.Survivors = len(postAnchors.Accepted)
	result.Yield = yield
	completed = true
	return result, nil
}

func requireNightSeatLaw(law seat.SeatLaw) error {
	required := seat.RequiredSeatLaw()
	for _, row := range []struct {
		name string
		got  seat.SeatPolicy
		want seat.SeatPolicy
	}{
		{name: "distill", got: law.Distill, want: required.Distill},
		{name: refinerSeat, got: law.Refiner, want: required.Refiner},
	} {
		if row.got != row.want {
			return fmt.Errorf(
				"%s seat violates the luna law: require model %q effort %q, got model %q effort %q",
				row.name, row.want.Model, row.want.Effort, row.got.Model, row.got.Effort,
			)
		}
	}
	return nil
}

func validateNightDependencies(dependencies NightDependencies) error {
	switch {
	case dependencies.NewSeatRunner == nil:
		return errors.New("dream night requires a seat-runner factory")
	case dependencies.Git == nil:
		return errors.New("dream night requires a Git-reader factory")
	case dependencies.Clock == nil:
		return errors.New("dream night requires a clock")
	case dependencies.AcquireLock == nil:
		return errors.New("dream night requires a runner lock")
	case dependencies.Stdout == nil:
		return errors.New("dream night requires stdout")
	case dependencies.Stderr == nil:
		return errors.New("dream night requires stderr")
	default:
		return nil
	}
}

type commandNightGit struct {
	repo string
}

func (reader commandNightGit) Head(ctx context.Context) (string, error) {
	command := exec.CommandContext(ctx, deps.Executable("git"), "-C", reader.repo, "rev-parse", "--verify", "HEAD")
	command.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w: %s", err, strings.TrimSpace(string(output)))
	}
	value := strings.TrimSpace(string(output))
	if (len(value) != 40 && len(value) != 64) || strings.ToLower(value) != value {
		return "", fmt.Errorf("git rev-parse HEAD returned invalid object id %q", value)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", fmt.Errorf("git rev-parse HEAD returned invalid object id %q", value)
	}
	return value, nil
}

func (reader commandNightGit) Resolve(tree, path string) (gate.GitObject, bool, error) {
	return (gate.CommandGitReader{Repo: reader.repo}).Resolve(tree, path)
}

type nightLogEvent struct {
	Phase        string             `json:"phase"`
	At           time.Time          `json:"at"`
	Repo         string             `json:"repo,omitempty"`
	Lane         string             `json:"lane,omitempty"`
	Stage        string             `json:"stage,omitempty"`
	Seat         string             `json:"seat,omitempty"`
	Gate         string             `json:"gate,omitempty"`
	Verdict      string             `json:"verdict,omitempty"`
	PhaseAfter   string             `json:"phase_after,omitempty"`
	ExitReason   string             `json:"exit_reason,omitempty"`
	Error        string             `json:"error,omitempty"`
	RecordedTree string             `json:"recorded_tree,omitempty"`
	Paths        int                `json:"paths,omitempty"`
	Gaps         int                `json:"gaps,omitempty"`
	Survivors    int                `json:"survivors,omitempty"`
	Yield        int                `json:"yield,omitempty"`
	State        artifact.HoldState `json:"state,omitempty"`
}

type nightLogger struct {
	mu             sync.Mutex
	humanPath      string
	structuredPath string
	stdout         io.Writer
	stderr         io.Writer
	clock          func() time.Time
}

func (logger *nightLogger) Record(event seat.Event) error {
	return logger.appendJSON(event)
}

func (logger *nightLogger) event(event nightLogEvent) error {
	if event.At.IsZero() {
		return fmt.Errorf("structured event %s has zero timestamp", event.Phase)
	}
	return logger.appendJSON(event)
}

func (logger *nightLogger) appendJSON(value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode structured night event: %w", err)
	}
	raw = append(raw, '\n')
	logger.mu.Lock()
	defer logger.mu.Unlock()
	if err := appendPrivate(logger.structuredPath, raw); err != nil {
		return fmt.Errorf("append structured night log: %w", err)
	}
	return nil
}

func (logger *nightLogger) human(line string) error {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	if _, err := fmt.Fprintln(logger.stdout, line); err != nil {
		return fmt.Errorf("write night stdout: %w", err)
	}
	if err := appendPrivate(logger.humanPath, []byte(line+"\n")); err != nil {
		return fmt.Errorf("append human night log: %w", err)
	}
	return nil
}

func (logger *nightLogger) now() time.Time {
	if logger.clock == nil {
		return time.Time{}
	}
	return logger.clock()
}

func appendPrivate(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func writeStageMetadata(
	repo artifact.RepoContext,
	laneContext artifact.LaneContext,
	stage artifact.StageLayout,
	recordedTree, today, fingerprint string,
) error {
	rows := map[string]string{
		"repo-root.txt":  repo.RepoRoot,
		"organ.txt":      repo.Organ,
		"agent-type.txt": laneContext.AgentType,
		"lane.txt":       laneContext.Lane,
		"repo-head.txt":  recordedTree,
		"run-date.txt":   today,
		"maps.sha256":    fingerprint,
	}
	for _, name := range []string{"repo-root.txt", "organ.txt", "agent-type.txt", "lane.txt", "repo-head.txt", "run-date.txt", "maps.sha256"} {
		if err := writePrivateExclusive(filepath.Join(stage.Meta, name), []byte(rows[name]+"\n")); err != nil {
			return fmt.Errorf("write stage metadata %s: %w", name, err)
		}
	}
	return nil
}

func writePrivateExclusive(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return artifact.ErrorAt(path, fmt.Errorf("create private artifact %s: %w", path, err))
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return artifact.ErrorAt(path, fmt.Errorf("write private artifact %s: %w", path, err))
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return artifact.ErrorAt(path, fmt.Errorf("close private artifact %s: %w", path, err))
	}
	return nil
}

func writePrivateReplace(path string, raw []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".dream-night-*")
	if err != nil {
		return artifact.ErrorAt(path, fmt.Errorf("create private replacement for %s: %w", path, err))
	}
	temporaryPath := temporary.Name()
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return artifact.ErrorAt(path, err)
	}
	if _, err := temporary.Write(raw); err != nil {
		_ = temporary.Close()
		return artifact.ErrorAt(path, err)
	}
	if err := temporary.Close(); err != nil {
		return artifact.ErrorAt(path, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return artifact.ErrorAt(path, fmt.Errorf("replace private artifact %s: %w", path, err))
	}
	keep = true
	return nil
}

func readRegular(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, artifact.ErrorAt(path, fmt.Errorf("inspect artifact %s: %w", path, err))
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, artifact.ErrorAt(path, fmt.Errorf("artifact is not a regular non-symlink file: %s", path))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, artifact.ErrorAt(path, fmt.Errorf("read artifact %s: %w", path, err))
	}
	return raw, nil
}

func runPinGate(
	stage artifact.StageLayout,
	logName string,
	logger *nightLogger,
	phaseAfter string,
	expected *gate.PinnedPaths,
) (gate.PinnedPaths, error) {
	pathsRaw, err := readRegular(stage.Paths)
	if err != nil {
		return gate.PinnedPaths{}, err
	}
	pinRaw, err := readRegular(stage.Pin)
	if err != nil {
		return gate.PinnedPaths{}, err
	}
	pinned, err := gate.Pin(pathsRaw, pinRaw)
	if err != nil {
		return gate.PinnedPaths{}, fmt.Errorf("PIN gate after %s: %w", phaseAfter, err)
	}
	if expected != nil && (pinned.Digest != expected.Digest || !bytesEqual(pinned.Raw, expected.Raw)) {
		return gate.PinnedPaths{}, fmt.Errorf(
			"PIN gate after %s: corpus changed from preflight digest %s to %s",
			phaseAfter, expected.Digest, pinned.Digest,
		)
	}
	line := fmt.Sprintf("PIN PASS %s %s", pinned.Digest, stage.Paths)
	if err := writePrivateReplace(filepath.Join(stage.Root, logName), []byte(line+"\n")); err != nil {
		return gate.PinnedPaths{}, err
	}
	if phaseAfter != "preflight" {
		if err := logger.human(line); err != nil {
			return gate.PinnedPaths{}, err
		}
	}
	if err := logger.event(
		nightLogEvent{
			Phase:      gatePhase,
			At:         logger.now(),
			Gate:       "PIN",
			Verdict:    gateVerdictPass,
			PhaseAfter: phaseAfter,
		},
	); err != nil {
		return gate.PinnedPaths{}, err
	}
	return pinned, nil
}

func bytesEqual(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func runCoverageGate(
	stage artifact.StageLayout,
	pinned gate.PinnedPaths,
	logger *nightLogger,
) (gate.CoverageResult, error) {
	raw, err := readRegular(stage.Coverage)
	if err != nil {
		return gate.CoverageResult{}, err
	}
	parsed, err := artifact.ParseCoverage(string(raw), len(pinned.Paths))
	if err != nil {
		return gate.CoverageResult{}, fmt.Errorf("COVERAGE gate: %w", err)
	}
	result, err := gate.Coverage(pinned, parsed)
	if err != nil {
		return gate.CoverageResult{}, fmt.Errorf("COVERAGE gate: %w", err)
	}
	if err := writePrivateReplace(
		stage.Coverage+".expanded",
		[]byte(artifact.RenderExpandedCoverage(parsed, pinned.Paths)),
	); err != nil {
		return gate.CoverageResult{}, err
	}
	line := fmt.Sprintf("COVERAGE PASS %d paths", len(pinned.Paths))
	if err := writePrivateReplace(filepath.Join(stage.Root, "gate-coverage.log"), []byte(line+"\n")); err != nil {
		return gate.CoverageResult{}, err
	}
	if err := logger.human(line); err != nil {
		return gate.CoverageResult{}, err
	}
	if err := logger.event(
		nightLogEvent{Phase: gatePhase, At: logger.now(), Gate: "COVERAGE+CONDUCT", Verdict: gateVerdictPass},
	); err != nil {
		return gate.CoverageResult{}, err
	}
	return result, nil
}

func runAnchorGate(
	stage artifact.StageLayout,
	recordedTree string,
	git gate.GitObjectReader,
	resultsName, survivorsName, gateLogName string,
	logger *nightLogger,
	phaseAfter string,
) (gate.AnchorResult, []gate.MapInput, error) {
	maps, err := readMapInputs(stage.Maps)
	if err != nil {
		return gate.AnchorResult{}, nil, err
	}
	result, err := gate.Anchors(recordedTree, maps, git)
	if err != nil {
		return gate.AnchorResult{}, nil, fmt.Errorf("ANCHORS gate after %s: %w", phaseAfter, err)
	}
	if err := writePrivateReplace(
		filepath.Join(stage.Root, resultsName),
		[]byte(renderAnchorResults(maps, result)),
	); err != nil {
		return gate.AnchorResult{}, nil, err
	}
	if err := writePrivateReplace(
		filepath.Join(stage.Root, survivorsName),
		[]byte(renderLines(result.Accepted)),
	); err != nil {
		return gate.AnchorResult{}, nil, err
	}
	line := fmt.Sprintf("ANCHORS PASS accepted=%d rejected=%d", len(result.Accepted), len(result.Rejected))
	if err := writePrivateReplace(filepath.Join(stage.Root, gateLogName), []byte(line+"\n")); err != nil {
		return gate.AnchorResult{}, nil, err
	}
	if err := logger.human(line); err != nil {
		return gate.AnchorResult{}, nil, err
	}
	if err := logger.event(
		nightLogEvent{
			Phase:      gatePhase,
			At:         logger.now(),
			Gate:       "ANCHORS",
			Verdict:    gateVerdictPass,
			PhaseAfter: phaseAfter,
		},
	); err != nil {
		return gate.AnchorResult{}, nil, err
	}
	return result, maps, nil
}

func runVerdictGate(
	stage artifact.StageLayout,
	survivors []string,
	logger *nightLogger,
) ([]artifact.NormalizedVerdict, error) {
	raw, err := readRegular(stage.Verdicts)
	if err != nil {
		return nil, err
	}
	verdicts, err := artifact.ParseVerdicts(string(raw))
	if err != nil {
		return nil, fmt.Errorf("VERDICTS gate: %w", err)
	}
	result, err := gate.Verdicts(survivors, verdicts)
	if err != nil {
		return nil, fmt.Errorf("VERDICTS gate: %w", err)
	}
	if err := writePrivateReplace(
		stage.NormalizedVerdicts,
		[]byte(artifact.RenderNormalizedVerdicts(result.Normalized)),
	); err != nil {
		return nil, err
	}
	ruled := 0
	for _, row := range result.Normalized {
		if row.Kind != artifact.NormalizedUnruled {
			ruled++
		}
	}
	line := fmt.Sprintf("VERDICTS PASS ruled=%d unruled=%d", ruled, len(result.Normalized)-ruled)
	if err := writePrivateReplace(filepath.Join(stage.Root, "gate-verdicts.log"), []byte(line+"\n")); err != nil {
		return nil, err
	}
	if err := logger.human(line); err != nil {
		return nil, err
	}
	if err := logger.event(
		nightLogEvent{Phase: gatePhase, At: logger.now(), Gate: "VERDICTS", Verdict: gateVerdictPass},
	); err != nil {
		return nil, err
	}
	return result.Normalized, nil
}

func readMapInputs(directory string) ([]gate.MapInput, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, fmt.Errorf("read staged maps %s: %w", directory, err)
	}
	result := make([]gate.MapInput, 0, len(entries))
	for _, entry := range entries {
		path := filepath.Join(directory, entry.Name())
		raw, err := readRegular(path)
		if err != nil {
			return nil, err
		}
		result = append(result, gate.MapInput{Name: entry.Name(), Text: string(raw)})
	}
	sort.Slice(result, func(left, right int) bool { return result[left].Name < result[right].Name })
	return result, nil
}

func sameMapNames(left, right []gate.MapInput) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Name != right[index].Name {
			return false
		}
	}
	return true
}

func renderAnchorResults(inputs []gate.MapInput, result gate.AnchorResult) string {
	accepted := make(map[string]struct{}, len(result.Accepted))
	for _, path := range result.Accepted {
		accepted[path] = struct{}{}
	}
	rejected := make(map[string]string, len(result.Rejected))
	for _, row := range result.Rejected {
		rejected[row.MapPath] = row.Reason
	}
	var rendered strings.Builder
	for _, input := range inputs {
		path := "maps/" + input.Name
		if _, ok := accepted[path]; ok {
			fmt.Fprintf(&rendered, "ACCEPT\t%s\tcanonical map and recorded-tree anchors\n", path)
		} else {
			fmt.Fprintf(&rendered, "REJECT\t%s\t%s\n", path, rejected[path])
		}
	}
	return rendered.String()
}

func buildDistillBrief(
	template string,
	profile artifact.LaneProfile,
	repo artifact.RepoContext,
	laneContext artifact.LaneContext,
	stage artifact.StageLayout,
	today, recordedTree string,
	titles []string,
	corpusResult corpus.Result,
) string {
	var out strings.Builder
	out.WriteString(template)
	out.WriteString("\n## Lane\n\n")
	out.WriteString(profile.Body)
	if !strings.HasSuffix(profile.Body, "\n") {
		out.WriteByte('\n')
	}
	out.WriteString("\n## Run context\n\n")
	fmt.Fprintf(&out, "Agent type: `%s` (lane `%s`)\n", laneContext.AgentType, laneContext.Lane)
	fmt.Fprintf(&out, "Repository root: `%s`\n", repo.RepoRoot)
	fmt.Fprintf(&out, "Repository tree: `%s`\n", recordedTree)
	fmt.Fprintf(&out, "Staging root: `%s`\n", stage.Root)
	fmt.Fprintf(&out, "Map output directory: `%s`\n", stage.Maps)
	fmt.Fprintf(&out, "Coverage output: `%s`\n", stage.Coverage)
	fmt.Fprintf(&out, "Run date: `%s`\n", today)
	out.WriteString("\n### Cached map titles\n\n")
	writeBulletList(&out, titles)
	if corpusResult.Window.Mode == corpus.WindowExplicitCorpus {
		comments := corpusComments(corpusResult.CorpusFileBytes)
		if len(comments) > 0 {
			out.WriteString("\n### Corpus provenance\n\n")
			for _, comment := range comments {
				out.WriteString(comment)
				out.WriteByte('\n')
			}
		}
	}
	out.WriteString("\n### Transcript paths (coverage indices)\n\n")
	for index, path := range corpusResult.Paths {
		fmt.Fprintf(&out, "%d. %s\n", index+1, path)
	}
	fmt.Fprintf(
		&out,
		"\nWrite only `%s/*.md` and `%s`; finish coverage with `END-OF-RUN`.\n",
		stage.Maps,
		stage.Coverage,
	)
	return out.String()
}

func buildRefinerBrief(
	template string,
	profile artifact.LaneProfile,
	repo artifact.RepoContext,
	laneContext artifact.LaneContext,
	stage artifact.StageLayout,
	recordedTree string,
	titles, survivors []string,
) string {
	var out strings.Builder
	out.WriteString(template)
	out.WriteString("\n## Lane\n\n")
	out.WriteString(profile.Body)
	if !strings.HasSuffix(profile.Body, "\n") {
		out.WriteByte('\n')
	}
	out.WriteString("\n## Run context\n\n")
	fmt.Fprintf(&out, "Agent type: `%s` (lane `%s`)\n", laneContext.AgentType, laneContext.Lane)
	fmt.Fprintf(&out, "Repository root: `%s`\n", repo.RepoRoot)
	fmt.Fprintf(&out, "Repository tree: `%s`\n", recordedTree)
	fmt.Fprintf(&out, "Staging root: `%s`\n", stage.Root)
	fmt.Fprintf(&out, "Verdict output: `%s`\n", stage.Verdicts)
	out.WriteString("\n### Existing map titles\n\n")
	writeBulletList(&out, titles)
	out.WriteString("\n### Staged maps to rule\n\n")
	if len(survivors) == 0 {
		out.WriteString("(none)\n")
	} else {
		for _, path := range survivors {
			fmt.Fprintf(&out, "- %s\n", filepath.Join(stage.Root, path))
		}
	}
	fmt.Fprintf(
		&out,
		"\nWrite only `%s` and AMEND edits to the listed staged maps. Rule every listed map or leave it mechanically UNRULED.\n",
		stage.Verdicts,
	)
	return out.String()
}

func writeBulletList(out *strings.Builder, rows []string) {
	if len(rows) == 0 {
		out.WriteString("(none)\n")
		return
	}
	for _, row := range rows {
		fmt.Fprintf(out, "- %s\n", row)
	}
}

func corpusComments(raw []byte) []string {
	var result []string
	for _, row := range strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(row, "#") {
			continue
		}
		row = strings.TrimPrefix(row, "#")
		row = strings.TrimPrefix(row, " ")
		result = append(result, row)
	}
	return result
}

func persistSeatResult(stage artifact.StageLayout, role string, result seat.SeatResult) error {
	prefix := role
	lastName := role + "-last-message.txt"
	if role == refinerSeat {
		lastName = "verify-last-message.txt"
	}
	summary := fmt.Sprintf(
		"seat=%s\texit=%s\tduration=%s\tsession=%s\trollout=%s\n",
		role, result.ExitReason, result.Duration, result.SessionID, result.RolloutPath,
	)
	if err := writePrivateReplace(filepath.Join(stage.Root, prefix+"-seat.log"), []byte(summary)); err != nil {
		return err
	}
	return writePrivateReplace(filepath.Join(stage.Root, lastName), []byte(result.LastAssistant+"\n"))
}

func secureStage(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk stage at %s: %w", path, walkErr)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect staged artifact %s: %w", path, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("staged artifact is a symlink: %s", path)
		}
		if entry.IsDir() {
			if err := os.Chmod(path, 0o700); err != nil {
				return fmt.Errorf("secure staged directory %s: %w", path, err)
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("staged artifact is not regular: %s", path)
		}
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("secure staged file %s: %w", path, err)
		}
		return nil
	})
}

func renderLines(rows []string) string {
	if len(rows) == 0 {
		return ""
	}
	return strings.Join(rows, "\n") + "\n"
}

func stageToken(stage string) string {
	sum := sha256.Sum256([]byte(stage))
	return hex.EncodeToString(sum[:6])
}

func oneLine(value string) string {
	return strings.Map(func(character rune) rune {
		if character == '\n' || character == '\r' || character == '\t' {
			return ' '
		}
		return character
	}, value)
}
