package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/cli"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/statusline"
	"github.com/rezzminator/professor/pfm/internal/usagehook"
)

var statuslineCodexOptions = func() statusline.CodexOptions {
	return statusline.CodexOptions{}
}

func runStatusline(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		fmt.Fprintf(stderr, "pfm statusline: load config (fail-open): %v\n", err)
		return 0
	}
	return runStatuslineWithRuntime(args, stdin, stdout, stderr, runtime, paths.OSEnv{})
}

func runStatuslineWithRuntime(
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	machine commandRuntime,
	env paths.Env,
) int {
	env = defaultEnv(env)
	const statuslineHostEngine = pfmengine.Claude // pfm statusline is launched only by Claude Code's statusline hook; an environment that names no engine is that hook's
	flags := cli.NewFlagSet("statusline", "usage: pfm statusline [--refresh-gpt | --subagents]", stderr)
	refreshCodex := flags.Bool("refresh-gpt", false, "refresh the GPT usage cache")
	subagents := flags.Bool("subagents", false, "render agent-panel row bodies from subagentStatusLine JSON")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 || (*refreshCodex && *subagents) {
		flags.Usage()
		return 2
	}
	if *subagents {
		return statusline.ServeSubagents(stdin, stdout, stderr, machine.Paths.SIDDir)
	}

	ctx := context.Background()
	if *refreshCodex {
		options := statuslineCodexOptions()
		account := accountForCodexHome(machine.Config, env.Get("CODEX_HOME"))
		options.Binary = machine.Config.EffectiveCodex(account).Binary
		if err := statusline.RefreshCodex(ctx, options); err != nil {
			fmt.Fprintf(stderr, "pfm statusline: refresh GPT cache: %v\n", err)
			return 1
		}
		return 0
	}

	raw, err := io.ReadAll(io.LimitReader(stdin, 1<<20))
	if err != nil {
		fmt.Fprintf(stderr, "pfm statusline: read input (fail-open): %v\n", err)
		return 0
	}
	statusline.RecordSession(raw, machine.Paths.SIDDir, stderr)
	id, engineErr := statusline.EngineFromEnvironment(env.Get)
	if errors.Is(engineErr, statusline.ErrNoEngineInEnvironment) {
		id = statuslineHostEngine
	} else if engineErr != nil {
		fmt.Fprintf(stderr, "pfm statusline: resolve engine (fail-open): %v\n", engineErr)
		return 0
	}
	runtime, err := statusline.DefaultRuntime(id)
	if err != nil {
		fmt.Fprintf(stderr, "pfm statusline: resolve runtime (fail-open): %v\n", err)
		return 0
	}
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
	rendered, err := statusline.Render(ctx, raw, runtime)
	if err != nil {
		fmt.Fprintf(stderr, "pfm statusline: render (fail-open): %v\n", err)
		return 0
	}
	if _, err := io.WriteString(stdout, rendered); err != nil {
		fmt.Fprintf(stderr, "pfm statusline: write output (fail-open): %v\n", err)
	}
	return 0
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

func runUsageHook(args []string, stdout, stderr io.Writer) int {
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		fmt.Fprintf(stderr, "pfm usage-hook: load config (fail-open): %v\n", err)
		return 0
	}
	return runUsageHookWithRuntime(args, stdout, stderr, runtime, paths.OSEnv{})
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
	message, err := usagehook.Evaluate(context.Background(), usagehook.Options{
		AccountDirs: accountDirs,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm usage-hook: evaluate (fail-open): %v\n", err)
		return 0
	}
	if message != "" {
		if _, err := io.WriteString(stdout, message); err != nil {
			fmt.Fprintf(stderr, "pfm usage-hook: write output (fail-open): %v\n", err)
		}
	}
	return 0
}
