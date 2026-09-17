package statusline

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"hostops/pfm/internal/deps"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
)

// RefreshKind names one detached cache refresher the render path may arm.
type RefreshKind string

const (
	RefreshKindCodex RefreshKind = "gpt"
)

// CommandRunner is the small read-only command seam used by the git segment.
type CommandRunner interface {
	Output(context.Context, string, ...string) ([]byte, error)
}

type commandRunner struct{}

func (commandRunner) Output(
	ctx context.Context,
	name string,
	args ...string,
) ([]byte, error) {
	command := exec.CommandContext(ctx, deps.Executable(name), args...)
	return command.Output()
}

// Runtime holds every environmental input to a render. Tests replace all of
// them, so no fixture reads a live socket, account, cache, process tree, or
// repository.
type Runtime struct {
	Now func() time.Time

	Home          string
	ConfigDir     string
	CacheDir      string
	RateLimitDir  string
	SIDDir        string
	TmuxDir       string
	ProcRoot      string
	Columns       int
	UID           int
	AccountDirs   map[string]int
	AccountEmojis map[int]string
	Engine        pfmengine.ID

	// A non-nil Env is a closed test environment. Nil reads os.Getenv.
	Env map[string]string

	Command CommandRunner
	Spawn   func(RefreshKind) error
}

func DefaultRuntime(id pfmengine.ID) (Runtime, error) {
	resolved, err := paths.Resolve()
	if err != nil {
		return Runtime{}, err
	}
	columns, _ := strconv.Atoi(os.Getenv("COLUMNS"))
	descriptor := pfmengine.MustLookup(id)
	configDir := os.Getenv(pfmengine.MustLookup(pfmengine.Claude).HomeEnv)
	if id == pfmengine.Codex {
		configDir = os.Getenv(descriptor.HomeEnv)
		if configDir == "" {
			configDir = resolved.FirstRoot(pfmengine.Codex)
		}
	} else if configDir == "" {
		configDir = filepath.Join(resolved.Home, ".claude")
	}
	cacheDir := filepath.Dir(CodexStatuslineCachePath(os.Getenv(paths.EnvHome), os.Getuid()))
	rateDir := ClaudeRateLimitDir(os.Getenv(paths.EnvHome), os.Getuid())
	return Runtime{
		Now:          time.Now,
		Home:         resolved.Home,
		ConfigDir:    configDir,
		CacheDir:     cacheDir,
		RateLimitDir: rateDir,
		SIDDir:       resolved.SIDDir,
		TmuxDir:      resolved.TmuxDir,
		ProcRoot:     resolved.ProcRoot,
		Columns:      columns,
		UID:          os.Getuid(),
		Engine:       id,
		Command:      commandRunner{},
	}, nil
}

// EngineFromEnvironment resolves the current seat, not merely its parent.
// A live Codex thread is authoritative even when a Codex-launched Claude child
// inherited CODEX_HOME; an explicit Claude config otherwise wins that tie.
var ErrNoEngineInEnvironment = errors.New("no engine in environment")

func EngineFromEnvironment(getenv func(string) string) (pfmengine.ID, error) {
	for _, id := range pfmengine.All() {
		d := pfmengine.MustLookup(id)
		if d.SessionEnv != "" && strings.TrimSpace(getenv(d.SessionEnv)) != "" {
			return id, nil
		}
	}
	for _, id := range pfmengine.All() {
		d := pfmengine.MustLookup(id)
		if d.HomeEnv != "" && strings.TrimSpace(getenv(d.HomeEnv)) != "" {
			return id, nil
		}
	}
	return "", ErrNoEngineInEnvironment
}

// CodexStatuslineCachePath is the one filesystem rule for the Codex App Server limits
// cache. Production uses the host temp directory; a PFM_HOME jail keeps the
// cache inside that jail. The cc-gpt-usage name is a legacy on-disk contract.
func CodexStatuslineCachePath(jailHome string, uid int) string {
	cacheDir := os.TempDir()
	if jailHome != "" {
		cacheDir = filepath.Join(jailHome, "tmp")
	}
	return filepath.Join(cacheDir, "cc-gpt-usage-"+strconv.Itoa(uid)+".json")
}

// ClaudeRateLimitDir is the one filesystem rule for provider-confirmed Claude
// windows harvested from statusline input. Limits readers use the same path so
// the statusline writer remains the single owner of this cache.
func ClaudeRateLimitDir(jailHome string, uid int) string {
	return filepath.Join(filepath.Dir(CodexStatuslineCachePath(jailHome, uid)), "cc-rate-limits")
}

func (runtime Runtime) getenv(name string) string {
	if runtime.Env != nil {
		return runtime.Env[name]
	}
	return os.Getenv(name)
}

func (runtime Runtime) now() time.Time {
	if runtime.Now != nil {
		return runtime.Now()
	}
	return time.Now()
}

func (runtime Runtime) normalized() Runtime {
	if runtime.Engine == "" {
		runtime.Engine = pfmengine.Claude
	}
	if runtime.Home == "" {
		runtime.Home, _ = os.UserHomeDir()
	}
	if runtime.ConfigDir == "" {
		runtime.ConfigDir = filepath.Join(runtime.Home, ".claude")
	}
	if runtime.CacheDir == "" {
		runtime.CacheDir = os.TempDir()
	}
	if runtime.RateLimitDir == "" {
		runtime.RateLimitDir = filepath.Join(runtime.CacheDir, "cc-rate-limits")
	}
	if runtime.SIDDir == "" {
		runtime.SIDDir = filepath.Join(runtime.CacheDir, "cc-sid")
	}
	if runtime.TmuxDir == "" {
		runtime.TmuxDir = filepath.Join(runtime.CacheDir, "tmux-"+strconv.Itoa(runtime.UID))
	}
	if runtime.ProcRoot == "" {
		runtime.ProcRoot = "/proc"
	}
	if runtime.Command == nil {
		runtime.Command = commandRunner{}
	}
	return runtime
}
