//go:build linux

package spawn

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/obs"
	"hostops/pfm/internal/paths"
)

// serviceScopeCommand escapes a chat server from the user service that
// happened to request it. INVOCATION_ID is systemd's per-invocation marker;
// descendants inherit it, so its presence is exactly the mortal cgroup path
// this spawn must leave. Outside a service the ordinary direct launch stays
// unchanged.
func serviceScopeCommand(
	ctx context.Context,
	binary string,
	arguments, environment []string,
	env paths.Env,
) (*exec.Cmd, error) {
	if strings.TrimSpace(env.Get("INVOCATION_ID")) == "" {
		command := exec.CommandContext(ctx, binary, arguments...)
		command.Env = environment
		return command, nil
	}
	systemdRun, err := obs.Runner(deps.RealRunner{}).LookPath("systemd-run")
	if err != nil {
		return nil, fmt.Errorf(
			"running inside a systemd user service requires systemd-run to create a durable chat scope: %w",
			err,
		)
	}
	scopeArguments := []string{"--user", "--collect", "--scope", "--", binary}
	scopeArguments = append(scopeArguments, arguments...)
	command := exec.CommandContext(ctx, systemdRun, scopeArguments...)
	// The scope is no longer the requesting service. Do not let its chat
	// inherit the service's invocation marker and falsely classify every
	// nested spawn as another service-owned process.
	command.Env = withoutEnvironment(environment, "INVOCATION_ID")
	return command, nil
}

func withoutEnvironment(environment []string, key string) []string {
	prefix := key + "="
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
