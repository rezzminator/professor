//go:build darwin

package spawn

import (
	"context"
	"os/exec"

	"hostops/pfm/internal/paths"
)

func serviceScopeCommand(
	ctx context.Context,
	binary string,
	arguments, environment []string,
	_ paths.Env,
) (*exec.Cmd, error) {
	command := exec.CommandContext(ctx, binary, arguments...)
	command.Env = environment
	return command, nil
}
