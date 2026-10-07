package reload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// procTree adapts the gathered process table to the tree walker. It holds ONE
// reader rather than building a fresh one per call: on macOS the reader carries
// a snapshot cache, and a per-call instance would resample the world each hop.
type procTree struct{ procfs gather.ProcFS }

// NewProcessTable is the live process table under procRoot, shaped for Run.
func NewProcessTable(procRoot string) Process {
	return procTree{procfs: gather.NewProcFS(procRoot)}
}

func (proc procTree) PIDs() ([]int, error) { return proc.procfs.PIDs() }
func (proc procTree) Cmdline(pid int) ([]string, error) {
	return proc.procfs.Cmdline(pid)
}

func (proc procTree) Environ(pid int) (map[string]string, error) {
	return proc.procfs.Environ(pid)
}

func (proc procTree) Stat(pid int) (gather.ProcStat, error) {
	return proc.procfs.Stat(pid)
}

// EngineOf names the engine a seat socket belongs to, "" when none claims it.
func EngineOf(socketPath string) pfmengine.ID {
	id, _ := pfmengine.FromSocket(filepath.Base(socketPath))
	return id
}

// BirthAccount reads the account and prompt-cache TTL the seat was born with: the
// launch record first (Claude), then the engine process inside the pane, then
// the caller's own environment. env must be non-nil.
func BirthAccount(
	resolved paths.Values,
	machine pfmconfig.Config,
	socketPath string,
	sessionID string,
	pane Pane,
	stderr io.Writer,
	env paths.Env,
) (int, bool, error) {
	engine := EngineOf(socketPath)
	if engine == pfmengine.OpenCode {
		return 0, false, errors.New("OpenCode does not support in-place reload")
	}
	ids := machine.AccountIDs()
	if engine == pfmengine.Codex {
		ids = machine.CodexAccountIDs()
	}
	if len(ids) == 0 {
		return 0, false, fmt.Errorf("no %s accounts configured", engineLabel(engine))
	}
	account := ids[0]
	cache := false
	if engine == pfmengine.Claude {
		cache = machine.EffectiveClaude(account).Cache1H
		launches, err := fleetdb.OpenLaunches(context.Background(), resolved)
		if err != nil {
			return 0, false, fmt.Errorf("read launch record for %s: %w", sessionID, err)
		}
		launch, readErr := launches.LaunchFor(context.Background(), sessionID)
		closeErr := launches.Close()
		if closeErr != nil {
			return 0, false, fmt.Errorf("read launch record for %s: close launch database: %w", sessionID, closeErr)
		}
		if readErr == nil {
			return launch.Account, launch.Cache1H, nil
		}
		if !errors.Is(readErr, fleetdb.ErrNoLaunch) {
			return 0, false, fmt.Errorf("read launch record for %s: %w", sessionID, readErr)
		}
	}
	proc := gather.NewProcFS(resolved.ProcRoot)
	tree := procTree{procfs: proc}
	matcher, err := gather.MatcherFor(engine)
	if err != nil {
		return 0, false, err
	}
	binary := machine.Claude.Binary
	if engine == pfmengine.Codex {
		binary = machine.Codex.Binary
	}
	pids, err := proc.PIDs()
	if err != nil {
		fmt.Fprintf(
			stderr,
			"pfm chat reload: inspect birth processes for %s: %v; using safe defaults\n",
			filepath.Base(socketPath),
			err,
		)
		return account, cache, nil
	}
	for _, pid := range pids {
		argv, err := proc.Cmdline(pid)
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat reload: inspect process %d command: %v\n", pid, err)
			continue
		}
		if !matcher.IsCommand(argv, binary) {
			continue
		}
		inPane, err := processInPane(tree, pid, pane.PID)
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat reload: inspect process %d ancestry: %v\n", pid, err)
			continue
		}
		if !inPane {
			continue
		}
		env, err := proc.Environ(pid)
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat reload: inspect process %d environment: %v\n", pid, err)
			continue
		}
		if engine == pfmengine.Codex {
			account = CodexHomeAccount(machine, env["CODEX_HOME"])
		} else {
			account = machine.AccountForConfigDir(env["CLAUDE_CONFIG_DIR"])
			cache = machine.EffectiveClaude(account).Cache1H
		}
		return account, cache, nil
	}
	// A tool shell can be detached from the seat's process tree. In that case
	// its own birth config is the only safe account rung for a cache-only reload;
	// the login default is no birth config, so it reads as unset.
	if engine == pfmengine.Codex {
		account = CodexHomeAccount(machine, env.Get("CODEX_HOME"))
	} else {
		birthDir := env.Get("CLAUDE_CONFIG_DIR")
		if claudelaunch.InheritedConfigDir(env.Get) {
			birthDir = ""
		}
		account = machine.AccountForConfigDir(birthDir)
		cache = machine.EffectiveClaude(account).Cache1H
	}
	return account, cache, nil
}

// CodexHomeAccount maps a CODEX_HOME to its configured account id, the first
// account when none matches, 0 when no Codex account is configured.
func CodexHomeAccount(machine pfmconfig.Config, home string) int {
	if len(machine.CodexAccounts) == 0 {
		return 0
	}
	cleaned := filepath.Clean(home)
	for _, account := range machine.CodexAccounts {
		if cleaned == filepath.Clean(account.Home) {
			return account.ID
		}
	}
	return machine.CodexAccounts[0].ID
}

// AccountSelection is the roster verdict a reload needs BEYOND the
// machine config it already carries: the Claude half is now the door's job, so
// only the roster and the Codex seat's own fields survive here.
type AccountSelection struct {
	IDs         []int
	CodexHome   string
	CodexBinary string
	CodexYolo   bool
}

// ValidateAccount checks account against the seat engine's own roster.
func ValidateAccount(machine pfmconfig.Config, engine pfmengine.ID, account int) (AccountSelection, error) {
	switch engine {
	case pfmengine.OpenCode:
		return AccountSelection{}, errors.New("OpenCode does not support in-place reload")
	case pfmengine.Codex:
		if len(machine.CodexAccounts) == 0 {
			return AccountSelection{}, errors.New("no Codex accounts configured")
		}
		selected, found := machine.CodexAccountByID(account)
		if !found {
			return AccountSelection{}, fmt.Errorf(
				"requested Codex account %d is not in the configured roster",
				account,
			)
		}
		if err := pfmconfig.CodexLoginError(selected.Home); err != nil {
			//nolint:staticcheck // Codex is the proper noun in the required login instruction.
			return AccountSelection{}, fmt.Errorf("Codex account %d: %w", account, err)
		}
		policy := machine.EffectiveCodex(account)
		return AccountSelection{
			IDs: machine.CodexAccountIDs(), CodexHome: selected.Home,
			CodexBinary: policy.Binary, CodexYolo: policy.Yolo,
		}, nil
	case pfmengine.Claude:
		// Continue below: Claude has the legacy account/config-dir policy.
	default:
		return AccountSelection{}, fmt.Errorf("unknown reload engine %q", engine)
	}
	if len(machine.Accounts) == 0 {
		return AccountSelection{}, errors.New("no Claude accounts configured")
	}
	if _, found := machine.Account(account); !found {
		return AccountSelection{}, fmt.Errorf(
			"requested Claude account %d is not in the configured roster",
			account,
		)
	}
	return AccountSelection{IDs: machine.AccountIDs()}, nil
}

func processInPane(proc Process, pid, panePID int) (bool, error) {
	current := pid
	for depth := 0; depth <= 4; depth++ {
		if current == panePID {
			return true, nil
		}
		stat, err := proc.Stat(current)
		if err != nil {
			return false, err
		}
		if stat.ParentPID <= 1 || stat.ParentPID == current {
			break
		}
		current = stat.ParentPID
	}
	return false, nil
}
