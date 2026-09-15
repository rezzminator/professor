package seat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/headless"
	"hostops/pfm/internal/spawn"
)

type fakePane struct {
	state  string
	prompt string
	name   string
	alive  bool
}

type fakeHost struct {
	mu            sync.Mutex
	panes         map[string]*fakePane
	specs         []spawn.SessionSpec
	kills         []string
	confirmRename bool
	cropPrompt    bool
	dropPromptKey bool
	newErr        error
	killErr       error
	paneRootPID   int
	paneRootErr   error
}

func newFakeHost() *fakeHost {
	return &fakeHost{panes: make(map[string]*fakePane), confirmRename: true, paneRootPID: 100}
}

func (host *fakeHost) NewSession(_ context.Context, spec spawn.SessionSpec) error {
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.newErr != nil {
		return host.newErr
	}
	host.specs = append(host.specs, spec)
	host.panes[spec.Socket] = &fakePane{state: "composer", alive: true}
	return nil
}

func (host *fakeHost) Capture(
	_ context.Context,
	socket, _ string,
) (string, error) {
	host.mu.Lock()
	defer host.mu.Unlock()
	pane, ok := host.panes[socket]
	if !ok || !pane.alive {
		return "", errors.New("socket is dead")
	}
	switch pane.state {
	case "composer":
		return "›\n100% used", nil
	case "rename-offer":
		return "rename the current thread", nil
	case "name-prompt", "name-typed":
		return "Type a name and press Enter", nil
	case "renamed":
		return "Session renamed to " + pane.name + "\n›\n100% used", nil
	case "prompt-typed":
		if host.cropPrompt {
			// A real 20KB+ composer can scroll its first line outside the
			// visible capture. The prompt is still present (and its leading
			// composer marker is visible), but spawn's first-line fingerprint
			// cannot occur in these cropped rows.
			return "› tail of the large prompt remains in the composer\n100% used", nil
		}
		first := pane.prompt
		if index := strings.IndexByte(first, '\n'); index >= 0 {
			first = first[:index]
		}
		return "› " + first + "\n100% used", nil
	case "submitted":
		return "working", nil
	default:
		return "", fmt.Errorf("unknown fake pane state %q", pane.state)
	}
}

func (host *fakeHost) SendLiteral(
	_ context.Context,
	socket, _ string,
	value string,
) error {
	host.mu.Lock()
	defer host.mu.Unlock()
	pane := host.panes[socket]
	switch {
	case value == "/rename":
		pane.state = "rename-offer"
	case pane.state == "name-prompt":
		pane.name = value
		pane.state = "name-typed"
	default:
		pane.prompt = value
		pane.state = "prompt-typed"
	}
	return nil
}

func (host *fakeHost) SendKey(
	_ context.Context,
	socket, _ string,
	key string,
) error {
	host.mu.Lock()
	defer host.mu.Unlock()
	pane := host.panes[socket]
	switch {
	case key == "Escape":
		pane.state = "composer"
	case key == "Enter" && pane.state == "rename-offer":
		pane.state = "name-prompt"
	case key == "Enter" && pane.state == "name-typed" && host.confirmRename:
		pane.state = "renamed"
	case key == "Enter" && pane.state == "prompt-typed" && !host.dropPromptKey:
		pane.state = "submitted"
	}
	return nil
}

func (host *fakeHost) SocketAlive(_ context.Context, socket string) bool {
	host.mu.Lock()
	defer host.mu.Unlock()
	pane, ok := host.panes[socket]
	return ok && pane.alive
}

func (host *fakeHost) PaneRootPID(
	_ context.Context,
	_, _ string,
) (int, error) {
	return host.paneRootPID, host.paneRootErr
}

func (host *fakeHost) KillServer(_ context.Context, socket string) error {
	host.mu.Lock()
	defer host.mu.Unlock()
	if host.killErr != nil {
		return host.killErr
	}
	if pane, ok := host.panes[socket]; ok {
		pane.alive = false
	}
	host.kills = append(host.kills, socket)
	return nil
}

type fakeRollouts struct {
	chats         map[string]headless.Chat
	snapshots     int
	locates       int
	locateErr     error
	cancel        context.CancelFunc
	cancelOnFirst bool
}

type fakeProcessTree struct {
	verification ProcessTreeVerification
	err          error
	calls        []int
	before       func()
}

type fakeProcessJailer struct {
	captures   []ProcessGroupJail
	kills      []ProcessGroupJail
	captureErr error
	killErr    error
}

func (jailer *fakeProcessJailer) Capture(
	_ context.Context,
	rootPID int,
) (ProcessGroupJail, error) {
	if jailer.captureErr != nil {
		return ProcessGroupJail{}, jailer.captureErr
	}
	jail := ProcessGroupJail{
		RootPID: rootPID, RootStartTicks: uint64(rootPID * 10),
		GroupID: rootPID, SessionID: rootPID,
	}
	jailer.captures = append(jailer.captures, jail)
	return jail, nil
}

func (jailer *fakeProcessJailer) Kill(
	_ context.Context,
	jail ProcessGroupJail,
) error {
	jailer.kills = append(jailer.kills, jail)
	return jailer.killErr
}

func cleanFakeProcessTree() *fakeProcessTree {
	return &fakeProcessTree{verification: ProcessTreeVerification{
		RootReadable:        true,
		Root:                ProcessRecord{PID: 100, ParentPID: 1, StartTicks: 1000, Command: "node:codex"},
		ProcessesEnumerated: 2,
		RelationsEnumerated: 2,
		Descendants: []ProcessRecord{
			{PID: 101, ParentPID: 100, StartTicks: 1010, Command: "codex"},
		},
	}}
}

func (processes *fakeProcessTree) Inspect(
	_ context.Context,
	pid int,
) (ProcessTreeVerification, error) {
	processes.calls = append(processes.calls, pid)
	if processes.before != nil {
		processes.before()
	}
	return cloneProcessTreeVerification(processes.verification), processes.err
}

func (rollouts *fakeRollouts) Snapshot(context.Context) (RolloutSnapshot, error) {
	rollouts.snapshots++
	return RolloutSnapshot{paths: map[string]struct{}{}}, nil
}

func (rollouts *fakeRollouts) Locate(
	_ context.Context,
	_ RolloutSnapshot,
	match RolloutMatch,
) (headless.Chat, bool, error) {
	rollouts.locates++
	if rollouts.cancelOnFirst && rollouts.locates == 1 {
		rollouts.cancel()
	}
	if rollouts.locateErr != nil {
		return headless.Chat{}, false, rollouts.locateErr
	}
	chat, ok := rollouts.chats[match.Name]
	if ok {
		chat.Name = match.Name
		chat.Socket = match.Socket
		chat.Session = match.Session
	}
	return chat, ok, nil
}

type eventLog struct {
	events []Event
	err    error
}

type testNightRequest struct {
	law     SeatLaw
	stage   string
	distill SeatInput
	refiner SeatInput
}

type testNightResult struct {
	config       PinnedConfig
	verification Verification
	distill      SeatResult
	refiner      SeatResult
}

func (log *eventLog) Record(event Event) error {
	log.events = append(log.events, event)
	return log.err
}

func successfulRunner(
	t *testing.T,
) (*Runner, *fakeHost, *fakeRollouts, *scriptedCommands, *eventLog, testNightRequest) {
	t.Helper()
	stage := t.TempDir()
	distillTranscript := filepath.Join(t.TempDir(), "distill.jsonl")
	refinerTranscript := filepath.Join(t.TempDir(), "refiner.jsonl")
	writeCodexTranscript(t, distillTranscript, codexAssistant("distill final"))
	writeCodexTranscript(t, refinerTranscript, codexAssistant("refiner final"))

	host := newFakeHost()
	rollouts := &fakeRollouts{chats: map[string]headless.Chat{
		"night-distill": {ID: "distill-id", Engine: pfmengine.Codex, Path: distillTranscript, CWD: stage},
		"night-refiner": {ID: "refiner-id", Engine: pfmengine.Codex, Path: refinerTranscript, CWD: stage},
	}}
	commands := &scriptedCommands{results: []CommandResult{
		{Stdout: discoveryMCPOutput, ExitCode: 0},
		{Stdout: verifiedMCPOutput, ExitCode: 0},
	}}
	events := &eventLog{}
	processes := cleanFakeProcessTree()
	jailer := &fakeProcessJailer{}
	runner := NewRunner(Dependencies{
		Commands:  commands,
		Host:      host,
		Processes: processes,
		Jailer:    jailer,
		Rollouts:  rollouts,
		Events:    events,
	})
	runner.poll = time.Microsecond
	runner.spawnTimings = spawn.Timings{
		Poll: time.Microsecond, Boot: 200 * time.Millisecond,
		Step: 200 * time.Millisecond, Typed: time.Microsecond,
	}
	request := testNightRequest{
		law:   RequiredSeatLaw(),
		stage: stage,
		distill: SeatInput{
			Name: "night-distill", Socket: "dream-distill-socket", Prompt: "DISTILL\nbrief",
		},
		refiner: SeatInput{
			Name: "night-refiner", Socket: "dream-refiner-socket", Prompt: "REFINE\nbrief",
		},
	}
	return runner, host, rollouts, commands, events, request
}

func runTestNight(
	ctx context.Context,
	runner *Runner,
	request testNightRequest,
) (testNightResult, error) {
	prepared, err := runner.PrepareNight(ctx, request.law, request.stage)
	if err != nil {
		return testNightResult{}, err
	}
	result := testNightResult{
		config:       prepared.Config(),
		verification: prepared.Verification(),
	}
	result.distill, err = prepared.RunDistill(ctx, request.distill)
	if err != nil {
		return result, err
	}
	result.refiner, err = prepared.RunRefiner(ctx, request.refiner)
	return result, err
}

func TestSeatLawRunsBeforeEveryEffect(t *testing.T) {
	host := newFakeHost()
	rollouts := &fakeRollouts{}
	commands := &scriptedCommands{}
	events := &eventLog{}
	runner := NewRunner(Dependencies{
		Commands:  commands,
		Host:      host,
		Processes: cleanFakeProcessTree(),
		Jailer:    &fakeProcessJailer{},
		Rollouts:  rollouts,
		Events:    events,
	})
	_, err := runner.PrepareNight(context.Background(), SeatLaw{
		Distill: SeatPolicy{Model: "wrong", Effort: SeatEffort},
		Refiner: SeatPolicy{Model: SeatModel, Effort: SeatEffort},
	}, "/does/not/exist")
	if err == nil || !strings.Contains(err.Error(), "luna law") {
		t.Fatalf("PrepareNight() error = %v, want luna law", err)
	}
	if len(commands.calls) != 0 || len(host.specs) != 0 || rollouts.snapshots != 0 || len(events.events) != 0 {
		t.Fatalf("law violation caused side effects: commands=%d spawns=%d snapshots=%d events=%d",
			len(commands.calls), len(host.specs), rollouts.snapshots, len(events.events))
	}
}

func TestRunnerUsesExactOrderOneAttemptAndLastAssistant(t *testing.T) {
	runner, host, rollouts, commands, events, request := successfulRunner(t)
	if runner.seatTimeout != PerSeatTimeout {
		t.Fatalf("live timeout = %s, want %s", runner.seatTimeout, PerSeatTimeout)
	}
	result, err := runTestNight(context.Background(), runner, request)
	if err != nil {
		t.Fatalf("RunNight() error = %v", err)
	}
	if got := []string{host.specs[0].Socket, host.specs[1].Socket}; !reflect.DeepEqual(got, []string{
		"dream-distill-socket", "dream-refiner-socket",
	}) {
		t.Fatalf("seat order = %q", got)
	}
	if len(host.specs) != 2 || rollouts.snapshots != 2 {
		t.Fatalf("attempts: spawns=%d snapshots=%d, want exactly one per seat", len(host.specs), rollouts.snapshots)
	}
	if !reflect.DeepEqual(host.kills, []string{"dream-distill-socket", "dream-refiner-socket"}) {
		t.Fatalf("cleanup order = %q", host.kills)
	}
	if result.distill.LastAssistant != "distill final" || result.refiner.LastAssistant != "refiner final" {
		t.Fatalf("last assistants = %q / %q", result.distill.LastAssistant, result.refiner.LastAssistant)
	}
	if result.distill.ExitReason != "idle" || result.refiner.ExitReason != "idle" {
		t.Fatalf("exit reasons = %q / %q", result.distill.ExitReason, result.refiner.ExitReason)
	}
	if len(commands.calls) != 2 {
		t.Fatalf("MCP commands = %d, want one discovery and one verification per night", len(commands.calls))
	}
	assertStructuredEvents(t, events.events, result.config)
}

func TestPreparedNightLeavesTheMechanicalGateGapAndSharesOnePinSet(t *testing.T) {
	runner, host, _, commands, events, request := successfulRunner(t)
	prepared, err := runner.PrepareNight(context.Background(), request.law, request.stage)
	if err != nil {
		t.Fatalf("PrepareNight() error = %v", err)
	}
	if len(host.specs) != 0 {
		t.Fatalf("PrepareNight spawned a seat: %d", len(host.specs))
	}
	if len(commands.calls) != 2 {
		t.Fatalf("config commands = %d, want discovery+verification once", len(commands.calls))
	}

	distill, err := prepared.RunDistill(context.Background(), request.distill)
	if err != nil {
		t.Fatalf("RunDistill() error = %v", err)
	}
	if distill.LastAssistant != "distill final" || len(host.specs) != 1 {
		t.Fatalf("distill = %#v, spawns=%d", distill, len(host.specs))
	}
	// This is where Night executes PIN/COVERAGE/ANCHORS and derives the
	// refiner brief. No seat package call occurs until the caller asks.
	if len(host.specs) != 1 || len(commands.calls) != 2 {
		t.Fatal("the mechanical gate gap caused an implicit seat/config operation")
	}
	refiner, err := prepared.RunRefiner(context.Background(), request.refiner)
	if err != nil {
		t.Fatalf("RunRefiner() error = %v", err)
	}
	if refiner.LastAssistant != "refiner final" || len(host.specs) != 2 {
		t.Fatalf("refiner = %#v, spawns=%d", refiner, len(host.specs))
	}
	if len(commands.calls) != 2 {
		t.Fatalf("config was re-derived between seats: %d commands", len(commands.calls))
	}
	if !reflect.DeepEqual(prepared.Config().Overrides, events.events[0].Config.Overrides) ||
		!reflect.DeepEqual(prepared.Verification().Overrides, events.events[1].Verification.Overrides) {
		t.Fatal("prepared handle did not preserve the exact logged pin set")
	}
}

func TestProcessTreeGateRunsAfterTUIIsUpBeforeEachBrief(t *testing.T) {
	runner, host, _, _, _, request := successfulRunner(t)
	processes := runner.dependencies.Processes.(*fakeProcessTree)
	checks := 0
	processes.before = func() {
		host.mu.Lock()
		defer host.mu.Unlock()
		var socket string
		if checks == 0 {
			socket = request.distill.Socket
		} else {
			socket = request.refiner.Socket
		}
		pane := host.panes[socket]
		if pane == nil || pane.state != "renamed" {
			t.Fatalf("gate %d ran before the TUI was genuinely up: %#v", checks+1, pane)
		}
		if pane.prompt != "" {
			t.Fatalf("gate %d ran after the brief reached the pane: %q", checks+1, pane.prompt)
		}
		checks++
	}

	if _, err := runTestNight(context.Background(), runner, request); err != nil {
		t.Fatalf("RunNight() error = %v", err)
	}
	if checks != 2 || !reflect.DeepEqual(processes.calls, []int{100, 100}) {
		t.Fatalf("process gates = %d calls=%v, want one exact-root scan per seat", checks, processes.calls)
	}
}

func TestExternalProcessFailsBeforeBriefAndLogsObservedTree(t *testing.T) {
	runner, host, _, _, events, request := successfulRunner(t)
	processes := runner.dependencies.Processes.(*fakeProcessTree)
	processes.verification.Descendants = append(
		processes.verification.Descendants,
		ProcessRecord{PID: 102, ParentPID: 101, StartTicks: 1020, Command: "npm"},
	)
	processes.verification.ProcessesEnumerated++
	processes.verification.RelationsEnumerated++

	result, err := runTestNight(context.Background(), runner, request)
	if err == nil || !strings.Contains(err.Error(), "external/MCP descendant") {
		t.Fatalf("RunNight() error = %v, want external descendant failure", err)
	}
	if result.distill.ExitReason != "process-tree-gate" {
		t.Fatalf("ExitReason = %q, want process-tree-gate", result.distill.ExitReason)
	}
	host.mu.Lock()
	prompt := host.panes[request.distill.Socket].prompt
	host.mu.Unlock()
	if prompt != "" {
		t.Fatalf("brief reached failed seat: %q", prompt)
	}
	if !reflect.DeepEqual(host.kills, []string{request.distill.Socket}) {
		t.Fatalf("failed process gate did not clean server: %q", host.kills)
	}
	if len(events.events) != 5 {
		t.Fatalf("failed gate events = %#v", events.events)
	}
	gateEvent := events.events[3]
	if gateEvent.ProcessTree == nil || gateEvent.ProcessTree.Clean || gateEvent.Error == "" ||
		len(gateEvent.ProcessTree.Descendants) != 2 {
		t.Fatalf("failed process-tree event = %#v", gateEvent)
	}
}

func TestProcessTreeFailureEventCannotLeakCommandArguments(t *testing.T) {
	runner, _, _, _, events, request := successfulRunner(t)
	processes := runner.dependencies.Processes.(*fakeProcessTree)
	processes.verification.Descendants = append(
		processes.verification.Descendants,
		ProcessRecord{PID: 102, ParentPID: 101, StartTicks: 1020, Command: "npm"},
	)
	processes.verification.ProcessesEnumerated++
	processes.verification.RelationsEnumerated++

	_, _ = runTestNight(context.Background(), runner, request)
	encoded, err := json.Marshal(events.events[3])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "argv") || strings.Contains(string(encoded), "token=") {
		t.Fatalf("process-tree event carries command arguments: %s", encoded)
	}
	for _, command := range []string{"node:codex", "codex", "npm"} {
		if !strings.Contains(string(encoded), command) {
			t.Fatalf("process-tree event omitted safe command identity %q: %s", command, encoded)
		}
	}
}

func TestUnresolvedPaneRootFailsBeforeBriefAndLogsVisibilityFailure(t *testing.T) {
	runner, host, _, _, events, request := successfulRunner(t)
	host.paneRootErr = errors.New("tmux did not expose pane pid")

	result, err := runTestNight(context.Background(), runner, request)
	if err == nil || !strings.Contains(err.Error(), "tmux did not expose pane pid") {
		t.Fatalf("RunNight() error = %v, want pane-root visibility failure", err)
	}
	if result.distill.ExitReason != "process-tree-gate" {
		t.Fatalf("ExitReason = %q, want process-tree-gate", result.distill.ExitReason)
	}
	gateEvent := events.events[3]
	if gateEvent.ProcessTree == nil || gateEvent.ProcessTree.PaneRootResolved || gateEvent.Error == "" {
		t.Fatalf("visibility failure event = %#v", gateEvent)
	}
}

func TestPreparedNightEnforcesOrderAndConsumesEveryAttempt(t *testing.T) {
	runner, host, _, _, _, request := successfulRunner(t)
	prepared, err := runner.PrepareNight(context.Background(), request.law, request.stage)
	if err != nil {
		t.Fatalf("PrepareNight() error = %v", err)
	}
	if _, err := prepared.RunRefiner(context.Background(), request.refiner); err == nil ||
		!strings.Contains(err.Error(), "before a successful distill") {
		t.Fatalf("early refiner error = %v", err)
	}
	if len(host.specs) != 0 {
		t.Fatalf("early refiner spawned: %d", len(host.specs))
	}
	if _, err := prepared.RunDistill(context.Background(), request.distill); err != nil {
		t.Fatalf("RunDistill() error = %v", err)
	}
	if _, err := prepared.RunDistill(context.Background(), request.distill); err == nil ||
		!strings.Contains(err.Error(), "retries are forbidden") {
		t.Fatalf("distill retry error = %v", err)
	}
	if _, err := prepared.RunRefiner(context.Background(), request.refiner); err != nil {
		t.Fatalf("RunRefiner() error = %v", err)
	}
	if _, err := prepared.RunRefiner(context.Background(), request.refiner); err == nil ||
		!strings.Contains(err.Error(), "retries are forbidden") {
		t.Fatalf("refiner retry error = %v", err)
	}
	if len(host.specs) != 2 {
		t.Fatalf("attempts spawned = %d, want exactly two", len(host.specs))
	}
}

func TestFailedPreparedDistillCannotRetryOrRunRefiner(t *testing.T) {
	runner, host, rollouts, _, _, request := successfulRunner(t)
	rollouts.locateErr = errors.New("ambiguous rollout")
	prepared, err := runner.PrepareNight(context.Background(), request.law, request.stage)
	if err != nil {
		t.Fatalf("PrepareNight() error = %v", err)
	}
	if _, err := prepared.RunDistill(context.Background(), request.distill); err == nil {
		t.Fatal("broken distill passed")
	}
	if _, err := prepared.RunDistill(context.Background(), request.distill); err == nil ||
		!strings.Contains(err.Error(), "retries are forbidden") {
		t.Fatalf("failed distill retry error = %v", err)
	}
	if _, err := prepared.RunRefiner(context.Background(), request.refiner); err == nil ||
		!strings.Contains(err.Error(), "before a successful distill") {
		t.Fatalf("refiner-after-failure error = %v", err)
	}
	if len(host.specs) != 1 {
		t.Fatalf("failed attempt spawned %d seats, want 1", len(host.specs))
	}
}

func TestPreparedNightLawStillPrecedesEveryEffect(t *testing.T) {
	host := newFakeHost()
	commands := &scriptedCommands{}
	rollouts := &fakeRollouts{}
	events := &eventLog{}
	runner := NewRunner(
		Dependencies{
			Commands:  commands,
			Host:      host,
			Processes: cleanFakeProcessTree(),
			Jailer:    &fakeProcessJailer{},
			Rollouts:  rollouts,
			Events:    events,
		},
	)
	_, err := runner.PrepareNight(context.Background(), SeatLaw{}, "/does/not/exist")
	if err == nil || !strings.Contains(err.Error(), "luna law") {
		t.Fatalf("PrepareNight() error = %v", err)
	}
	if len(commands.calls)+len(host.specs)+rollouts.snapshots+len(events.events) != 0 {
		t.Fatal("prepared-night law violation caused an effect")
	}
}

func TestSeatPlanIsStageSandboxedAndCarriesEveryPin(t *testing.T) {
	runner, host, _, _, _, request := successfulRunner(t)
	result, err := runTestNight(context.Background(), runner, request)
	if err != nil {
		t.Fatalf("RunNight() error = %v", err)
	}
	for _, spec := range host.specs {
		if spec.CWD != request.stage {
			t.Fatalf("spawn CWD = %q, want stage %q", spec.CWD, request.stage)
		}
		for _, forbidden := range []string{
			"--dangerously-bypass-approvals-and-sandbox", "CODEX_HOME=", "--ephemeral", "--ignore-user-config",
		} {
			if strings.Contains(spec.Run, forbidden) {
				t.Errorf("sandboxed plan contains %q: %s", forbidden, spec.Run)
			}
		}
		for _, required := range append([]string{"--sandbox", "workspace-write", request.stage}, result.config.Overrides...) {
			if !strings.Contains(spec.Run, required) {
				t.Errorf("sandboxed plan lacks %q: %s", required, spec.Run)
			}
		}
	}
}

func TestTimeoutFailsClosedAndCleansTheProcessGroupWithoutRetry(t *testing.T) {
	runner, host, rollouts, _, events, request := successfulRunner(t)
	jailer := runner.dependencies.Jailer.(*fakeProcessJailer)
	working := filepath.Join(t.TempDir(), "working.jsonl")
	writeCodexTranscript(t, working, codexUser("still waiting"))
	rollouts.chats[request.distill.Name] = headless.Chat{
		ID: "working-id", Engine: pfmengine.Codex, Path: working, CWD: request.stage,
	}
	runner.seatTimeout = 3 * time.Millisecond
	runner.poll = 100 * time.Microsecond
	result, err := runTestNight(context.Background(), runner, request)
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("RunNight() error = %v, want timeout", err)
	}
	if result.distill.ExitReason != "timeout" {
		t.Fatalf("ExitReason = %q, want timeout", result.distill.ExitReason)
	}
	if len(host.specs) != 1 || !reflect.DeepEqual(host.kills, []string{request.distill.Socket}) {
		t.Fatalf("timeout attempts/cleanup: specs=%d kills=%q", len(host.specs), host.kills)
	}
	if len(jailer.captures) != 1 ||
		!reflect.DeepEqual(jailer.kills, jailer.captures) {
		t.Fatalf("timeout process-group cleanup = captures=%v kills=%#v", jailer.captures, jailer.kills)
	}
	if finish := lastEvent(events.events); finish.ExitReason != "timeout" || finish.Duration <= 0 {
		t.Fatalf("timeout finish event = %#v", finish)
	}
}

func TestContextCancellationStillUsesFreshCleanupContext(t *testing.T) {
	runner, host, rollouts, _, _, request := successfulRunner(t)
	jailer := runner.dependencies.Jailer.(*fakeProcessJailer)
	ctx, cancel := context.WithCancel(context.Background())
	rollouts.cancel = cancel
	rollouts.cancelOnFirst = true
	rollouts.chats = nil
	result, err := runTestNight(ctx, runner, request)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("RunNight() error = %v, want context canceled", err)
	}
	if result.distill.ExitReason != "canceled" {
		t.Fatalf("ExitReason = %q, want canceled", result.distill.ExitReason)
	}
	if !reflect.DeepEqual(host.kills, []string{request.distill.Socket}) {
		t.Fatalf("canceled parent stranded server; kills=%q", host.kills)
	}
	if len(jailer.captures) != 1 ||
		!reflect.DeepEqual(jailer.kills, jailer.captures) {
		t.Fatalf("canceled parent process-group cleanup = captures=%#v kills=%#v", jailer.captures, jailer.kills)
	}
}

func TestProcessGroupCleanupFailureCannotRenderAsACompletedSeat(t *testing.T) {
	runner, host, _, _, _, request := successfulRunner(t)
	jailer := runner.dependencies.Jailer.(*fakeProcessJailer)
	jailer.killErr = errors.New("kernel refused group signal")
	result, err := runTestNight(context.Background(), runner, request)
	if err == nil || !strings.Contains(err.Error(), "kernel refused group signal") {
		t.Fatalf("RunNight() error = %v, want process-group cleanup failure", err)
	}
	if result.distill.ExitReason != "cleanup-error" {
		t.Fatalf("ExitReason = %q, want cleanup-error", result.distill.ExitReason)
	}
	if !reflect.DeepEqual(host.kills, []string{request.distill.Socket}) || len(jailer.kills) != 1 {
		t.Fatalf("cleanup effects = servers=%q groups=%#v", host.kills, jailer.kills)
	}
	if len(host.specs) != 1 {
		t.Fatalf("refiner ran after process-group cleanup failed: %d spawns", len(host.specs))
	}
}

func TestRolloutAmbiguityFailsClosedAndCleans(t *testing.T) {
	runner, host, rollouts, _, _, request := successfulRunner(t)
	rollouts.locateErr = errors.New("ambiguous rollout: two candidates")
	result, err := runTestNight(context.Background(), runner, request)
	if err == nil || !strings.Contains(err.Error(), "ambiguous rollout") {
		t.Fatalf("RunNight() error = %v", err)
	}
	if result.distill.ExitReason != "rollout-ambiguity" {
		t.Fatalf("ExitReason = %q", result.distill.ExitReason)
	}
	if !reflect.DeepEqual(host.kills, []string{request.distill.Socket}) {
		t.Fatalf("ambiguity did not clean server: %q", host.kills)
	}
}

func TestCleanupFailureCannotRenderAsACompletedSeat(t *testing.T) {
	runner, host, _, _, _, request := successfulRunner(t)
	host.killErr = errors.New("tmux refused kill")
	result, err := runTestNight(context.Background(), runner, request)
	if err == nil || !strings.Contains(err.Error(), "process group") {
		t.Fatalf("RunNight() error = %v, want cleanup failure", err)
	}
	if result.distill.ExitReason != "cleanup-error" {
		t.Fatalf("ExitReason = %q, want cleanup-error", result.distill.ExitReason)
	}
	if len(host.specs) != 1 {
		t.Fatalf("refiner ran after distill cleanup failed: %d spawns", len(host.specs))
	}
}

func TestUnprovenRenameOrPromptIsFailure(t *testing.T) {
	runner, host, _, _, _, request := successfulRunner(t)
	host.confirmRename = false
	result, err := runTestNight(context.Background(), runner, request)
	if err == nil || !strings.Contains(err.Error(), "not proven") {
		t.Fatalf("RunNight() error = %v, want unproven spawn", err)
	}
	if result.distill.ExitReason != "spawn-unproven" {
		t.Fatalf("ExitReason = %q", result.distill.ExitReason)
	}
	if !reflect.DeepEqual(host.kills, []string{request.distill.Socket}) {
		t.Fatalf("unproven spawn did not clean server: %q", host.kills)
	}
}

func TestLargeCroppedComposerCannotPassWithoutRolloutTaskStarted(t *testing.T) {
	runner, host, rollouts, _, events, request := successfulRunner(t)
	host.cropPrompt = true
	host.dropPromptKey = true
	rollouts.chats = nil
	request.distill.Prompt = "DISTILL-FIRST-LINE-DELIVERY-FINGERPRINT\n" +
		strings.Repeat("large distill brief row with enough material to fill the TUI\n", 500)
	// Keep the regression fast while leaving enough room to distinguish the
	// delivery-proof bound from the whole-seat bound.
	runner.spawnTimings.Step = 5 * time.Millisecond
	runner.promptProofTimeout = 5 * time.Millisecond
	runner.seatTimeout = 100 * time.Millisecond

	result, err := runTestNight(context.Background(), runner, request)
	if err == nil || !strings.Contains(err.Error(), "task_started") {
		t.Fatalf("RunNight() error = %v, want missing task_started proof", err)
	}
	if result.distill.ExitReason != "prompt-unproven" {
		t.Fatalf("ExitReason = %q, want prompt-unproven", result.distill.ExitReason)
	}
	host.mu.Lock()
	state := host.panes[request.distill.Socket].state
	host.mu.Unlock()
	if state != "prompt-typed" {
		t.Fatalf("fixture did not preserve the cropped composer: state=%q", state)
	}
	if rollouts.locates == 0 {
		t.Fatal("task-start proof never examined the rollout boundary")
	}
	if len(events.events) < 2 ||
		events.events[len(events.events)-2].Phase != "seat.prompt.started" ||
		events.events[len(events.events)-2].Error == "" {
		t.Fatalf("missing durable failed prompt proof event: %#v", events.events)
	}
}

func TestWrongOrMissingTranscriptModelFailsAfterCleanup(t *testing.T) {
	runner, host, rollouts, _, _, request := successfulRunner(t)
	path := filepath.Join(t.TempDir(), "wrong.jsonl")
	writeCodexTranscriptWithConfig(
		t, path, codexAssistant("answer"), "some-other-model", SeatEffort,
	)
	rollouts.chats[request.distill.Name] = headless.Chat{
		ID: "wrong-id", Engine: pfmengine.Codex, Path: path, CWD: request.stage,
	}
	result, err := runTestNight(context.Background(), runner, request)
	if err == nil || !strings.Contains(err.Error(), "reports model") {
		t.Fatalf("RunNight() error = %v", err)
	}
	if result.distill.ExitReason != "model-mismatch" {
		t.Fatalf("ExitReason = %q", result.distill.ExitReason)
	}
	if !reflect.DeepEqual(host.kills, []string{request.distill.Socket}) {
		t.Fatalf("model mismatch did not clean server: %q", host.kills)
	}
}

func TestWrongTranscriptEffortFailsAfterCleanup(t *testing.T) {
	runner, host, rollouts, _, _, request := successfulRunner(t)
	path := filepath.Join(t.TempDir(), "wrong-effort.jsonl")
	writeCodexTranscriptWithConfig(
		t, path, codexAssistant("answer"), SeatModel, "low",
	)
	rollouts.chats[request.distill.Name] = headless.Chat{
		ID: "wrong-effort-id", Engine: pfmengine.Codex, Path: path, CWD: request.stage,
	}
	result, err := runTestNight(context.Background(), runner, request)
	if err == nil || !strings.Contains(err.Error(), "reports effort") {
		t.Fatalf("RunNight() error = %v", err)
	}
	if result.distill.ExitReason != "effort-mismatch" {
		t.Fatalf("ExitReason = %q", result.distill.ExitReason)
	}
	if !reflect.DeepEqual(host.kills, []string{request.distill.Socket}) {
		t.Fatalf("effort mismatch did not clean server: %q", host.kills)
	}
}

func TestEventWriteFailureStopsBeforeSpawn(t *testing.T) {
	runner, host, _, _, events, request := successfulRunner(t)
	events.err = errors.New("disk full")
	_, err := runTestNight(context.Background(), runner, request)
	if err == nil || !strings.Contains(err.Error(), "record pinned seat configuration") {
		t.Fatalf("RunNight() error = %v", err)
	}
	if len(host.specs) != 0 {
		t.Fatalf("seat spawned without a durable pin event: %d", len(host.specs))
	}
}

func TestFailedMCPVerificationIsLoggedWithFactsBeforeReturning(t *testing.T) {
	runner, host, _, commands, events, request := successfulRunner(t)
	commands.results[1] = CommandResult{
		Stdout:   "No MCP servers configured yet. Try `codex mcp add my-tool -- my-command`.\n",
		Stderr:   "failed to load bootstrap configuration",
		ExitCode: 1,
	}
	_, err := runTestNight(context.Background(), runner, request)
	if err == nil || !strings.Contains(err.Error(), "exited 1") {
		t.Fatalf("RunNight() error = %v, want failed config load", err)
	}
	if len(host.specs) != 0 {
		t.Fatalf("seat spawned after failed MCP verification: %d", len(host.specs))
	}
	if len(events.events) != 2 || events.events[0].Config == nil ||
		events.events[1].Verification == nil || events.events[1].Error == "" {
		t.Fatalf("failed verification events = %#v", events.events)
	}
	verification := events.events[1].Verification
	if verification.ExitCode != 1 || verification.ConfigLoaded {
		t.Fatalf("failed verification event = %#v", verification)
	}
}

func TestProjectRootForStageUsesNearestGitMarkerAndFallsBackToStage(t *testing.T) {
	repository := t.TempDir()
	stage := filepath.Join(repository, "organ", "tmp", "staging", "night")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repository, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := projectRootForStage(stage)
	if err != nil {
		t.Fatal(err)
	}
	if root != repository {
		t.Fatalf("project root = %q, want %q", root, repository)
	}

	standalone := string(filepath.Separator)
	root, err = projectRootForStage(standalone)
	if err != nil {
		t.Fatal(err)
	}
	if root != standalone {
		t.Fatalf("non-repository project root = %q, want stage itself", root)
	}
}

func assertStructuredEvents(t *testing.T, events []Event, config PinnedConfig) {
	t.Helper()
	phases := make([]string, 0, len(events))
	for _, event := range events {
		phases = append(phases, event.Phase+":"+event.Seat)
	}
	want := []string{
		"seat.config.pinned:",
		"seat.config.verified:",
		"seat.start:distill",
		"seat.process-tree.verified:distill",
		"seat.prompt.started:distill",
		"seat.finish:distill",
		"seat.start:refiner",
		"seat.process-tree.verified:refiner",
		"seat.prompt.started:refiner",
		"seat.finish:refiner",
	}
	if !reflect.DeepEqual(phases, want) {
		t.Fatalf("event phases = %q, want %q", phases, want)
	}
	if events[0].Config == nil || !reflect.DeepEqual(events[0].Config.Overrides, config.Overrides) {
		t.Fatalf("pin event omitted full overrides: %#v", events[0])
	}
	verification := events[1].Verification
	if verification == nil || !verification.ConfigLoaded || verification.Enabled != 0 ||
		verification.Limitation != MCPVerificationLimitation {
		t.Fatalf("verification event = %#v", events[1])
	}
	for _, event := range []Event{events[5], events[9]} {
		if event.ExitReason != "idle" || event.Error != "" || event.Duration < 0 {
			t.Fatalf("finish event = %#v", event)
		}
	}
	for _, event := range []Event{events[3], events[7]} {
		if event.ProcessTree == nil || !event.ProcessTree.Clean ||
			!event.ProcessTree.PaneRootResolved || !event.ProcessTree.RootReadable ||
			event.ProcessTree.RelationsEnumerated == 0 {
			t.Fatalf("process-tree event = %#v", event)
		}
	}
}

func lastEvent(events []Event) Event {
	return events[len(events)-1]
}

func writeCodexTranscript(t *testing.T, path, lastLine string) {
	t.Helper()
	writeCodexTranscriptWithConfig(t, path, lastLine, SeatModel, SeatEffort)
}

func writeCodexTranscriptWithConfig(
	t *testing.T,
	path, lastLine, model, effort string,
) {
	t.Helper()
	content := sessionMeta("id", filepath.Dir(path)) + "\n" +
		`{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn"}}` + "\n" +
		fmt.Sprintf(
			`{"type":"turn_context","payload":{"turn_id":"turn","model":%q,"effort":%q}}`,
			model,
			effort,
		) + "\n" +
		lastLine + "\n"
	if strings.Contains(lastLine, `"type":"agent_message"`) {
		content += `{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn","last_agent_message":"done"}}` + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestPromptProofExaminesTheRolloutOnceEvenWhenItsWindowIsAlreadySpent pins
// the proof loop's order: the rollout boundary is examined BEFORE the proof
// deadline is honoured, so a window that expires between arming and the
// first poll (a starved scheduler, a tiny timeout) still yields an examined
// verdict — never "unproven" without a single look.
func TestPromptProofExaminesTheRolloutOnceEvenWhenItsWindowIsAlreadySpent(t *testing.T) {
	runner, host, rollouts, _, _, request := successfulRunner(t)
	host.cropPrompt = true
	host.dropPromptKey = true
	rollouts.chats = nil
	runner.spawnTimings.Step = 5 * time.Millisecond
	runner.promptProofTimeout = time.Nanosecond
	runner.seatTimeout = 100 * time.Millisecond

	_, err := runTestNight(context.Background(), runner, request)
	if err == nil || !strings.Contains(err.Error(), "task_started") {
		t.Fatalf("RunNight() error = %v, want missing task_started proof", err)
	}
	if rollouts.locates == 0 {
		t.Fatal("task-start proof honoured its deadline without examining the rollout boundary once")
	}
}
