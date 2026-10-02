package installer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

// layoutHolderProbeTimeout bounds the service PID probes the gate runs while
// it judges a held database.
const layoutHolderProbeTimeout = 10 * time.Second

// layoutAncestryDepth bounds the parent-PID walk from a database holder to a
// service; a real process tree is far shallower.
const layoutAncestryDepth = 64

// nameSyncServiceUnit is the oneshot unit the name-sync path and timer start;
// its process opens the state database while it runs.
const nameSyncServiceUnit = "pfm-name-sync.service"

// layoutServicePIDs returns the main PID of each pfm service running now,
// keyed by PID to its label or unit: the processes stopLayoutServices stops —
// on systemd the MCP service, and the name-sync service its stopped path and
// timer units start (a path or timer unit runs no process of its own). It
// only reads.
// A service that is not loaded or not running has no PID; an unreachable
// systemd user manager runs no service. A probe that cannot answer is an
// error, never "no service".
func layoutServicePIDs(ctx context.Context, env LayoutEnv) (map[int]string, error) {
	runner := env.commandRunner()
	reader, ok := runner.(OutputRunner)
	if !ok {
		return nil, errors.New("service PID probe: command runner cannot read output")
	}
	if schedulerIsLaunchd {
		return launchdServicePIDs(ctx, reader)
	}
	services := map[int]string{}
	if layoutSystemctl(ctx, runner, "show-environment") != nil {
		return services, nil
	}
	for _, unit := range []string{mcpUnitName, nameSyncServiceUnit} {
		args := []string{systemctlUser, "show", "--property=MainPID", "--value", unit}
		output, err := reader.Output(ctx, "systemctl", args...)
		if err != nil {
			return nil, fmt.Errorf("systemctl %s: %w", strings.Join(args, " "), err)
		}
		value := strings.TrimSpace(string(output))
		pid, err := strconv.Atoi(value)
		if err != nil || pid < 0 {
			return nil, fmt.Errorf("systemctl %s: MainPID %q is not a PID", strings.Join(args, " "), value)
		}
		if pid > 0 {
			services[pid] = unit
		}
	}
	return services, nil
}

// launchdServicePIDs is layoutServicePIDs on launchd: each pfm launch agent's
// running PID, keyed to its label.
func launchdServicePIDs(ctx context.Context, reader OutputRunner) (map[int]string, error) {
	services := map[int]string{}
	for _, label := range []string{mcpLaunchdLabel, launchdLabel} {
		service := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
		output, err := reader.Output(ctx, "launchctl", "print", service)
		loaded, err := launchctlPrintLoaded(service, err)
		if err != nil {
			return nil, err
		}
		if !loaded {
			continue
		}
		pid, err := launchctlPrintPID(string(output))
		if err != nil {
			return nil, fmt.Errorf("launchctl print %s: %w", service, err)
		}
		if pid > 0 {
			services[pid] = label
		}
	}
	return services, nil
}

// launchctlPrintPID reads the job's own `pid = N` line from `launchctl
// print`; a job that is loaded but not running prints none (0).
func launchctlPrintPID(output string) (int, error) {
	for _, line := range strings.Split(output, "\n") {
		value, ok := strings.CutPrefix(strings.TrimSpace(line), "pid = ")
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, fmt.Errorf("pid line %q is not a PID", line)
		}
		return pid, nil
	}
	return 0, nil
}

// judgeHeldDB judges a legacy database row with holders: service-owned
// holders keep today's "held by pid" refusal marked serviceHeld, which the
// gate lets through because apply stops those services and rescans before
// any write; any other holder is a refusal naming it; a probe that cannot
// answer is the finding's error.
func judgeHeldDB(env LayoutEnv, finding *LayoutFinding, holders []string) {
	detail, err := judgeDBHolders(env, holders)
	switch {
	case err != nil:
		finding.Err = err
	case detail != "":
		finding.Verdict, finding.Detail = VerdictRefuse, detail
	default:
		finding.Verdict, finding.Detail = VerdictRefuse, "held by pid "+strings.Join(holders, ",")
		finding.serviceHeld = true
	}
}

// judgeSessionOwnership refuses a session merge the invoking user cannot
// carry out, and makes a walk that fails the finding's error.
func judgeSessionOwnership(env LayoutEnv, finding *LayoutFinding, dir string) {
	refusal, err := unmovableSessionEntries(env, dir)
	if err != nil {
		finding.Err = err
	} else if refusal != "" {
		finding.Verdict, finding.Detail = VerdictRefuse, refusal
	}
}

// judgeDBHolders splits a legacy database's holders into those a stopped pfm
// service takes with it (the service's main process or a descendant of it)
// and the rest; a holder that exited before its parent walk holds nothing and
// is dropped. It returns "" when no other holder is left — apply stops those
// services and rescans before it moves the database — and otherwise the
// refusal detail naming each other holder. It only reads.
func judgeDBHolders(env LayoutEnv, holders []string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), layoutHolderProbeTimeout)
	defer cancel()
	services, err := layoutServicePIDs(ctx, env)
	if err != nil {
		return "", fmt.Errorf("database holder %s: %w", strings.Join(holders, ","), err)
	}
	processes := gather.NewProcFS(env.ProcRoot)
	var others []string
	for _, holder := range holders {
		pid, err := strconv.Atoi(holder)
		if err != nil {
			return "", fmt.Errorf("database holder %q is not a PID", holder)
		}
		owner, err := serviceOwnedPID(processes, services, pid)
		if err != nil {
			return "", fmt.Errorf("database holder %d ancestry: %w", pid, err)
		}
		if owner != holderOther {
			continue
		}
		name := "not a pfm service"
		if argv, err := processes.Cmdline(pid); err == nil && len(argv) > 0 && argv[0] != "" {
			name = filepath.Base(argv[0])
		}
		others = append(others, fmt.Sprintf("pid %d (%s)", pid, name))
	}
	if len(others) == 0 {
		return "", nil
	}
	return "held by " + strings.Join(others, ", ") + " — close it", nil
}

// holderOwner is what the parent walk found a database holder to be.
type holderOwner int

const (
	// holderOther is a live holder no pfm service owns.
	holderOther holderOwner = iota
	// holderService is a service's main process or a descendant of one.
	holderService
	// holderGone exited before the walk read it, so it holds nothing.
	holderGone
)

// serviceOwnedPID walks holder's parents up to a service's main process. A
// holder the process table no longer has is holderGone; an ancestor gone
// mid-walk ends the walk holderOther, so the live holder refuses rather than
// passes. Only a live process whose stat cannot be read is an error.
func serviceOwnedPID(processes gather.ProcFS, services map[int]string, holder int) (holderOwner, error) {
	pid := holder
	for depth := 0; depth < layoutAncestryDepth && pid > 1; depth++ {
		if _, ok := services[pid]; ok {
			return holderService, nil
		}
		stat, err := processes.Stat(pid)
		if errors.Is(err, fs.ErrNotExist) {
			if pid == holder {
				return holderGone, nil
			}
			return holderOther, nil
		}
		if err != nil {
			return holderOther, err
		}
		if len(services) == 0 {
			return holderOther, nil
		}
		pid = stat.ParentPID
	}
	return holderOther, nil
}

// layoutInvokingUID is the uid a session merge moves files as; env.uid is the
// test seam.
func (env LayoutEnv) layoutInvokingUID() int {
	if env.uid != nil {
		return env.uid()
	}
	return os.Getuid()
}

// layoutRootRefusal refuses a merge run as root: sudo keeps HOME, so root
// would move another user's sessions and leave them root's.
const layoutRootRefusal = "run pfm install as your own user, not root"

// layoutAccess answers access(2) for the invoking user; env.access is the
// test seam.
func (env LayoutEnv) layoutAccess(path string, mode uint32) error {
	if env.access != nil {
		return env.access(path, mode)
	}
	return unix.Access(path, mode)
}

// unmovableSessionEntries walks one session entry directory a merge moves and
// deletes, and returns the refusal naming what the invoking user cannot do
// there, or "" when the merge can run. The journal first copies the whole
// tree, then the merge renames every entry out of its parent and removes each
// emptied directory, the directory itself included: every parent must be
// writable, every directory readable and writable, and every file readable.
// Who owns an entry does not matter — a rename needs its parent, not the
// entry. Root is refused, never handed a remedy naming root.
func unmovableSessionEntries(env LayoutEnv, dir string) (string, error) {
	uid := env.layoutInvokingUID()
	if uid == 0 {
		info, err := os.Lstat(dir)
		if err != nil {
			return "", fmt.Errorf("session entry owner: %w", err)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return "", fmt.Errorf("owner of %s unreadable", dir)
		}
		if stat.Uid != 0 {
			return layoutRootRefusal, nil
		}
	}
	count, example := 0, ""
	block := func(path string) {
		count++
		if example == "" {
			example = path
		}
	}
	parentBlocked := false
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				block(path)
				return nil
			}
			return err
		}
		blocked := env.layoutAccess(filepath.Dir(path), unix.W_OK|unix.X_OK) != nil
		if blocked && path == dir {
			parentBlocked = true
		}
		unreadable := false
		switch {
		case entry.IsDir():
			unreadable = env.layoutAccess(path, unix.R_OK|unix.X_OK) != nil
			blocked = blocked || unreadable || env.layoutAccess(path, unix.W_OK|unix.X_OK) != nil
		case entry.Type().IsRegular():
			blocked = blocked || env.layoutAccess(path, unix.R_OK) != nil
		}
		if blocked {
			block(path)
		}
		if unreadable {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("session entries: %w", err)
	}
	if count == 0 {
		return "", nil
	}
	if uid == 0 {
		return layoutRootRefusal, nil
	}
	name := strconv.Itoa(uid)
	if account, err := user.LookupId(name); err == nil && account.Username != "" {
		name = account.Username
	}
	target := dir
	if parentBlocked {
		target = filepath.Dir(dir)
	}
	quoted := shellCommandLine(target)
	return fmt.Sprintf("%d entries pfm cannot move as %s (e.g. %s) — run: sudo chown -R %s %s && chmod -R u+rwX %s",
		count, name, example, name, quoted, quoted), nil
}
