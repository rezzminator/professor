package seat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"hostops/pfm/internal/action"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/headless"
	"hostops/pfm/internal/spawn"
	"hostops/pfm/internal/transcript"
)

const (
	// PerSeatTimeout is a law, not a caller tuning knob. Tests shorten the
	// private Runner field while this exported constant pins the live bound.
	PerSeatTimeout = 2700 * time.Second

	// A submitted TUI prompt writes task_started promptly. Waiting longer does
	// not add confidence; it only recreates the old failure where an untouched
	// composer entered the 2700-second work monitor.
	defaultPromptProofTimeout = 15 * time.Second
)

// SeatPolicy makes both model choices explicit at the call boundary.
type SeatPolicy struct {
	Model  string
	Effort string
}

// SeatLaw is checked before filesystem validation, command enumeration,
// logging, or any other effect.
type SeatLaw struct {
	Distill SeatPolicy
	Refiner SeatPolicy
}

func RequiredSeatLaw() SeatLaw {
	required := SeatPolicy{Model: SeatModel, Effort: SeatEffort}
	return SeatLaw{Distill: required, Refiner: required}
}

func requireSeatLaw(law SeatLaw) error {
	for _, seat := range []struct {
		name   string
		policy SeatPolicy
	}{
		{name: "distill", policy: law.Distill},
		{name: "refiner", policy: law.Refiner},
	} {
		if seat.policy.Model != SeatModel || seat.policy.Effort != SeatEffort {
			return fmt.Errorf(
				"%s seat violates the luna law: require model %q effort %q, got model %q effort %q",
				seat.name,
				SeatModel,
				SeatEffort,
				seat.policy.Model,
				seat.policy.Effort,
			)
		}
	}
	return nil
}

// SeatInput is one and only one attempt. Names and sockets are supplied by
// the night so the human log can correlate the detached pane.
type SeatInput struct {
	Name   string
	Socket string
	Prompt string
}

// SeatResult contains the audit trail and the uncondensed final assistant
// message read from the rollout, never from pane pixels.
type SeatResult struct {
	Name          string
	Duration      time.Duration
	ExitReason    string
	LastAssistant string
	SessionID     string
	RolloutPath   string
}

// Event is one structured JSONL-ready fact. EventSink owns persistence; seat
// refuses to start if a required audit event cannot be written.
type Event struct {
	Phase        string                   `json:"phase"`
	Seat         string                   `json:"seat,omitempty"`
	At           time.Time                `json:"at"`
	Duration     time.Duration            `json:"duration,omitempty"`
	ExitReason   string                   `json:"exit_reason,omitempty"`
	Error        string                   `json:"error,omitempty"`
	Config       *PinnedConfig            `json:"config,omitempty"`
	Verification *Verification            `json:"verification,omitempty"`
	ProcessTree  *ProcessTreeVerification `json:"process_tree,omitempty"`
}

type EventSink interface {
	Record(Event) error
}

type EventSinkFunc func(Event) error

func (function EventSinkFunc) Record(event Event) error {
	return function(event)
}

type Dependencies struct {
	Commands    CommandRunner
	Host        Host
	Processes   ProcessTree
	Jailer      ProcessJailer
	Rollouts    RolloutLocator
	Events      EventSink
	Now         func() time.Time
	CodexBinary string
}

type Runner struct {
	dependencies       Dependencies
	seatTimeout        time.Duration
	promptProofTimeout time.Duration
	poll               time.Duration
	spawnTimings       spawn.Timings
	spawnTrace         io.Writer
	codexBinary        string
}

// PreparedNight is one verified seat configuration shared by the distill and
// refiner attempts. It deliberately exposes separate methods so the Dream
// engine can run PIN/COVERAGE/ANCHORS and build the refiner brief between the
// two thinking seats. Its state machine enforces distill-before-refiner and
// consumes each attempt before executing it, so even a failure cannot retry.
type PreparedNight struct {
	runner       *Runner
	stage        string
	config       PinnedConfig
	verification Verification

	mu               sync.Mutex
	distillAttempted bool
	distillComplete  bool
	refinerAttempted bool
}

func NewRunner(dependencies Dependencies) *Runner {
	return &Runner{
		dependencies:       dependencies,
		seatTimeout:        PerSeatTimeout,
		promptProofTimeout: defaultPromptProofTimeout,
		poll:               time.Second,
		codexBinary:        dependencies.CodexBinary,
	}
}

// PrepareNight checks the luna law before every effect, validates the stage,
// derives the MCP roster, and verifies the exact pin set once for the night.
// Seat inputs carry no model or effort: only this verified handle can launch
// an attempt, and both attempts therefore share one immutable pin set.
func (runner *Runner) PrepareNight(
	ctx context.Context,
	law SeatLaw,
	stage string,
) (*PreparedNight, error) {
	if err := requireSeatLaw(law); err != nil {
		return nil, err
	}
	if runner == nil {
		return nil, errors.New("seat runner is nil")
	}
	if err := runner.validateDependencies(); err != nil {
		return nil, err
	}
	validatedStage, err := validateStage(stage)
	if err != nil {
		return nil, err
	}
	projectRoot, err := projectRootForStage(validatedStage)
	if err != nil {
		return nil, err
	}
	config, verification, configErr := runner.prepareConfig(ctx, projectRoot)
	if configErr != nil {
		return nil, configErr
	}
	return &PreparedNight{
		runner:       runner,
		stage:        validatedStage,
		config:       clonePinnedConfig(config),
		verification: cloneVerification(verification),
	}, nil
}

func (night *PreparedNight) Config() PinnedConfig {
	return clonePinnedConfig(night.config)
}

func (night *PreparedNight) Verification() Verification {
	return cloneVerification(night.verification)
}

// RunDistill consumes the night's sole distill attempt before launching it.
func (night *PreparedNight) RunDistill(
	ctx context.Context,
	input SeatInput,
) (SeatResult, error) {
	if night == nil || night.runner == nil {
		return SeatResult{}, errors.New("prepared night is nil")
	}
	if err := validateSeatInput("distill", input); err != nil {
		return SeatResult{}, err
	}
	night.mu.Lock()
	if night.distillAttempted {
		night.mu.Unlock()
		return SeatResult{}, errors.New("distill seat attempt is already consumed; retries are forbidden")
	}
	night.distillAttempted = true
	night.mu.Unlock()

	result, err := night.runner.runSeat(ctx, night.stage, "distill", input, night.config)
	if err != nil {
		return result, err
	}
	night.mu.Lock()
	night.distillComplete = true
	night.mu.Unlock()
	return result, nil
}

// RunRefiner requires a completed distill and consumes the sole refiner
// attempt before launching. Deterministic gates belong between these calls.
func (night *PreparedNight) RunRefiner(
	ctx context.Context,
	input SeatInput,
) (SeatResult, error) {
	if night == nil || night.runner == nil {
		return SeatResult{}, errors.New("prepared night is nil")
	}
	if err := validateSeatInput("refiner", input); err != nil {
		return SeatResult{}, err
	}
	night.mu.Lock()
	switch {
	case !night.distillComplete:
		night.mu.Unlock()
		return SeatResult{}, errors.New("refiner cannot run before a successful distill seat")
	case night.refinerAttempted:
		night.mu.Unlock()
		return SeatResult{}, errors.New("refiner seat attempt is already consumed; retries are forbidden")
	}
	night.refinerAttempted = true
	night.mu.Unlock()
	return night.runner.runSeat(ctx, night.stage, "refiner", input, night.config)
}

func (runner *Runner) prepareConfig(
	ctx context.Context,
	projectRoot string,
) (PinnedConfig, Verification, error) {
	config, verification, configErr := DiscoverAndVerifyConfig(ctx, runner.dependencies.Commands, projectRoot)
	now := runner.now()
	if len(config.Overrides) == 0 {
		if configErr != nil {
			eventErr := runner.dependencies.Events.Record(Event{
				Phase: "seat.config.discovery-failed",
				At:    now,
				Error: configErr.Error(),
			})
			return PinnedConfig{}, Verification{}, errors.Join(configErr, eventErr)
		}
		return PinnedConfig{}, Verification{}, errors.New("seat config discovery returned no pin set and no error")
	}
	configCopy := clonePinnedConfig(config)
	if eventErr := runner.dependencies.Events.Record(Event{
		Phase:  "seat.config.pinned",
		At:     now,
		Config: &configCopy,
	}); eventErr != nil {
		return config, verification, errors.Join(
			configErr,
			fmt.Errorf("record pinned seat configuration: %w", eventErr),
		)
	}
	verificationCopy := cloneVerification(verification)
	verificationEvent := Event{
		Phase:        "seat.config.verified",
		At:           runner.now(),
		Verification: &verificationCopy,
	}
	if configErr != nil {
		verificationEvent.Error = configErr.Error()
	}
	if eventErr := runner.dependencies.Events.Record(verificationEvent); eventErr != nil {
		return config, verification, errors.Join(
			configErr,
			fmt.Errorf("record seat configuration verification: %w", eventErr),
		)
	}
	if configErr != nil {
		return config, verification, configErr
	}
	return config, verification, nil
}

// projectRootForStage mirrors Codex's repository trust boundary without a
// Git subprocess: the nearest .git directory or worktree marker is the
// project whose trust level must be pinned. A non-repository stage is its own
// project, which also prevents an interactive trust decision.
func projectRootForStage(stage string) (string, error) {
	for directory := stage; ; directory = filepath.Dir(directory) {
		_, err := os.Lstat(filepath.Join(directory, ".git"))
		switch {
		case err == nil:
			return directory, nil
		case !os.IsNotExist(err):
			return "", fmt.Errorf("inspect project marker for seat stage: %w", err)
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return stage, nil
		}
	}
}

func (runner *Runner) runSeat(
	ctx context.Context,
	stage, role string,
	input SeatInput,
	config PinnedConfig,
) (result SeatResult, returnErr error) {
	result.Name = input.Name
	started := runner.now()
	if err := runner.dependencies.Events.Record(Event{
		Phase: "seat.start",
		Seat:  role,
		At:    started,
	}); err != nil {
		return result, fmt.Errorf("record %s seat start: %w", role, err)
	}
	seatContext, cancelSeat := context.WithTimeout(ctx, runner.seatTimeout)
	defer cancelSeat()

	snapshot, err := runner.dependencies.Rollouts.Snapshot(seatContext)
	if err != nil {
		reason := "rollout-snapshot-error"
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			reason = monitorExitReason(err)
		}
		return runner.finishSeat(result, role, started, reason, err)
	}
	plan, err := action.SandboxedCodexRun(action.SandboxedCodexRequest{
		CWD:    stage,
		Config: append([]string(nil), config.Overrides...),
		Binary: runner.codexBinary,
	})
	if err != nil {
		return runner.finishSeat(result, role, started, "plan-error", err)
	}
	if err := verifySandboxedPlan(plan, stage, config); err != nil {
		return runner.finishSeat(result, role, started, "plan-error", err)
	}

	gateHost := &processGateHost{
		Host:      runner.dependencies.Host,
		processes: runner.dependencies.Processes,
		jailer:    runner.dependencies.Jailer,
		events:    runner.dependencies.Events,
		now:       runner.now,
		role:      role,
		prompt:    input.Prompt,
	}
	spawnResult, spawnErr := spawn.Run(seatContext, gateHost, spawn.Request{
		Engine:  pfmengine.Codex,
		Name:    input.Name,
		Socket:  input.Socket,
		CWD:     stage,
		Run:     plan.Run,
		Prompt:  input.Prompt,
		Width:   action.HeadlessWidth,
		Height:  action.HeadlessHeight,
		Timings: runner.spawnTimings,
		Trace:   runner.spawnTrace,
	})
	if spawnErr != nil {
		cleanupErr := runner.cleanup(input.Socket, gateHost.jail, gateHost.sessionMade)
		reason := "spawn-error"
		if gateHost.err != nil {
			reason = "process-tree-gate"
		} else if errors.Is(spawnErr, context.Canceled) || errors.Is(spawnErr, context.DeadlineExceeded) {
			reason = monitorExitReason(spawnErr)
		}
		return runner.finishSeat(
			result,
			role,
			started,
			reason,
			errors.Join(spawnErr, cleanupErr),
		)
	}
	if gateHost.err != nil {
		cleanupErr := runner.cleanup(input.Socket, gateHost.jail, gateHost.sessionMade)
		return runner.finishSeat(
			result,
			role,
			started,
			"process-tree-gate",
			errors.Join(gateHost.err, cleanupErr),
		)
	}
	// spawn.Run can return an unproven result when its context expires during
	// startup choreography. Preserve the law-level reason: an expired seat is
	// a timeout, not a generic rename/prompt failure.
	if contextErr := seatContext.Err(); contextErr != nil {
		cleanupErr := runner.cleanup(input.Socket, gateHost.jail, gateHost.sessionMade)
		return runner.finishSeat(
			result,
			role,
			started,
			monitorExitReason(contextErr),
			errors.Join(contextErr, cleanupErr),
		)
	}
	if !spawnResult.Named || !spawnResult.Prompted || len(spawnResult.Warnings) != 0 {
		cleanupErr := runner.cleanup(input.Socket, gateHost.jail, gateHost.sessionMade)
		spawnStateErr := fmt.Errorf(
			"%s seat spawn was not proven (named=%t prompted=%t warnings=%q)",
			role,
			spawnResult.Named,
			spawnResult.Prompted,
			spawnResult.Warnings,
		)
		return runner.finishSeat(
			result,
			role,
			started,
			"spawn-unproven",
			errors.Join(spawnStateErr, cleanupErr),
		)
	}

	match := RolloutMatch{
		Name:    input.Name,
		CWD:     stage,
		Socket:  spawnResult.Socket,
		Session: spawnResult.Session,
	}
	promptErr := runner.awaitPromptTaskStarted(seatContext, snapshot, match)
	promptEvent := Event{
		Phase: "seat.prompt.started",
		Seat:  role,
		At:    runner.now(),
	}
	if promptErr != nil {
		promptEvent.Error = promptErr.Error()
	}
	if eventErr := runner.dependencies.Events.Record(promptEvent); eventErr != nil {
		promptErr = errors.Join(promptErr, fmt.Errorf("record %s prompt proof: %w", role, eventErr))
	}
	if promptErr != nil {
		cleanupErr := runner.cleanup(input.Socket, gateHost.jail, gateHost.sessionMade)
		reason := "prompt-unproven"
		if contextErr := seatContext.Err(); contextErr != nil {
			reason = monitorExitReason(contextErr)
		} else if strings.Contains(promptErr.Error(), "ambiguous rollout") {
			reason = "rollout-ambiguity"
		}
		return runner.finishSeat(
			result,
			role,
			started,
			reason,
			errors.Join(promptErr, cleanupErr),
		)
	}

	chat, _, err := runner.monitor(seatContext, snapshot, match)
	exitReason := "idle"
	if err != nil {
		exitReason = monitorExitReason(err)
	}
	cleanupErr := runner.cleanup(input.Socket, gateHost.jail, gateHost.sessionMade)
	if cleanupErr != nil {
		err = errors.Join(err, cleanupErr)
		exitReason = "cleanup-error"
	}
	if err != nil {
		return runner.finishSeat(result, role, started, exitReason, err)
	}
	turnEvidence, evidenceErr := inspectRolloutTurnEvidence(seatContext, chat.Path)
	if evidenceErr != nil {
		return runner.finishSeat(result, role, started, "transcript-error", evidenceErr)
	}
	if turnEvidence.Model != SeatModel {
		return runner.finishSeat(
			result,
			role,
			started,
			"model-mismatch",
			fmt.Errorf("%s seat transcript reports model %q, require %q", role, turnEvidence.Model, SeatModel),
		)
	}
	if turnEvidence.Effort != SeatEffort {
		return runner.finishSeat(
			result,
			role,
			started,
			"effort-mismatch",
			fmt.Errorf("%s seat transcript reports effort %q, require %q", role, turnEvidence.Effort, SeatEffort),
		)
	}
	entries, _, err := transcript.Tail(seatContext, chat.Path, string(pfmengine.Codex), 1, 0)
	if err != nil {
		return runner.finishSeat(result, role, started, "transcript-error", err)
	}
	last, ok := transcript.Last(entries, transcript.RoleAssistant)
	if !ok || strings.TrimSpace(last.Text) == "" {
		return runner.finishSeat(
			result,
			role,
			started,
			"transcript-error",
			errors.New("idle seat has no final assistant message"),
		)
	}
	result.SessionID = chat.ID
	result.RolloutPath = chat.Path
	result.LastAssistant = last.Text
	return runner.finishSeat(result, role, started, "idle", nil)
}

// awaitPromptTaskStarted is the delivery boundary between spawn choreography
// and work monitoring. A visible pane cannot prove submission for a large
// multiline composer: its first line may be cropped, making both a retained
// prompt and a submitted one look fingerprint-free. Codex's rollout
// task_started event is the engine's own durable assertion that Enter landed.
func (runner *Runner) awaitPromptTaskStarted(
	ctx context.Context,
	snapshot RolloutSnapshot,
	match RolloutMatch,
) error {
	proofContext, cancel := context.WithTimeout(ctx, runner.promptProofTimeout)
	defer cancel()
	poll := runner.poll
	if poll <= 0 {
		poll = time.Second
	}
	proofFailure := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return fmt.Errorf(
			"prompt submission was not proven: Codex recorded no task_started within %s",
			runner.promptProofTimeout,
		)
	}
	// The boundary is examined before the deadline is honoured: a window
	// spent between arming and the first poll still gets one look, so the
	// verdict is always an examined one — never "unproven" without a look.
	for {
		chat, found, err := runner.dependencies.Rollouts.Locate(proofContext, snapshot, match)
		if err != nil {
			if proofContext.Err() != nil {
				return proofFailure()
			}
			return err
		}
		if found {
			evidence, err := inspectRolloutTurnEvidence(proofContext, chat.Path)
			if err != nil {
				if proofContext.Err() != nil {
					return proofFailure()
				}
				return err
			}
			if evidence.Started {
				return nil
			}
		} else if !runner.dependencies.Host.SocketAlive(proofContext, match.Socket) {
			return errors.New("seat process exited before Codex recorded task_started")
		}

		timer := time.NewTimer(poll)
		select {
		case <-proofContext.Done():
			timer.Stop()
			return proofFailure()
		case <-timer.C:
		}
	}
}

func (runner *Runner) monitor(
	ctx context.Context,
	snapshot RolloutSnapshot,
	match RolloutMatch,
) (headless.Chat, headless.Status, error) {
	poll := runner.poll
	if poll <= 0 {
		poll = time.Second
	}
	for {
		if err := ctx.Err(); err != nil {
			return headless.Chat{}, headless.Status{}, err
		}
		chat, found, err := runner.dependencies.Rollouts.Locate(ctx, snapshot, match)
		if err != nil {
			return headless.Chat{}, headless.Status{}, err
		}
		live := runner.dependencies.Host.SocketAlive(ctx, match.Socket)
		if found {
			chat.Live = live
			status, err := headless.Inspect(ctx, chat, runner.now())
			if err != nil {
				return chat, status, err
			}
			turnState, err := inspectRolloutTurn(ctx, chat.Path)
			if err != nil {
				return chat, status, err
			}
			switch turnState {
			case rolloutTurnComplete:
				return chat, status, nil
			case rolloutTurnAborted:
				return chat, status, errors.New("seat turn was aborted before completion")
			case rolloutTurnPending:
				// Continue below even when Inspect reports idle: Codex commentary
				// is an assistant transcript entry but not a completed turn.
			default:
				return chat, status, fmt.Errorf("seat reported unknown rollout turn state %q", turnState)
			}
			switch status.State {
			case headless.StateIdle, headless.StateWorking:
				// Continue below.
			case headless.StateDead, headless.StateMissing:
				return chat, status, fmt.Errorf("seat became %s before an assistant answer", status.State)
			default:
				return chat, status, fmt.Errorf("seat reported unknown state %q", status.State)
			}
		} else if !live {
			return headless.Chat{}, headless.Status{}, errors.New(
				"seat process exited before its rollout could be resolved",
			)
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return headless.Chat{}, headless.Status{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func (runner *Runner) cleanup(
	socket string,
	jail ProcessGroupJail,
	sessionMade bool,
) error {
	// The caller's context may be canceled or expired. Cleanup receives a
	// fresh, short bound so cancellation cannot strand the process group.
	cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	alive := runner.dependencies.Host.SocketAlive(cleanupContext, socket)
	var cleanupErr error
	if !jail.valid() && alive {
		pid, err := runner.dependencies.Host.PaneRootPID(cleanupContext, socket, socket)
		if err == nil {
			jail, err = runner.dependencies.Jailer.Capture(cleanupContext, pid)
		}
		if err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("capture process group during cleanup: %w", err))
		}
	}
	if alive {
		if err := runner.dependencies.Host.KillServer(cleanupContext, socket); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("kill seat server: %w", err))
		}
	}
	if jail.valid() {
		if err := runner.dependencies.Jailer.Kill(cleanupContext, jail); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("kill pane process group: %w", err))
		}
	} else if sessionMade {
		cleanupErr = errors.Join(cleanupErr, errors.New("spawned seat has no proven process-group jail"))
	}
	if cleanupErr != nil {
		return fmt.Errorf("clean seat %q pane and process group: %w", socket, cleanupErr)
	}
	return nil
}

func (runner *Runner) finishSeat(
	result SeatResult,
	role string,
	started time.Time,
	exitReason string,
	seatErr error,
) (SeatResult, error) {
	result.ExitReason = exitReason
	result.Duration = runner.now().Sub(started)
	event := Event{
		Phase:      "seat.finish",
		Seat:       role,
		At:         runner.now(),
		Duration:   result.Duration,
		ExitReason: exitReason,
	}
	if seatErr != nil {
		event.Error = seatErr.Error()
	}
	if eventErr := runner.dependencies.Events.Record(event); eventErr != nil {
		seatErr = errors.Join(seatErr, fmt.Errorf("record %s seat finish: %w", role, eventErr))
	}
	if seatErr != nil {
		return result, fmt.Errorf("%s seat: %w", role, seatErr)
	}
	return result, nil
}

func (runner *Runner) validateDependencies() error {
	if runner.dependencies.Commands == nil {
		return errors.New("seat runner requires a command runner")
	}
	if runner.dependencies.Host == nil {
		return errors.New("seat runner requires a tmux host")
	}
	if runner.dependencies.Processes == nil {
		return errors.New("seat runner requires a process-tree scanner")
	}
	if runner.dependencies.Jailer == nil {
		return errors.New("seat runner requires a process-group jailer")
	}
	if runner.dependencies.Rollouts == nil {
		return errors.New("seat runner requires a rollout locator")
	}
	if runner.dependencies.Events == nil {
		return errors.New("seat runner requires an event sink")
	}
	if runner.seatTimeout != PerSeatTimeout && runner.seatTimeout <= 0 {
		return errors.New("seat timeout must be positive")
	}
	if runner.promptProofTimeout <= 0 {
		return errors.New("prompt proof timeout must be positive")
	}
	return nil
}

func (runner *Runner) now() time.Time {
	if runner.dependencies.Now != nil {
		return runner.dependencies.Now()
	}
	return time.Now()
}

func validateStage(stage string) (string, error) {
	if stage == "" || !filepath.IsAbs(stage) {
		return "", errors.New("seat stage must be an absolute directory")
	}
	clean := filepath.Clean(stage)
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", fmt.Errorf("resolve seat stage: %w", err)
	}
	if resolved != clean {
		return "", fmt.Errorf("seat stage must not traverse symlinks: %s resolves to %s", clean, resolved)
	}
	info, err := os.Stat(clean)
	if err != nil {
		return "", fmt.Errorf("stat seat stage: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("seat stage is not a directory: %s", clean)
	}
	return clean, nil
}

func validateSeatInput(role string, input SeatInput) error {
	if strings.TrimSpace(input.Name) == "" || hasControl(input.Name) {
		return fmt.Errorf("%s seat requires a one-line name", role)
	}
	if input.Socket == "" || input.Socket == "." || input.Socket == ".." ||
		strings.ContainsAny(input.Socket, "/\\") || hasControl(input.Socket) {
		return fmt.Errorf("%s seat requires a safe tmux socket name", role)
	}
	if strings.TrimSpace(input.Prompt) == "" || strings.ContainsRune(input.Prompt, '\x00') {
		return fmt.Errorf("%s seat requires a non-empty prompt", role)
	}
	return nil
}

func hasControl(value string) bool {
	for _, character := range value {
		if character < ' ' || character == 0x7f {
			return true
		}
	}
	return false
}

func verifySandboxedPlan(
	plan action.HeadlessPlan,
	stage string,
	config PinnedConfig,
) error {
	if plan.PromptOnCommandLine {
		return errors.New("sandboxed Codex plan put the prompt on the command line")
	}
	for _, forbidden := range []string{
		"--dangerously-bypass-approvals-and-sandbox",
		"--ignore-user-config",
		"--ephemeral",
		"CODEX_HOME=",
	} {
		if strings.Contains(plan.Run, forbidden) {
			return fmt.Errorf("sandboxed Codex plan contains forbidden %q", forbidden)
		}
	}
	for _, required := range append([]string{
		"--sandbox",
		"workspace-write",
		stage,
	}, config.Overrides...) {
		if !strings.Contains(plan.Run, required) {
			return fmt.Errorf("sandboxed Codex plan omitted %q", required)
		}
	}
	return nil
}

func monitorExitReason(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case strings.Contains(err.Error(), "ambiguous rollout"):
		return "rollout-ambiguity"
	case strings.Contains(err.Error(), "exited") || strings.Contains(err.Error(), "dead"):
		return "dead"
	default:
		return "monitor-error"
	}
}

func clonePinnedConfig(config PinnedConfig) PinnedConfig {
	config.Servers = append([]string(nil), config.Servers...)
	config.Overrides = append([]string(nil), config.Overrides...)
	return config
}

func cloneVerification(verification Verification) Verification {
	verification.Servers = append([]MCPServer(nil), verification.Servers...)
	verification.Overrides = append([]string(nil), verification.Overrides...)
	return verification
}
