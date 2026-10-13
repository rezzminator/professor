package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/enginesync"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

// runCodex forwards `pfm codex ARGS…` to `engine-sync codex ARGS…` for the one
// release the forwarders live: same arguments, same output, same exit code,
// plus one stderr line naming the engine-sync command. engine-sync is the
// single writer of the Codex mirror and the global roles.
func runCodex(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	return forwardEngineSync(enginesync.EngineCodex, args, stdout, stderr, runtime)
}

// forwardEngineSync runs engine-sync under this process's resolved home, so a
// forwarded --home default means what pfm's own default meant.
func forwardEngineSync(engine string, args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if engine == enginesync.EngineCodex && len(args) > 0 && args[0] == "agents" {
		var err error
		args, err = forwardAgentsSource(args, runtime.Paths.Home)
		if err != nil {
			fmt.Fprintf(stderr, "pfm codex agents: %v\n", err)
			return 1
		}
	}
	args = enginesync.CapabilityArgs(engine, args, enginesync.HostCapabilities(runtime.Config.Claude.GptAgents))
	runner := enginesync.Runner{
		Home:     runtime.Paths.Home,
		ChildEnv: append(os.Environ(), "HOME="+runtime.Paths.Home),
	}
	return runner.Forward(context.Background(), "pfm "+engine, append([]string{engine}, args...), stdout, stderr)
}

// forwardAgentsSource preserves pfm's clone source when the caller omits it.
func forwardAgentsSource(args []string, home string) ([]string, error) {
	// engine-sync accepts flags before or after positionals; a sentinel ends parsing.
	for index := 1; index < len(args); index++ {
		arg := args[index]
		if arg == "--" {
			break
		}
		if !strings.HasPrefix(arg, "-") {
			continue
		}
		name, value, hasValue := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		switch name {
		case "source-repo", "help", "h":
			return args, nil
		case "home":
			if !hasValue {
				index++
				if index == len(args) {
					return args, nil
				}
				value = args[index]
			}
			home = value
		}
	}
	if home == "" {
		return args, nil
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return nil, err
	}
	source, cloned, err := installer.RecordedGlobalClone(home)
	if err != nil {
		return nil, err
	}
	if !cloned {
		return args, nil
	}
	split := len(args)
	for index, value := range args {
		if value == "--" {
			split = index
			break
		}
	}
	forwarded := append([]string(nil), args[:split]...)
	forwarded = append(forwarded, "--source-repo", source)
	return append(forwarded, args[split:]...), nil
}
