package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	pfmconfig "hostops/pfm/internal/config"
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
