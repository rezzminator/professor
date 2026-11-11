package spawn

import (
	"context"
	"os/exec"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/servicemanager"
)

// chatServerCommand escapes a chat server from the user service that
// happened to request it: servicemanager.ScopeArgv decides, from env's
// INVOCATION_ID and kind, whether the launch goes through a transient scope.
// Outside a service the ordinary direct launch stays unchanged.
func chatServerCommand(
	ctx context.Context,
	kind servicemanager.Kind,
	lookPath func(string) (string, error),
	binary string,
	arguments, environment []string,
	env paths.Env,
) (*exec.Cmd, error) {
	argv := append([]string{binary}, arguments...)
	scoped, err := servicemanager.ScopeArgv(kind, env.Get, lookPath, argv)
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, scoped[0], scoped[1:]...)
	command.Env = environment
	if len(scoped) > len(argv) {
		// The scope is no longer the requesting service. Do not let its chat
		// inherit the service's invocation marker and falsely classify every
		// nested spawn as another service-owned process.
		command.Env = servicemanager.WithoutInvocationID(environment)
	}
	return command, nil
}
