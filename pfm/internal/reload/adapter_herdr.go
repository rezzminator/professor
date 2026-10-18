package reload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// SeatForReload resolves only an exact durable native address, never a socket name.
func SeatForReload(ctx context.Context, values paths.Values, socket, pane string) (*fleetdb.Seat, error) {
	seats, err := fleetdb.ReadSeats(ctx, values)
	if err != nil {
		return nil, fmt.Errorf("read reload backend: %w", err)
	}
	for i := range seats {
		seat := &seats[i]
		if seat.Backend == spawn.BackendHerdr && seat.HerdrSocket == socket && seat.HerdrPane == pane {
			return seat, nil
		}
	}
	return nil, nil
}

// NativeTarget's handled result distinguishes native refusal from a tmux target.
// An unreadable store is returned, not rendered as an empty native roster.
func NativeTarget(
	ctx context.Context,
	values paths.Values,
	socket, pane string,
	env paths.Env,
) (*fleetdb.Seat, bool, error) {
	if env == nil {
		env = paths.OSEnv{}
	}
	ambient := socket == "" && env.Get("TMUX") == "" && env.Get("HERDR_ENV") == "1"
	if ambient {
		socket = env.Get("HERDR_SOCKET_PATH")
		if pane == "" {
			pane = env.Get("HERDR_PANE_ID")
		}
		if socket == "" || pane == "" {
			return nil, true, errors.New("native socket and pane are required")
		}
	}
	if socket == "" {
		return nil, false, nil
	}
	if !filepath.IsAbs(socket) {
		socket = filepath.Join(values.TmuxDir, socket)
	}
	seats, err := fleetdb.ReadSeats(ctx, values)
	if err != nil {
		return nil, ambient, fmt.Errorf("read native seats: %w", err)
	}
	var chosen *fleetdb.Seat
	handled := ambient
	for i := range seats {
		seat := &seats[i]
		if seat.Backend != spawn.BackendHerdr || seat.HerdrSocket != socket {
			continue
		}
		handled = true
		if pane != "" && pane != seat.HerdrPane {
			continue
		}
		if chosen != nil {
			return nil, true, errors.New("native socket has multiple panes — specify --pane")
		}
		chosen = seat
	}
	if chosen == nil && handled {
		return nil, true, fmt.Errorf("no native seat for %s %s", socket, pane)
	}
	return chosen, handled, nil
}

// notifyRetryDelays are the waits before each retry of a failure notification
// Herdr refused as rate_limited.
var notifyRetryDelays = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}

// NotifyWorkerFailure uses the native backend surface, including config failures.
// On a backend lookup failure the caller must not fall through to tmux.
// A native worker whose seat row is already retired (the chat ended) still
// notifies its Herdr server: its socket is never a tmux server to display on.
func NotifyWorkerFailure(
	ctx context.Context,
	values paths.Values,
	socket, pane, message string,
	native bool,
) (bool, error) {
	seat, err := SeatForReload(ctx, values, socket, pane)
	if err != nil {
		return true, err
	}
	if seat == nil && !native {
		return false, nil
	}
	for attempt := 0; ; attempt++ {
		body, err := herdr.SeatRequest(
			ctx,
			socket,
			"notification.show",
			map[string]string{"title": "pfm chat reload failed", "body": message},
		)
		if err != nil {
			return true, fmt.Errorf("native reload failure notification: %w", err)
		}
		err = herdr.ValidateNotificationShown(body)
		if err == nil {
			return true, nil
		}
		var notShown *herdr.NotificationNotShownError
		if !errors.As(err, &notShown) || notShown.Reason != herdr.NotificationRateLimited {
			return true, fmt.Errorf("native reload failure notification: %w", err)
		}
		if attempt == len(notifyRetryDelays) {
			return true, fmt.Errorf("native reload failure notification, %d attempts: %w", attempt+1, err)
		}
		if waitErr := clock.Real.Sleep(ctx, notifyRetryDelays[attempt]); waitErr != nil {
			return true, fmt.Errorf("native reload failure notification: %w (retry cancelled: %w)", err, waitErr)
		}
	}
}

// EngineForTarget takes native identity from its durable seat, never its socket name.
func EngineForTarget(ctx context.Context, values paths.Values, socket, pane string) (pfmengine.ID, error) {
	seat, err := SeatForReload(ctx, values, socket, pane)
	if err != nil {
		return "", err
	}
	if seat != nil {
		return pfmengine.ID(seat.Engine), nil
	}
	return EngineOf(socket), nil
}

// WorkerArguments carries the validated CLI values to a backend-specific request.
type WorkerArguments struct {
	Account, Cache, Socket, Pane, Then, Model, Effort string
	New, Hide                                         bool
}

// ParseWorkerArguments decodes values after the CLI's product validation.
func ParseWorkerArguments(args []string) (WorkerArguments, error) {
	var parsed WorkerArguments
	values := map[string]*string{
		"--account": &parsed.Account,
		"--cache":   &parsed.Cache,
		"--sock":    &parsed.Socket,
		"--pane":    &parsed.Pane,
		"--then":    &parsed.Then,
		"--model":   &parsed.Model,
		"--effort":  &parsed.Effort,
	}
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--new":
			parsed.New = true
		case "--hide":
			parsed.Hide = true
		default:
			if dest, ok := values[args[index]]; ok {
				if index+1 >= len(args) {
					return parsed, fmt.Errorf("%s needs a value", args[index])
				}
				index++
				*dest = args[index]
			} else {
				parsed.Account = args[index]
			}
		}
	}
	return parsed, nil
}

// NativeRequest constructs a typed native request without tmux identity inference.
func NativeRequest(runtime pfmconfig.Runtime, seat fleetdb.Seat, args WorkerArguments) (Request, error) {
	engine := pfmengine.ID(seat.Engine)
	account := runtime.Config.PrimaryAccountFor(engine, runtime.Config.ImplicitAccount())
	if args.Account != "" {
		var err error
		account, err = strconv.Atoi(args.Account)
		if err != nil {
			return Request{}, fmt.Errorf("native account %q: %w", args.Account, err)
		}
	}
	selected, err := ValidateAccount(runtime.Config, engine, account)
	if err != nil {
		return Request{}, err
	}
	return Request{
		Engine:       engine,
		SocketPath:   seat.HerdrSocket,
		Pane:         seat.HerdrPane,
		SessionID:    seat.SessionID,
		LeftBehind:   seat.SessionID,
		New:          args.New,
		AccountGiven: args.Account != "",
		CacheGiven:   args.Cache != "",
		Cache1H:      args.Cache == "1h",
		Account:      account,
		AccountIDs:   selected.IDs,
		CodexHome:    selected.CodexHome,
		CodexBinary:  selected.CodexBinary,
		CodexYolo:    selected.CodexYolo,
		Model:        args.Model,
		Effort:       args.Effort,
		Then:         args.Then,
		Home:         runtime.Paths.Home,
		Machine:      runtime.Config,
	}, nil
}

// OptionsForWorker retains the CLI's duration parser and common backend defaults.
func OptionsForWorker(values paths.Values, env paths.Env, duration func(string, int, paths.Env) time.Duration) Options {
	return Options{
		Home:        values.Home,
		SIDDir:      values.SIDDir,
		ClaudeRoots: values.Roots[pfmengine.Claude],
		Delay:       duration("PFM_RELOAD_DELAY_MS", 1500, env),
		Poll:        duration("PFM_RELOAD_POLL_MS", 1000, env),
		ExitTries: ParseIntEnv(
			paths.OSEnv{},
			"PFM_RELOAD_EXIT_TRIES",
			20,
		),
		IdleTries: ParseIntEnv(paths.OSEnv{}, "PFM_RELOAD_IDLE_TRIES", 120),
		ThenTries: ParseIntEnv(paths.OSEnv{}, "PFM_RELOAD_THEN_TRIES", 900),
	}
}

// NativeSchedule is a native seat's held record of its scheduled reload
// worker, {SIDDir}/reload-herdr-{seat}.pid holding "{pid} {start}". The
// scheduler holds its flock across reading the record, starting the worker
// and writing the record, so two /reloads typed close together start one
// worker; the worker publishes its own pending state only after the chat goes
// idle, too late to guard the second schedule.
type NativeSchedule struct {
	file *os.File
	proc gather.ProcFS
}

// ScheduledError refuses a /reload while the seat's recorded worker still runs.
type ScheduledError struct {
	PID int
	Log string
}

func (err *ScheduledError) Error() string {
	if err.PID == 0 {
		return fmt.Sprintf(
			"another /reload is scheduling this chat right now (log %s); try again once it has answered",
			err.Log,
		)
	}
	return fmt.Sprintf(
		"a reload is already scheduled for this chat (worker pid %d, log %s); it runs once the chat is idle",
		err.PID,
		err.Log,
	)
}

// LockWorkerSchedule returns the reload worker's log path. A tmux chat's log
// is named by its socket and has no schedule. A native seat's log is its own,
// since every seat on one Herdr server shares the socket, and its schedule
// comes back locked; a live recorded worker is refused as *ScheduledError.
// On any error nothing stays locked.
func LockWorkerSchedule(
	ctx context.Context,
	values paths.Values,
	socket, pane string,
) (string, *NativeSchedule, error) {
	logPath := filepath.Join(values.SIDDir, "reload-"+filepath.Base(socket)+".log")
	seat, err := SeatForReload(ctx, values, socket, pane)
	if err != nil || seat == nil {
		return logPath, nil, err
	}
	id := seat.SeatID
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\x00") {
		return logPath, nil, fmt.Errorf("native seat id %q cannot name a reload file", id)
	}
	logPath = filepath.Join(values.SIDDir, paths.SIDReloadHerdrPrefix+id+".log")
	path := filepath.Join(values.SIDDir, paths.SIDReloadHerdrPrefix+id+".pid")
	file, err := openSIDLeaf(values.SIDDir, filepath.Base(path), unix.O_RDWR)
	if err != nil {
		return logPath, nil, fmt.Errorf("open reload schedule record: %w", err)
	}
	if err := lockScheduleRecord(ctx, file); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) {
			return logPath, nil, errors.Join(&ScheduledError{Log: logPath}, file.Close())
		}
		return logPath, nil, errors.Join(fmt.Errorf("lock reload schedule record %s: %w", path, err), file.Close())
	}
	schedule := &NativeSchedule{file: file, proc: gather.NewProcFS(values.ProcRoot)}
	pid, err := schedule.scheduled()
	if err == nil && pid != 0 {
		err = &ScheduledError{PID: pid, Log: logPath}
	}
	if err != nil {
		return logPath, nil, errors.Join(err, schedule.release())
	}
	return logPath, schedule, nil
}

// scheduleLockWait bounds the wait for another scheduler's hold on the record
// (its read, start and record take milliseconds), well inside the 5 s budget
// of the /reload prompt hook.
var scheduleLockWait = 2 * time.Second

// maxScheduleRecord caps the record read: "{pid} {start}\n" is far shorter.
const maxScheduleRecord = 64

// lockScheduleRecord takes the record's flock, waiting at most
// scheduleLockWait; a hold that outlasts it returns EWOULDBLOCK.
func lockScheduleRecord(ctx context.Context, file *os.File) error {
	deadline := clock.Real.Now().Add(scheduleLockWait)
	for {
		err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if !errors.Is(err, unix.EWOULDBLOCK) || !clock.Real.Now().Before(deadline) {
			return err
		}
		if waitErr := clock.Real.Sleep(ctx, 20*time.Millisecond); waitErr != nil {
			return errors.Join(err, waitErr)
		}
	}
}

// OpenWorkerLog opens the reload worker's append-only log at logPath, a leaf
// of the SID directory, under openSIDLeaf's checks.
func OpenWorkerLog(logPath string) (*os.File, error) {
	return openSIDLeaf(filepath.Dir(logPath), filepath.Base(logPath), unix.O_WRONLY|unix.O_APPEND)
}

// openSIDLeaf opens name in dir, creating it 0600, only when dir is the
// user's own directory and not writable by group or others — the SID
// directory defaults to a shared /tmp path. The leaf is never a followed
// symlink, a blocking FIFO or a shared file: only a private single-link
// regular file of the user's is returned.
func openSIDLeaf(dir, name string, flags int) (_ *os.File, returnErr error) {
	directory, err := os.OpenFile(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open SID directory: %w", err)
	}
	defer func() {
		if err := directory.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close SID directory: %w", err))
		}
	}()
	info, err := directory.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat SID directory: %w", err)
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || !info.IsDir() || info.Mode().Perm()&0o022 != 0 {
		return nil, fmt.Errorf("SID directory %s is not a protected user directory", dir)
	}
	fd, err := unix.Openat(
		int(directory.Fd()),
		name,
		flags|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC,
		0o600,
	)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filepath.Join(dir, name), err)
	}
	file := os.NewFile(uintptr(fd), filepath.Join(dir, name))
	info, err = file.Stat()
	if err != nil {
		return nil, errors.Join(fmt.Errorf("stat %s: %w", file.Name(), err), file.Close())
	}
	owner, ok = info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 || !info.Mode().IsRegular() ||
		info.Mode().Perm()&0o077 != 0 {
		return nil, errors.Join(
			fmt.Errorf("%s is not a private single-link regular file", file.Name()),
			file.Close(),
		)
	}
	return file, nil
}

// scheduled returns the recorded worker's pid when that process still runs
// with its recorded start; 0 means no live worker (no record, a dead pid, or
// a pid now held by another process), so a new worker may start.
func (s *NativeSchedule) scheduled() (int, error) {
	if _, err := s.file.Seek(0, io.SeekStart); err != nil {
		return 0, fmt.Errorf("read reload schedule record: %w", err)
	}
	content, err := io.ReadAll(io.LimitReader(s.file, maxScheduleRecord))
	if err != nil {
		return 0, fmt.Errorf("read reload schedule record: %w", err)
	}
	fields := strings.Fields(string(content))
	if len(fields) != 2 {
		// Empty (never written) or torn by a crash mid-write: no worker is
		// provably scheduled, and the record is replaced.
		return 0, nil
	}
	pid, pidErr := strconv.Atoi(fields[0])
	start, startErr := strconv.ParseUint(fields[1], 10, 64)
	if pidErr != nil || startErr != nil || pid <= 0 {
		return 0, nil
	}
	stat, err := s.proc.Stat(pid)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read scheduled reload worker %d: %w", pid, err)
	}
	if stat.StartTime != start {
		return 0, nil
	}
	return pid, nil
}

// StartWorker starts the reload worker through start and records it on
// schedule as one commit span (deps.BeginCommit): a prompt hook's deadline
// applies before the start, and once the worker started its record is
// written whatever the deadline does, so the hook never exits between the
// two. A nil schedule (a tmux chat) records nothing.
func StartWorker(ctx context.Context, schedule *NativeSchedule, stderr io.Writer, start func() (int, error)) error {
	end, ok := deps.BeginCommit(ctx)
	if !ok {
		return errors.New("the prompt hook's deadline passed before the reload worker started; nothing was scheduled")
	}
	defer end()
	pid, err := start()
	if err != nil {
		return err
	}
	schedule.Record(pid, stderr)
	return nil
}

// Record writes the started worker's pid and start over the old record; a
// nil schedule (a tmux chat) records nothing. The worker runs either way, so
// a failure only loses the guard against a second schedule, and stderr says so.
func (s *NativeSchedule) Record(pid int, stderr io.Writer) {
	if s == nil {
		return
	}
	stat, err := s.proc.Stat(pid)
	if err == nil {
		err = s.file.Truncate(0)
	}
	if err == nil {
		_, err = s.file.WriteAt([]byte(fmt.Sprintf("%d %d\n", pid, stat.StartTime)), 0)
	}
	if err != nil {
		fmt.Fprintf(
			stderr,
			"pfm chat reload: a second /reload is not refused until this one runs: record worker %d: %v\n",
			pid,
			err,
		)
	}
}

// Close drops the lock, naming a failure on stderr; a nil schedule holds none.
func (s *NativeSchedule) Close(stderr io.Writer) {
	if s == nil {
		return
	}
	if err := s.release(); err != nil {
		fmt.Fprintf(stderr, "pfm chat reload: %v\n", err)
	}
}

// release closes the descriptor, which releases its flock.
func (s *NativeSchedule) release() error {
	if err := s.file.Close(); err != nil {
		return fmt.Errorf("close reload schedule record: %w", err)
	}
	return nil
}
