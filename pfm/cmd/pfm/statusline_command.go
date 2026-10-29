package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/hookentry"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/reload"
	"github.com/rezzminator/professor/pfm/internal/statusline"
	"github.com/rezzminator/professor/pfm/internal/usagehook"
)

var statuslineCodexOptions = func() statusline.CodexOptions {
	return statusline.CodexOptions{}
}

func runStatuslineWithRuntime(
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	machine commandRuntime,
	env paths.Env,
) int {
	env = defaultEnv(env)
	flags := cli.NewFlagSet("statusline", "usage: pfm statusline [--refresh-gpt | --subagents]", stderr)
	refreshCodex := flags.Bool("refresh-gpt", false, "refresh the GPT usage cache")
	subagents := flags.Bool("subagents", false, "render agent-panel row bodies from subagentStatusLine JSON")
	pacedKey := flags.String("paced", "", "internal: run the paced render worker of one chat key")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	modes := 0
	for _, set := range []bool{*refreshCodex, *subagents, *pacedKey != ""} {
		if set {
			modes++
		}
	}
	if flags.NArg() != 0 || modes > 1 {
		flags.Usage()
		return 2
	}
	if *subagents {
		return statusline.ServeSubagents(stdin, stdout, stderr, machine.Paths, env, machine.Config.Path)
	}

	ctx := context.Background()
	if *refreshCodex {
		options := statuslineCodexOptions()
		account := reload.CodexHomeAccount(machine.Config, env.Get("CODEX_HOME"))
		options.Binary = machine.Config.EffectiveCodex(account).Binary
		if err := statusline.RefreshCodex(ctx, options); err != nil {
			fmt.Fprintf(stderr, "pfm statusline: refresh GPT cache: %v\n", err)
			return 1
		}
		return 0
	}

	paced := statusline.Paced{
		CacheDir: filepath.Dir(statusline.CodexStatuslineCachePath(env.Get(paths.EnvHome), os.Getuid())),
		Render: func(raw []byte) (string, error) {
			runtime, err := statuslineRuntime(machine, env, stderr)
			if err != nil {
				return "", err
			}
			return statusline.Render(ctx, raw, runtime)
		},
		Spawn: statuslinePacedSpawn,
		Warn:  stderr,
	}
	if *pacedKey != "" {
		return runPacedStatuslineWorker(paced, *pacedKey, machine.Config.Path, stderr)
	}
	raw, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil {
		fmt.Fprintf(stderr, "pfm statusline: read input (fail-open): %v\n", err)
		return 0
	}
	statusline.RecordSession(raw, machine.Paths.SIDDir, stderr)
	paced.Key = statusline.PacedKey(raw, env.Get("COLUMNS"))
	paced.Front(raw, stdout)
	return 0
}

// statuslinePacedSpawn starts a chat's paced render worker detached; tests
// replace it, since the test binary is no pfm to start.
var statuslinePacedSpawn = func(key string) error {
	return statusline.SpawnDetached(statusline.RefreshKindPaced, obs.Runner(deps.RealRunner{}), key)
}

// runPacedStatuslineWorker is `pfm statusline --paced KEY`, started detached by
// a front. It ends when its executable or the machine config is replaced, so
// the next front starts one that renders with the new binary and config.
func runPacedStatuslineWorker(paced statusline.Paced, key, configPath string, stderr io.Writer) int {
	executable, err := os.Executable()
	if err == nil {
		executable, err = filepath.EvalSymlinks(executable)
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm statusline: paced worker: resolve pfm executable: %v\n", err)
		return 1
	}
	paced.Key = key
	paced.Watch = []string{executable, configPath}
	if err := paced.Work(); err != nil {
		fmt.Fprintf(stderr, "pfm statusline: paced worker: %v\n", err)
		return 1
	}
	return 0
}

// statuslineRuntime is the render runtime of one main-statusline render, the
// front's inline render and the paced worker's alike.
func statuslineRuntime(machine commandRuntime, env paths.Env, stderr io.Writer) (statusline.Runtime, error) {
	const statuslineHostEngine = pfmengine.Claude // pfm statusline is launched only by Claude Code's statusline hook; an environment that names no engine is that hook's
	id, engineErr := statusline.EngineFromEnvironment(env.Get)
	if errors.Is(engineErr, statusline.ErrNoEngineInEnvironment) {
		id = statuslineHostEngine
	} else if engineErr != nil {
		return statusline.Runtime{}, fmt.Errorf("resolve engine: %w", engineErr)
	}
	runtime, err := statusline.DefaultRuntime(id)
	if err != nil {
		return statusline.Runtime{}, fmt.Errorf("resolve runtime: %w", err)
	}
	runtime.UseConfig(machine, stderr)
	if runtime.Engine == pfmengine.Codex {
		runtime.AccountDirs = make(map[string]int, len(machine.Config.CodexAccounts))
		runtime.AccountEmojis = make(map[int]string, len(machine.Config.CodexAccounts))
		for _, account := range machine.Config.CodexAccounts {
			runtime.AccountDirs[canonicalAccountPath(account.Home)] = account.ID
			runtime.AccountEmojis[account.ID] = machine.Config.CodexEmojiFor(account.ID)
		}
	} else {
		runtime.AccountDirs = make(map[string]int, len(machine.Config.Accounts))
		runtime.AccountEmojis = make(map[int]string, len(machine.Config.Accounts))
		for _, account := range machine.Config.Accounts {
			runtime.AccountDirs[canonicalAccountPath(account.ConfigDir)] = account.ID
			runtime.AccountEmojis[account.ID] = machine.Config.EmojiFor(account.ID)
		}
	}
	runtime.Spawn = func(kind statusline.RefreshKind) error {
		return statusline.SpawnDetached(kind, obs.Runner(deps.RealRunner{}))
	}
	return runtime, nil
}

func canonicalAccountPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	if absolute, err := filepath.Abs(path); err == nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(path)
}

func runUsageHookWithRuntime(
	args []string,
	stdout, stderr io.Writer,
	runtime commandRuntime,
	env paths.Env,
) int {
	env = defaultEnv(env)
	flags := cli.NewFlagSet("usage-hook", "usage: pfm usage-hook", stderr)
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	id, _ := statusline.EngineFromEnvironment(env.Get)
	if id == pfmengine.Codex {
		return 0
	}
	accountDirs := make(map[string]int, len(runtime.Config.Accounts))
	for _, account := range runtime.Config.Accounts {
		accountDirs[account.ConfigDir] = account.ID
	}
	return hookentry.UsageHook(stdout, stderr, usagehook.Options{
		AccountDirs:  accountDirs,
		Version:      runtime.Version,
		ClaudeBinary: runtime.Config.Claude.Binary,
	})
}
