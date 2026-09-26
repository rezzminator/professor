package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// commandRuntime is the process runtime every command branch consumes; the
// one shape lives in internal/config (Runtime, LoadRuntime).
type commandRuntime = pfmconfig.Runtime

// firstRuntime is a branch's optional trailing runtime as the pointer the
// package APIs take; nil hands each package its own default.
func firstRuntime(runtimes []commandRuntime) *commandRuntime {
	if len(runtimes) == 0 {
		return nil
	}
	return &runtimes[0]
}

// firstEnv is the same optional-trailing-argument shape as firstRuntime, for
// the host-environment seam (pfm/TESTPLAN.md § Seams, paths.Env): a caller's
// pinned environment, or the real process environment when none was given.
func firstEnv(envs []paths.Env) paths.Env {
	if len(envs) == 0 {
		return defaultEnv(nil)
	}
	return defaultEnv(envs[0])
}

// defaultEnv is firstEnv's shape for a single optional paths.Env parameter:
// a caller's pinned environment, or the real one when nil.
func defaultEnv(env paths.Env) paths.Env {
	if env == nil {
		return paths.OSEnv{}
	}
	return env
}

// defaultClock is defaultEnv's shape for the time seam (clock.Clock): a
// caller's pinned clock, or the real wall clock when nil.
func defaultClock(clk clock.Clock) clock.Clock {
	if clk == nil {
		return clock.Real
	}
	return clk
}

// splitGlobalConfig accepts the global flag only before the command. This is
// deliberate: internal subcommands already own flags named --config, and a
// global parser must never steal their arguments.
func splitGlobalConfig(args []string) (string, []string, error) {
	path := ""
	for len(args) > 0 {
		switch {
		case args[0] == "--config":
			if path != "" {
				return "", nil, errors.New("--config may be specified only once")
			}
			if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
				return "", nil, errors.New("--config requires a path")
			}
			path = args[1]
			args = args[2:]
		case strings.HasPrefix(args[0], "--config="):
			if path != "" {
				return "", nil, errors.New("--config may be specified only once")
			}
			path = strings.TrimPrefix(args[0], "--config=")
			if strings.TrimSpace(path) == "" {
				return "", nil, errors.New("--config requires a path")
			}
			args = args[1:]
		default:
			if path != "" && !filepath.IsAbs(path) {
				absolute, err := filepath.Abs(path)
				if err != nil {
					return "", nil, fmt.Errorf("resolve --config path %q: %w", path, err)
				}
				path = absolute
			}
			return path, args, nil
		}
	}
	return path, args, nil
}
