package gather

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"syscall"
	"time"

	"github.com/rezzminator/professor/pfm/internal/agentlauncher"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Identity is one process generation as discovery saw it. A pid alone is never
// a target: the kernel hands a freed pid to the next process, so a stop names
// the generation (start time), its owner and its command line too.
type Identity struct {
	PID   int
	Start uint64
	UID   uint32
	Argv  []string
	// Executable is the kernel's executable path, "" where the table cannot
	// read it; it is compared only when both reads have one.
	Executable string
}

// IdentifyProcess reads pid's identity from procs. A table that cannot read a uid
// cannot identify anything and is an error; a pid that is gone or a zombie is
// an error wrapping fs.ErrNotExist, so IsGone tells it from a failed read.
func IdentifyProcess(procs ProcFS, pid int) (Identity, error) {
	if pid <= 0 {
		return Identity{}, fmt.Errorf("identify process: %d is not a process id", pid)
	}
	identities, ok := procs.(ProcIdentity)
	if !ok {
		return Identity{}, fmt.Errorf("identify process %d: the process table cannot read owners", pid)
	}
	meta, err := identities.ProcessIdentity(pid)
	if err != nil {
		return Identity{}, identifyError(pid, "owner", err)
	}
	if meta.Zombie {
		return Identity{}, fmt.Errorf("identify process %d: a zombie: %w", pid, fs.ErrNotExist)
	}
	stat, err := procs.Stat(pid)
	if err != nil {
		return Identity{}, identifyError(pid, "start time", err)
	}
	argv, err := procs.Cmdline(pid)
	if err != nil {
		return Identity{}, identifyError(pid, "command line", err)
	}
	identity := Identity{PID: pid, Start: stat.StartTime, UID: meta.EffectiveUID, Argv: argv}
	if executables, ok := procs.(ProcExecutable); ok {
		// Best effort: a table that withholds the executable still identifies
		// the process by start time, owner and argv.
		if executable, err := executables.Executable(pid); err == nil {
			identity.Executable = executable
		}
	}
	return identity, nil
}

func identifyError(pid int, field string, err error) error {
	if IsGone(err) {
		return fmt.Errorf("identify process %d: %s: %w (%w)", pid, field, fs.ErrNotExist, err)
	}
	return fmt.Errorf("identify process %d: read %s: %w", pid, field, err)
}

// IsGone reports whether err says the process no longer exists.
func IsGone(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH)
}

// Check is a site's own match, run on the live identity right before a signal:
// false withholds the signal as a changed process.
type Check func(live Identity) (bool, error)

// Target is a process to signal: the identity discovery captured, and the
// site's own match that must still hold.
type Target struct {
	Identity
	Check Check
}

// Outcome is what one Signal did.
type Outcome int

const (
	// OutcomeSent delivered the signal to the identified process.
	OutcomeSent Outcome = iota + 1
	// OutcomeGone sent nothing: the pid is gone, a zombie, or a later
	// generation (another start time).
	OutcomeGone
	// OutcomeChanged sent nothing: the same generation now has another owner,
	// command line or executable, or the site's Check no longer holds.
	OutcomeChanged
)

func (outcome Outcome) String() string {
	switch outcome {
	case OutcomeSent:
		return "sent"
	case OutcomeGone:
		return "gone"
	case OutcomeChanged:
		return "changed"
	}
	return fmt.Sprintf("outcome(%d)", int(outcome))
}

// ErrRefused marks a target the door never signals whatever the table says:
// pid 1 or below, this process, another user's process, or an identity with
// no start time to tell generations apart.
var ErrRefused = errors.New("signal refused")

// ErrStillRunning marks a process Stop could not end within its graces.
var ErrStillRunning = errors.New("still running")

// Kernel opens a process for signalling. The handle pins the process where the
// platform can (a Linux pidfd), so the identity read after Open is the identity
// the signal reaches.
type Kernel interface {
	Open(pid int) (Handle, error)
}

// Handle signals the process a Kernel opened.
type Handle interface {
	Signal(sig syscall.Signal) error
	Close() error
}

// Door is the one way pfm signals a process it found rather than started:
// every signal re-identifies the live process first and is withheld from
// anything that is not the identity discovery captured.
type Door struct {
	Procs  ProcFS
	Kernel Kernel
	Clock  clock.Clock
	// UID is the effective uid a target must run as; Self is this process.
	UID  uint32
	Self int
}

// NativeKernel is the platform's kernel: a pidfd on Linux, kill(2) on darwin.
func NativeKernel() Kernel { return nativeKernel{} }

// NewDoor is the real door over procs: the platform's kernel, the wall clock,
// this process's effective uid and pid.
func NewDoor(procs ProcFS) Door {
	return Door{Procs: procs, Kernel: NativeKernel(), Clock: clock.Real, UID: uint32(os.Geteuid()), Self: os.Getpid()}
}

func (door Door) refuse(id Identity) error {
	switch {
	case id.PID <= 1:
		return fmt.Errorf("pid %d: %w: not a single user process", id.PID, ErrRefused)
	case id.PID == door.Self:
		return fmt.Errorf("pid %d: %w: this process", id.PID, ErrRefused)
	case id.Start == 0:
		return fmt.Errorf("pid %d: %w: no start time to tell its generation", id.PID, ErrRefused)
	case id.UID != door.UID:
		return fmt.Errorf("pid %d: %w: runs as uid %d, not %d", id.PID, ErrRefused, id.UID, door.UID)
	}
	return nil
}

// generationOutcome classifies live against the captured identity.
func generationOutcome(captured, live Identity) Outcome {
	switch {
	case live.Start != captured.Start:
		return OutcomeGone
	case live.UID != captured.UID, !slices.Equal(live.Argv, captured.Argv):
		return OutcomeChanged
	case live.Executable != "" && captured.Executable != "" && live.Executable != captured.Executable:
		return OutcomeChanged
	}
	return OutcomeSent
}

// Signal sends sig to target only while the live process is target's identity
// and target's Check holds. It opens the process first (on Linux a pidfd, so
// pid reuse after the read cannot redirect the signal), re-reads it, and
// re-reads again after a Check, which may take time.
func (door Door) Signal(target Target, sig syscall.Signal) (Outcome, error) {
	if err := door.refuse(target.Identity); err != nil {
		return 0, err
	}
	handle, err := door.Kernel.Open(target.PID)
	if IsGone(err) {
		return OutcomeGone, nil
	}
	if err != nil {
		return 0, fmt.Errorf("open pid %d to signal: %w", target.PID, err)
	}
	defer func() { _ = handle.Close() }()
	outcome, err := door.verify(target.Identity)
	if err != nil || outcome != OutcomeSent {
		return outcome, err
	}
	if target.Check != nil {
		live, err := IdentifyProcess(door.Procs, target.PID)
		if IsGone(err) {
			return OutcomeGone, nil
		}
		if err != nil {
			return 0, fmt.Errorf("re-read pid %d before %s: %w", target.PID, sig, err)
		}
		holds, err := target.Check(live)
		if err != nil {
			return 0, fmt.Errorf("check pid %d before %s: %w", target.PID, sig, err)
		}
		// The Check may have waited: re-read either way, so a process that
		// exited meanwhile reads gone, not changed.
		if outcome, err := door.verify(target.Identity); err != nil || outcome != OutcomeSent {
			return outcome, err
		}
		if !holds {
			return OutcomeChanged, nil
		}
	}
	if err := handle.Signal(sig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return OutcomeGone, nil
		}
		return 0, fmt.Errorf("send %s to pid %d: %w", sig, target.PID, err)
	}
	return OutcomeSent, nil
}

// verify re-reads the live process and compares it with id.
func (door Door) verify(id Identity) (Outcome, error) {
	live, err := IdentifyProcess(door.Procs, id.PID)
	if IsGone(err) {
		return OutcomeGone, nil
	}
	if err != nil {
		return 0, fmt.Errorf("re-read pid %d before signalling: %w", id.PID, err)
	}
	return generationOutcome(id, live), nil
}

// Alive reports whether id's generation still runs: the same pid since the
// same start, and not a zombie. Another start time is a later process, so
// the one id names is gone; a bare kill -0 cannot tell the two apart.
func (door Door) Alive(id Identity) (bool, error) {
	identities, ok := door.Procs.(ProcIdentity)
	if !ok {
		return false, fmt.Errorf("probe pid %d: the process table cannot read owners", id.PID)
	}
	meta, err := identities.ProcessIdentity(id.PID)
	if IsGone(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("probe pid %d: %w", id.PID, err)
	}
	if meta.Zombie {
		return false, nil
	}
	stat, err := door.Procs.Stat(id.PID)
	if IsGone(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("probe pid %d start time: %w", id.PID, err)
	}
	return stat.StartTime == id.Start, nil
}

// Escalation is a stop's schedule: Term is how long SIGTERM gets before
// SIGKILL, Kill how long the kernel gets to reap after it, Poll the cadence.
type Escalation struct {
	Term, Kill, Poll time.Duration
}

// Stopped is how a Stop ended.
type Stopped int

const (
	// StoppedGone: the process was gone before SIGTERM.
	StoppedGone Stopped = iota + 1
	// StoppedChanged: SIGTERM was withheld from a changed process.
	StoppedChanged
	// StoppedOnTerm: the process exited on SIGTERM.
	StoppedOnTerm
	// StoppedKilled: the process exited only on SIGKILL.
	StoppedKilled
)

// Stop ends target: SIGTERM, an identity-aware wait of esc.Term, then SIGKILL
// to the same identity (re-checked like the first signal) and a wait of
// esc.Kill. A process still alive after both is an error wrapping
// ErrStillRunning; one that changed identity before SIGKILL is an error too,
// since what it became is not what the stop was for.
func (door Door) Stop(ctx context.Context, target Target, esc Escalation) (Stopped, error) {
	result := door.StopAll(ctx, []Target{target}, esc)[0]
	return result.Stopped, result.Err
}

// StopResult is how one target of StopAll ended: Stopped when it ended or was
// withheld, Err when it could not be stopped.
type StopResult struct {
	Stopped Stopped
	Err     error
}

// StopAll is Stop over several targets on one schedule: SIGTERM to each, one
// shared wait of esc.Term, SIGKILL to each survivor's same identity, one wait
// of esc.Kill. Results line up with targets.
func (door Door) StopAll(ctx context.Context, targets []Target, esc Escalation) []StopResult {
	results := make([]StopResult, len(targets))
	var termed []int
	for index, target := range targets {
		outcome, err := door.Signal(target, syscall.SIGTERM)
		switch {
		case err != nil:
			results[index].Err = err
		case outcome == OutcomeGone:
			results[index].Stopped = StoppedGone
		case outcome == OutcomeChanged:
			results[index].Stopped = StoppedChanged
		default:
			termed = append(termed, index)
		}
	}
	survivors := door.awaitAllGone(ctx, targets, termed, esc.Term, esc.Poll, results)
	var killed []int
	for _, index := range termed {
		if results[index].Err != nil {
			continue
		}
		if !slices.Contains(survivors, index) {
			results[index].Stopped = StoppedOnTerm
			continue
		}
		outcome, err := door.Signal(targets[index], syscall.SIGKILL)
		switch {
		case err != nil:
			results[index].Err = fmt.Errorf("after SIGTERM and %s: %w", esc.Term, err)
		case outcome == OutcomeGone:
			results[index].Stopped = StoppedOnTerm
		case outcome == OutcomeChanged:
			results[index].Err = fmt.Errorf(
				"pid %d changed identity after SIGTERM; SIGKILL withheld",
				targets[index].PID,
			)
		default:
			killed = append(killed, index)
		}
	}
	survivors = door.awaitAllGone(ctx, targets, killed, esc.Kill, esc.Poll, results)
	for _, index := range killed {
		switch {
		case results[index].Err != nil:
		case slices.Contains(survivors, index):
			results[index].Err = fmt.Errorf("pid %d %w after SIGKILL", targets[index].PID, ErrStillRunning)
		default:
			results[index].Stopped = StoppedKilled
		}
	}
	return results
}

// awaitAllGone polls Alive for the targets at indices until every one is gone
// or within elapses, and returns the indices still alive. A probe that fails
// is recorded in that target's result, never read as gone.
func (door Door) awaitAllGone(
	ctx context.Context,
	targets []Target,
	indices []int,
	within, every time.Duration,
	results []StopResult,
) []int {
	if every <= 0 {
		every = 100 * time.Millisecond
	}
	deadline := door.Clock.Now().Add(within)
	waiting := slices.Clone(indices)
	for {
		alive := waiting[:0]
		for _, index := range waiting {
			running, err := door.Alive(targets[index].Identity)
			switch {
			case err != nil:
				results[index].Err = err
			case running:
				alive = append(alive, index)
			}
		}
		waiting = alive
		if len(waiting) == 0 || !door.Clock.Now().Before(deadline) {
			return waiting
		}
		if err := door.Clock.Sleep(ctx, every); err != nil {
			for _, index := range waiting {
				results[index].Err = fmt.Errorf("wait for pid %d to exit: %w", targets[index].PID, err)
			}
			return waiting
		}
	}
}

// Install is the pfm install a stop of pfm-launched processes acts for: Mark
// is paths.InstallMark of its home, Binary its installed pfm.
type Install struct {
	Mark   string
	Binary string
}

// InstallAt is the install rooted at home: its mark and its installed pfm.
func InstallAt(home string) Install {
	return Install{Mark: paths.InstallMark(home), Binary: agentlauncher.ExecTarget(home)}
}

// Owns reports whether live was launched by this install: its environment
// carries this install's mark, or — launched before marks existed — carries
// none and executes this install's own binary. Another mark is another
// install (a test jail, another checkout's build), never ours.
func (install Install) Owns(procs ProcFS, live Identity) (bool, error) {
	if install.Mark == "" || install.Binary == "" {
		return false, errors.New("install ownership: no install mark or binary to compare")
	}
	environment, err := procs.Environ(live.PID)
	if err != nil {
		return false, fmt.Errorf("read pid %d environment for its install mark: %w", live.PID, err)
	}
	if _, marked := environment[paths.EnvInstallMark]; marked {
		return install.OwnsEnvironment(environment, ""), nil
	}
	executable := live.Executable
	if executable == "" {
		executables, ok := procs.(ProcExecutable)
		if !ok {
			return false, fmt.Errorf(
				"pid %d carries no install mark and the table cannot read its executable",
				live.PID,
			)
		}
		if executable, err = executables.Executable(live.PID); err != nil {
			return false, fmt.Errorf("pid %d carries no install mark: read its executable: %w", live.PID, err)
		}
	}
	return install.OwnsEnvironment(environment, executable), nil
}

// OwnsEnvironment is Owns' rule over a process's environment and executable
// already read: this install's mark, or no mark and this install's binary.
func (install Install) OwnsEnvironment(environment map[string]string, executable string) bool {
	if install.Mark == "" || install.Binary == "" {
		return false
	}
	if mark, marked := environment[paths.EnvInstallMark]; marked {
		return mark == install.Mark
	}
	return executable != "" && paths.PhysicalPath(executable) == paths.PhysicalPath(install.Binary)
}
